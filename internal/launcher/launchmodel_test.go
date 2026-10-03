package launch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/model"
)

// TestLaunchModelCarriesOnlyCatalogFacts pins LaunchModel's field set.
//
// Every field in it has exactly one writer: parseSwitchCatalog fills Name,
// Capabilities, Tier and Description, and withClaudeTiers fills FoldedInto. A
// field with no writer is zero at every runtime, and a branch reading it looks
// live while picking its default on every launch. That is how Remote,
// ToolCapable, the Details struct, ContextLength and MaxOutputTokens grew six
// unreachable branches in codex, opencode and pi. This test is what keeps the
// next one from arriving: adding a field means adding the writer in the same
// change.
func TestLaunchModelCarriesOnlyCatalogFacts(t *testing.T) {
	want := []string{"Capabilities", "Description", "FoldedInto", "Name", "Tier"}

	var got []string
	typ := reflect.TypeFor[LaunchModel]()
	for i := range typ.NumField() {
		got = append(got, typ.Field(i).Name)
	}
	slices.Sort(got)

	if !slices.Equal(got, want) {
		t.Fatalf("LaunchModel fields = %v, want %v", got, want)
	}
}

// TestCodexEntryContextWindowFallsBackTo128k pins the surviving context-window
// rule. The Switch catalog reports no window for any entry, so codex ships the
// fallback and $HARNESS_CONTEXT_LENGTH is the only way to change it — the env
// var used to sit behind a guard (a cloud-model test and a safetensors-format
// test) that no catalog entry could ever fail.
func TestCodexEntryContextWindowFallsBackTo128k(t *testing.T) {
	entry := buildCodexModelEntry(LaunchModel{Name: "no-window-model"})

	if got := entry["context_window"]; got != codexFallbackContextWindow {
		t.Fatalf("context_window = %v, want %d", got, codexFallbackContextWindow)
	}
}

func TestCodexEntryHonorsHarnessContextLength(t *testing.T) {
	t.Setenv("HARNESS_CONTEXT_LENGTH", "200000")

	entry := buildCodexModelEntry(LaunchModel{Name: "no-window-model"})

	if got := entry["context_window"]; got != 200000 {
		t.Fatalf("context_window = %v, want 200000", got)
	}
}

// TestCodexTruncationIsAlwaysBytes pins the truncation mode for every model
// name, including one that reads as cloud-served. The mode used to be selected
// by a name test that always answered false, so no entry could ever ask for
// token truncation.
func TestCodexTruncationIsAlwaysBytes(t *testing.T) {
	for _, name := range []string{"gpt-5.6", "claude-opus-5[1m]", "some-cloud-model"} {
		policy, ok := buildCodexModelEntry(LaunchModel{Name: name})["truncation_policy"].(map[string]any)
		if !ok {
			t.Fatalf("%s: entry lacks a truncation_policy object", name)
		}
		if got := policy["mode"]; got != "bytes" {
			t.Fatalf("%s: truncation_policy.mode = %v, want bytes", name, got)
		}
		if got := policy["limit"]; got != 10000 {
			t.Fatalf("%s: truncation_policy.limit = %v, want 10000", name, got)
		}
	}
}

// TestOpenCodeEntryHasNoLimit pins that opencode's entry carries no limit
// object. No catalog entry reported an output-token budget, so the limit block
// never ran and opencode kept applying its own defaults; writing a limit here
// now would change the config the harness reads.
func TestOpenCodeEntryHasNoLimit(t *testing.T) {
	entry := opencodeModelEntry(t, LaunchModel{Name: "no-limit-model"})

	if got, ok := entry["limit"]; ok {
		t.Fatalf("entry has limit = %v, want no limit key", got)
	}
}

// TestOpenCodeThinkingLevelsComeFromTheName pins the reasoning-levels check
// that survives the removal of the serving-family fields. No catalog entry
// reported a family, so the family loop was a no-op and the model's own name
// decided the scale; opencode's two scales differ by model line, and answering
// "none of the above" costs a usable effort setting on a model that takes one.
func TestOpenCodeThinkingLevelsComeFromTheName(t *testing.T) {
	thinking := LaunchModel{
		Name:         "openai/gpt-oss-120b",
		Capabilities: []model.Capability{model.CapabilityThinking},
	}
	entry := opencodeModelEntry(t, thinking)

	if entry["reasoning"] != true {
		t.Fatalf("entry lacks reasoning = true: %v", entry)
	}
	options, _ := entry["options"].(map[string]any)
	if got := options["reasoningEffort"]; got != "medium" {
		t.Fatalf("options.reasoningEffort = %v, want medium", got)
	}
	variants, _ := entry["variants"].(map[string]any)
	for _, level := range []string{"low", "medium", "high", "max"} {
		effort, _ := variants[level].(map[string]any)["reasoningEffort"].(string)
		if effort != level {
			t.Fatalf("variants.%s.reasoningEffort = %q, want %q", level, effort, level)
		}
	}

	// A model off the gpt-oss line keeps opencode's other scale, so the check
	// is a name test and not a blanket "four levels".
	plain := LaunchModel{
		Name:         "anthropic/claude-sonnet-5",
		Capabilities: []model.Capability{model.CapabilityThinking},
	}
	plainVariants := opencodeModelEntry(t, plain)["variants"].(map[string]any)
	if _, ok := plainVariants["none"]; !ok {
		t.Fatalf("a non-gpt-oss model lost its none variant: %v", plainVariants)
	}
}

// TestPiConfigHasNoContextWindow pins that pi writes no contextWindow. The
// catalog reports no window, so every managed entry used to be born without
// one; writing a value here would be a guess the operator cannot check against
// the Switch.
func TestPiConfigHasNoContextWindow(t *testing.T) {
	cfg := createConfig(LaunchModel{Name: "no-window-model"})

	if got, ok := cfg["contextWindow"]; ok {
		t.Fatalf("pi config has contextWindow = %v, want none", got)
	}
}

// TestPiEditKeepsManagedEntryWithoutContextWindow pins the merge rule that
// replaced the stale-entry rebuild: a still-selected managed entry is kept as
// it stands. The rebuild it deletes was gated on a cloud-limit lookup that
// always reported no limit, so it never fired — and if it had, it would have
// dropped an entry that createConfig then rebuilt without the operator's own
// keys. An entry carrying a field prizmal does not write must survive an Edit.
func TestPiEditKeepsManagedEntryWithoutContextWindow(t *testing.T) {
	home := sandboxedHome(t)
	modelsPath := filepath.Join(home, ".pi", "agent", "models.json")
	const kept = "operator-set-by-hand"

	seed := map[string]any{"providers": map[string]any{piProviderID: map[string]any{
		"baseUrl": "https://example.invalid/v1",
		"models": []any{map[string]any{
			"id":      "kept-model",
			"_launch": true,
			"notes":   kept,
		}},
	}}}
	data, err := json.Marshal(seed)
	if err != nil {
		t.Fatalf("marshal seed config: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(modelsPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(modelsPath, data, 0o600); err != nil {
		t.Fatalf("seed models.json: %v", err)
	}

	if err := (&Pi{}).Edit([]LaunchModel{{Name: "kept-model"}}); err != nil {
		t.Fatalf("Pi.Edit: %v", err)
	}

	raw, err := os.ReadFile(modelsPath)
	if err != nil {
		t.Fatalf("read models.json: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("unmarshal models.json: %v", err)
	}
	providers, _ := cfg["providers"].(map[string]any)
	provider, _ := providers[piProviderID].(map[string]any)
	entries, _ := provider["models"].([]any)
	if len(entries) != 1 {
		t.Fatalf("pi provider has %d models, want 1: %v", len(entries), entries)
	}
	entry, _ := entries[0].(map[string]any)
	if got := entry["notes"]; got != kept {
		t.Fatalf("managed entry was rebuilt instead of kept: notes = %v, want %q (entry %v)", got, kept, entry)
	}
}
