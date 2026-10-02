package launch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/model"
)

// sandboxCodexHome points HOME and USERPROFILE at a temp dir so config writes
// never touch a real user config, and returns the dir.
func sandboxCodexHome(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	t.Setenv("HOME", d)
	t.Setenv("USERPROFILE", d)
	return d
}

// TestCodexProfileConfigSourcesKeyFromEnvNotDisk pins the credential channel
// for codex: the generated profile names the environment variable codex must
// read (env_key), and carries no literal api_key. prizmal sets that variable
// on the child at exec, so the key lives for one process instead of forever.
func TestCodexProfileConfigSourcesKeyFromEnvNotDisk(t *testing.T) {
	sandboxCodexHome(t)
	envconfig.SetAPIKey("not-a-real-key-codex-probe")
	t.Cleanup(func() { envconfig.SetAPIKey("") })

	profilePath := filepath.Join(t.TempDir(), "prizmal.config.toml")
	if err := writeCodexNamedProfileConfig(profilePath, codexProfileName, "gpt-test", "", ""); err != nil {
		t.Fatalf("writeCodexNamedProfileConfig: %v", err)
	}

	data, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	text := string(data)
	parsed, err := codexParseConfig(text)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	if got, ok := parsed.String("model_providers", codexProfileName, "env_key"); !ok || got != "OPENAI_API_KEY" {
		t.Fatalf("env_key = %q (ok=%v), want OPENAI_API_KEY", got, ok)
	}
	if got, ok := parsed.String("model_providers", codexProfileName, "api_key"); ok {
		t.Fatalf("api_key = %q is present; the profile must carry no credential at rest", got)
	}
	if strings.Contains(text, "not-a-real-key-codex-probe") {
		t.Fatalf("the provider key appears in the generated profile:\n%s", text)
	}
}

// TestCodexProfileConfigNeverWritesPlaceholderKey guards against regressing to
// the old placeholder credential, and against api_key coming back at all.
func TestCodexProfileConfigNeverWritesPlaceholderKey(t *testing.T) {
	sandboxCodexHome(t)
	envconfig.SetAPIKey("")
	t.Cleanup(func() { envconfig.SetAPIKey("") })

	profilePath := filepath.Join(t.TempDir(), "prizmal.config.toml")
	if err := writeCodexNamedProfileConfig(profilePath, codexProfileName, "gpt-test", "", ""); err != nil {
		t.Fatalf("writeCodexNamedProfileConfig: %v", err)
	}
	data, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	text := string(data)

	for _, bad := range []string{
		"api_key",
		"OPENAI_API_KEY=harness-launch",
		"OPENAI_API_KEY=ollama",
	} {
		if strings.Contains(text, bad) {
			t.Fatalf("found %q in config:\n%s", bad, text)
		}
	}
}

// codexContextWindow pulls context_window off a codex catalog entry.
func codexContextWindow(t *testing.T, entry map[string]any) int {
	t.Helper()
	got, ok := entry["context_window"].(int)
	if !ok {
		t.Fatalf("codex entry lacks an int context_window: %v", entry["context_window"])
	}
	return got
}

// The declared window is the documented fallback for every model, because
// GET /v1/models carries no context length: the Switch sends id,
// input_modalities, tier and description, and no writer in the tree ever fills
// LaunchModel.ContextLength or LaunchModel.Details. Pinning it here means a
// writer added later without a wire from the catalog fails here instead of
// leaving a session to compact at 90% of a window nobody chose.
func TestCodexEntryDeclaresTheDocumentedContextWindow(t *testing.T) {
	entry := buildCodexModelEntry(LaunchModel{
		Name:         "smart",
		Capabilities: []model.Capability{model.CapabilityVision, model.CapabilityCompletion},
	})

	if got := codexContextWindow(t, entry); got != 1_000_000 {
		t.Fatalf("context_window = %d, want 1000000: every model the Switch serves has a 1M window", got)
	}
}

// $HARNESS_CONTEXT_LENGTH is the only way an operator can state a real window,
// so it has to win over the fallback for a model whose window the catalog
// cannot describe.
func TestCodexEntryHonoursTheOperatorStatedContextWindow(t *testing.T) {
	t.Setenv("HARNESS_CONTEXT_LENGTH", "1000000")

	entry := buildCodexModelEntry(LaunchModel{Name: "smart"})

	if got := codexContextWindow(t, entry); got != 1_000_000 {
		t.Fatalf("context_window = %d, want the operator's 1000000", got)
	}
}

// An unset or unparseable $HARNESS_CONTEXT_LENGTH must fall back rather than
// declare a zero window, which codex reads as no window at all.
func TestCodexEntryIgnoresAnUnusableContextWindowOverride(t *testing.T) {
	t.Setenv("HARNESS_CONTEXT_LENGTH", "not-a-number")

	entry := buildCodexModelEntry(LaunchModel{Name: "smart"})

	if got := codexContextWindow(t, entry); got != codexFallbackContextWindow {
		t.Fatalf("context_window = %d, want the fallback %d for an unparseable override", got, codexFallbackContextWindow)
	}
}

// Tool output is truncated in tokens, matching what codex's own catalog
// declares for every model it ships. Codex reads a bytes-mode limit as bytes,
// so a mode of "bytes" cut every tool's output at 10000 bytes — a quarter of
// the budget the vendor default gives it — on a branch that could never select
// anything else.
func TestCodexEntryTruncatesToolOutputInTokens(t *testing.T) {
	policy, ok := buildCodexModelEntry(LaunchModel{Name: "smart"})["truncation_policy"].(map[string]any)
	if !ok {
		t.Fatal("codex entry lacks a truncation_policy map")
	}

	if mode := policy["mode"]; mode != "tokens" {
		t.Fatalf("truncation mode = %v, want tokens", mode)
	}
	if limit := policy["limit"]; limit != 10000 {
		t.Fatalf("truncation limit = %v, want 10000", limit)
	}
}

// base_instructions stays on the entry even though it is empty, which is not
// the same as leaving it out: codex accepts a present empty template and runs
// the session with it, while the catalog decoder rejects a model that carries
// neither base_instructions nor model_messages.instructions_template. The key
// cannot be dropped until the entry carries a real template.
func TestCodexEntryKeepsTheExplicitBaseInstructionsKey(t *testing.T) {
	entry := buildCodexModelEntry(LaunchModel{Name: "smart"})

	instructions, ok := entry["base_instructions"]
	if !ok {
		t.Fatal("codex entry omits base_instructions; codex rejects a model with no instruction template at all")
	}
	if instructions != "" {
		t.Fatalf("base_instructions = %v, want the empty template the entry has always written", instructions)
	}
}

// The model catalog prizmal writes for codex carries the entry the Switch
// described. The launch names its model exactly as the operator typed it, which
// can differ from the catalog's spelling by the [1m] decoration the Switch puts
// on every id; a lookup that compared the strings would write a bare entry with
// no capabilities, and codex would then be told nothing about what the model
// takes as input.
func TestCodexCatalogModelMatchesAcrossTheSuffix(t *testing.T) {
	catalog := []LaunchModel{
		{Name: "vision-model", Capabilities: capabilitiesFromModalities([]string{"text", "image"})},
	}

	got := codexCatalogModel("vision-model[1m]", catalog)
	if !got.HasCapability("vision") {
		t.Fatalf("codex catalog model = %+v, want the catalog entry's capabilities", got)
	}
	if got.Name != "vision-model[1m]" {
		t.Fatalf("codex catalog model name = %q, want the name the launch asked for", got.Name)
	}
}

// A model the catalog does not hold stays a bare entry: codex gets a name that
// routes and nothing it cannot know.
func TestCodexCatalogModelFallsBackForAnUnknownName(t *testing.T) {
	got := codexCatalogModel("not-in-catalog", []LaunchModel{{Name: "vision-model"}})
	if got.Name != "not-in-catalog" || len(got.Capabilities) != 0 {
		t.Fatalf("codex catalog model = %+v, want a bare entry named not-in-catalog", got)
	}
}
