package opencode

import (
	"encoding/json"
	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
	"slices"
	"strings"
	"testing"

	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/launchtest"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
)

func opencodePrizmalProvider(t *testing.T, primary launch.LaunchModel, models []launch.LaunchModel) map[string]any {
	t.Helper()
	content, err := buildInlineConfig(primary, models)
	if err != nil {
		t.Fatalf("buildInlineConfig: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(content), &cfg); err != nil {
		t.Fatalf("unmarshal opencode config: %v", err)
	}
	provider, _ := cfg["provider"].(map[string]any)
	prizmal, ok := provider["prizmal"].(map[string]any)
	if !ok {
		t.Fatalf("opencode config lacks a prizmal provider: %v", provider)
	}
	return prizmal
}

// opencodeOptionsAPIKey renders buildInlineConfig and returns the apiKey set
// on provider.prizmal.options, or "" if the field is absent.
func opencodeOptionsAPIKey(t *testing.T) (string, map[string]any) {
	t.Helper()
	prizmal := opencodePrizmalProvider(t, launchtest.WireKeyTestModel()[0], launchtest.WireKeyTestModel())
	opts, _ := prizmal["options"].(map[string]any)
	key, _ := opts["apiKey"].(string)
	return key, opts
}

func TestOpenCodeInjectsAPIKey(t *testing.T) {
	envconfig.SetAPIKey("sk-opencode")
	key, _ := opencodeOptionsAPIKey(t)
	if key != "sk-opencode" {
		t.Fatalf("opencode provider.prizmal.options.apiKey is not the launch key")
	}
	envconfig.SetAPIKey("")
}

func TestOpenCodeProviderIDIsPrizmal(t *testing.T) {
	envconfig.SetAPIKey("sk-opencode")
	content, err := buildInlineConfig(launchtest.WireKeyTestModel()[0], launchtest.WireKeyTestModel())
	if err != nil {
		t.Fatalf("buildInlineConfig: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(content), &cfg); err != nil {
		t.Fatalf("unmarshal opencode config: %v", err)
	}
	// Provider block keyed 'prizmal', not 'ollama'.
	provider, _ := cfg["provider"].(map[string]any)
	if _, ok := provider["prizmal"]; !ok {
		t.Fatalf("opencode provider lacks 'prizmal' id; got keys %v", provider)
	}
	if _, ok := provider["ollama"]; ok {
		t.Fatalf("opencode provider still exposes legacy 'ollama' id")
	}
	// Model prefix 'prizmal/', and no literal 'ollama' anywhere in the config.
	model, _ := cfg["model"].(string)
	if !strings.HasPrefix(model, "prizmal/") {
		t.Fatalf("opencode model = %q, want prefix prizmal/", model)
	}
	if strings.Contains(content, "ollama") {
		t.Fatalf("opencode config still contains 'ollama': %s", content)
	}
	envconfig.SetAPIKey("")
}

func TestOpenCodeEmptyKeyIsEmptyNotPlaceholder(t *testing.T) {
	internaltest.ClearCredentialEnv(t)
	envconfig.SetAPIKey("")
	key, opts := opencodeOptionsAPIKey(t)
	if key != "" {
		t.Fatalf("empty key: opencode options.apiKey is not empty")
	}
	for field, v := range opts {
		if s, ok := v.(string); ok {
			if s == "ollama" || s == "harness-launch" || s == "ollama-local" {
				t.Fatalf("opencode options %q holds placeholder %q with empty key: %+v", field, s, opts)
			}
		}
	}
}

func TestOpenCodeManagedProviderAcceptsPrizmalOnly(t *testing.T) {
	cases := []struct {
		id   string
		want bool
	}{
		{"prizmal", true},
		{"ollama", false},
		{"openai", false},
		{"anthropic", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isOpenCodeManagedProvider(map[string]any{"providerID": tc.id}); got != tc.want {
			t.Fatalf("isOpenCodeManagedProvider(%q) = %v, want %v", tc.id, got, tc.want)
		}
	}
}

// opencodeModelEntry renders buildInlineConfig for a single model and returns
// that model's entry from provider.prizmal.models.
func opencodeModelEntry(t *testing.T, m launch.LaunchModel) map[string]any {
	t.Helper()
	prizmal := opencodePrizmalProvider(t, m, []launch.LaunchModel{m})
	models, _ := prizmal["models"].(map[string]any)
	entry, ok := models[m.Name].(map[string]any)
	if !ok {
		t.Fatalf("opencode config lacks a model entry for %q; got %v", m.Name, models)
	}
	return entry
}

// opencodeInputModalities returns modalities.input for a model entry.
func opencodeInputModalities(t *testing.T, entry map[string]any) []string {
	t.Helper()
	modalities, ok := entry["modalities"].(map[string]any)
	if !ok {
		t.Fatalf("model entry lacks a modalities object: %v", entry)
	}
	raw, _ := modalities["input"].([]any)
	var input []string
	for _, v := range raw {
		s, _ := v.(string)
		input = append(input, s)
	}
	return input
}

// A normal launch supplies a model name and no capability metadata. OpenCode
// only sends an attached PDF when the entry declares the pdf input modality.
func TestOpenCodeDeclaresPDFInputModality(t *testing.T) {
	entry := opencodeModelEntry(t, launch.LaunchModel{Name: "wire-test-model"})
	input := opencodeInputModalities(t, entry)
	for _, want := range []string{"text", "image", "pdf"} {
		if !slices.Contains(input, want) {
			t.Fatalf("modalities.input = %v, want it to contain %q", input, want)
		}
	}
}

// The output modality stays text-only.
func TestOpenCodeOutputModalityIsTextOnly(t *testing.T) {
	entry := opencodeModelEntry(t, launch.LaunchModel{Name: "wire-test-model"})
	modalities, _ := entry["modalities"].(map[string]any)
	output, _ := modalities["output"].([]any)
	if len(output) != 1 || output[0] != "text" {
		t.Fatalf("modalities.output = %v, want [text]", output)
	}
}
