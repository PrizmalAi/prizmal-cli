package kilo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/PrizmalAi/prizmal-cli/internal/model"
)

func inlineConfig(t *testing.T, model string, catalog []launch.LaunchModel) map[string]any {
	t.Helper()
	raw, err := buildInlineConfig(model, catalog)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		t.Fatal(err)
	}
	return config
}

// TestInlineConfigDeclaresTheProvider pins the provider block Kilo reads from
// KILO_CONFIG_CONTENT: the OpenAI-compatible package, the Switch endpoint, and
// the launch's model first.
func TestInlineConfigDeclaresTheProvider(t *testing.T) {
	config := inlineConfig(t, "smart", []launch.LaunchModel{{Name: "fast"}, {Name: "smart"}})

	if config["model"] != "prizmal/smart" {
		t.Fatalf("model = %v, want prizmal/smart", config["model"])
	}
	provider := config["provider"].(map[string]any)["prizmal"].(map[string]any)
	if provider["npm"] != "@ai-sdk/openai-compatible" {
		t.Fatalf("npm = %v", provider["npm"])
	}
	options := provider["options"].(map[string]any)
	if options["baseURL"] != envconfig.OpenAIBaseURL(false) {
		t.Fatalf("baseURL = %v", options["baseURL"])
	}
	models := provider["models"].(map[string]any)
	for _, name := range []string{"smart", "fast"} {
		entry, _ := models[name].(map[string]any)
		limit, _ := entry["limit"].(map[string]any)
		if limit["context"] != float64(1_000_000) {
			t.Fatalf("model %s limit = %v, want a 1000000 context", name, entry["limit"])
		}
	}
}

// TestInlineConfigRegistersAModelOutsideTheCatalog pins that a model named on
// the command line but absent from the catalog still gets an entry.
func TestInlineConfigRegistersAModelOutsideTheCatalog(t *testing.T) {
	config := inlineConfig(t, "custom", nil)
	models := config["provider"].(map[string]any)["prizmal"].(map[string]any)["models"].(map[string]any)
	if _, ok := models["custom"]; !ok {
		t.Fatalf("models = %v, want an entry for custom", models)
	}
}

// TestInlineConfigDisablesHostedWebsearch pins that Kilo's built-in websearch,
// which posts outside the Switch, is off through both config blocks.
func TestInlineConfigDisablesHostedWebsearch(t *testing.T) {
	config := inlineConfig(t, "smart", nil)
	if config["tools"].(map[string]any)["websearch"] != false {
		t.Fatalf("tools = %v", config["tools"])
	}
	if config["permission"].(map[string]any)["websearch"] != "deny" {
		t.Fatalf("permission = %v", config["permission"])
	}
}

// TestInlineConfigRequiresAModel pins the error for a launch with no model.
func TestInlineConfigRequiresAModel(t *testing.T) {
	if _, err := buildInlineConfig("", nil); err == nil {
		t.Fatal("expected an error for an empty model")
	}
}

// TestInlineConfigCarriesTheRelayPlaceholder pins that a relay launch puts the
// per-launch placeholder in the config and never the switch key.
func TestInlineConfigCarriesTheRelayPlaceholder(t *testing.T) {
	envconfig.SetRelay("http://127.0.0.1:4242", "placeholder-1")
	t.Cleanup(envconfig.ClearRelay)

	raw, err := buildInlineConfig("smart", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, `"apiKey":"placeholder-1"`) || !strings.Contains(raw, ":4242/v1") {
		t.Fatalf("config = %s, want the relay URL and placeholder", raw)
	}
}

func TestRunsHeadlessOnTheRunSubcommand(t *testing.T) {
	k := &Kilo{}
	for args, want := range map[string]bool{
		"run hello":                true,
		"--model prizmal/x run hi": true,
		"":                         false,
		"--model run":              false,
		"-- run":                   false,
	} {
		if got := k.RunsHeadless(strings.Fields(args)); got != want {
			t.Errorf("RunsHeadless(%q) = %v, want %v", args, got, want)
		}
	}
}

func TestInstallerUsesNpm(t *testing.T) {
	name, args, err := KiloInstallerCommand("linux")
	if err != nil || name != "npm" || args[len(args)-1] != "@kilocode/cli@latest" {
		t.Fatalf("install command = %s %v, %v", name, args, err)
	}
}

// TestLaunchArgsPutTheModelAfterRun pins where --model goes: before a TUI
// launch's arguments, and after the run subcommand.
func TestLaunchArgsPutTheModelAfterRun(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", "--model prizmal/m"},
		{"run hi", "run --model prizmal/m hi"},
		{"--agent x run hi", "--agent x run --model prizmal/m hi"},
		{"--agent run", "--model prizmal/m --agent run"},
	} {
		got := strings.Join(launchArgs("m", strings.Fields(tc.in)), " ")
		if got != tc.want {
			t.Errorf("launchArgs(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestEnvPointsKiloAtTheSearchTool pins that the launch hands Kilo the
// directory of the generated search tool and the variables the tool reads.
func TestEnvPointsKiloAtTheSearchTool(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	env := strings.Join((&Kilo{}).envVars("smart", "{}"), "\n")
	for _, want := range []string{"KILO_CONFIG_CONTENT={}", "KILO_CONFIG_DIR=", "PRIZMAL_OPENCODE_SEARCH_MODEL=smart"} {
		if !strings.Contains(env, want) {
			t.Errorf("env lacks %q:\n%s", want, env)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".cache", "prizmal", "kilo", "tool", "prizmal_search.ts")); err != nil {
		t.Errorf("search tool not written: %v", err)
	}
}

// TestInlineConfigOffersOnlyThePrizmalProviderAndImages pins the two fields the
// e2e baselines depend on: no network-fetched gateway models, and image input.
func TestInlineConfigOffersOnlyThePrizmalProviderAndImages(t *testing.T) {
	config := inlineConfig(t, "smart", nil)

	enabled, _ := config["enabled_providers"].([]any)
	if len(enabled) != 1 || enabled[0] != "prizmal" {
		t.Fatalf("enabled_providers = %v, want [prizmal]", config["enabled_providers"])
	}
	models := config["provider"].(map[string]any)["prizmal"].(map[string]any)["models"].(map[string]any)
	input := models["smart"].(map[string]any)["modalities"].(map[string]any)["input"]
	if got := strings.Join(toStrings(input), ","); got != "text,image" {
		t.Fatalf("modalities.input = %s, want text,image", got)
	}
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

// TestInlineConfigInputsFollowTheModelsModalities pins the input modalities of
// Kilo's entry to what the model's input_modalities list says: text always,
// image for the image capability, pdf for the document capability. A model that
// reports no list keeps text and image, and never gets pdf.
func TestInlineConfigInputsFollowTheModelsModalities(t *testing.T) {
	for _, tc := range []struct {
		name  string
		model launch.LaunchModel
		want  string
	}{
		{"image only", launch.LaunchModel{Name: "m", Capabilities: []model.Capability{model.CapabilityCompletion, model.CapabilityVision}}, "text,image"},
		{"image and file", launch.LaunchModel{Name: "m", Capabilities: []model.Capability{model.CapabilityCompletion, model.CapabilityVision, model.CapabilityDocument}}, "text,image,pdf"},
		{"text only", launch.LaunchModel{Name: "m", Capabilities: []model.Capability{model.CapabilityCompletion}}, "text"},
		{"no list", launch.LaunchModel{Name: "m"}, "text,image"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := inlineConfig(t, "m", []launch.LaunchModel{tc.model})
			models := config["provider"].(map[string]any)["prizmal"].(map[string]any)["models"].(map[string]any)
			input := models["m"].(map[string]any)["modalities"].(map[string]any)["input"]
			if got := strings.Join(toStrings(input), ","); got != tc.want {
				t.Fatalf("modalities.input = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestInlineConfigPerAPI pins the provider package and endpoint each of Kilo's
// three API modes needs. The default is chat completions; the other two are
// opt-in. Each package appends its own path to the base, so all three bases are
// the Switch's /v1.
func TestInlineConfigPerAPI(t *testing.T) {
	for _, tc := range []struct {
		api string
		npm string
	}{
		{"chat", "@ai-sdk/openai-compatible"},
		{"responses", "@ai-sdk/openai"},
		{"anthropic", "@ai-sdk/anthropic"},
	} {
		t.Run(tc.api, func(t *testing.T) {
			raw, err := buildInlineConfigFor(tc.api, "smart", nil)
			if err != nil {
				t.Fatal(err)
			}
			var config map[string]any
			if err := json.Unmarshal([]byte(raw), &config); err != nil {
				t.Fatal(err)
			}
			provider := config["provider"].(map[string]any)["prizmal"].(map[string]any)
			if provider["npm"] != tc.npm {
				t.Errorf("npm = %v, want %s", provider["npm"], tc.npm)
			}
			options := provider["options"].(map[string]any)
			if options["baseURL"] != envconfig.OpenAIBaseURL(false) {
				t.Errorf("baseURL = %v, want the Switch /v1", options["baseURL"])
			}
			if _, ok := provider["models"].(map[string]any)["smart"]; !ok {
				t.Errorf("models = %v, want an entry for smart", provider["models"])
			}
		})
	}
}

func TestSetAPIRejectsAnUnknownMode(t *testing.T) {
	k := &Kilo{}
	if err := k.SetAPI("bogus"); err == nil || !strings.Contains(err.Error(), "chat, responses, anthropic") {
		t.Fatalf("SetAPI(bogus) = %v, want an error naming the modes", err)
	}
	for _, ok := range []string{"", "chat", "responses", "anthropic"} {
		if err := k.SetAPI(ok); err != nil {
			t.Errorf("SetAPI(%q) = %v", ok, err)
		}
	}
}
