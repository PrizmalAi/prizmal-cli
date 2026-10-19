package pi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

// TestPiConfigDeclaresTheContextWindow pins that pi writes the window. The
// catalog reports no window, so every model gets the one a launch declares:
// pi budgets and compacts from the declared window, and its own 128000 default
// would compact a session at a fraction of what the Switch accepts.
func TestPiConfigDeclaresTheContextWindow(t *testing.T) {
	cfg := createConfig(launch.LaunchModel{Name: "no-window-model"})

	got, ok := cfg["contextWindow"]
	if !ok {
		t.Fatalf("pi config has no contextWindow, want the declared %d", piFallbackContextWindow)
	}
	if got != piFallbackContextWindow {
		t.Fatalf("pi config contextWindow = %v, want %d", got, piFallbackContextWindow)
	}
}

// TestPiEditKeepsManagedEntryWithoutContextWindow pins the merge rule that
// replaced the stale-entry rebuild: a still-selected managed entry is kept as
// it stands. The rebuild it deletes was gated on a cloud-limit lookup that
// always reported no limit, so it never fired — and if it had, it would have
// dropped an entry that createConfig then rebuilt without the operator's own
// keys. An entry carrying a field prizmal does not write must survive an Edit.
func TestPiEditKeepsManagedEntryWithoutContextWindow(t *testing.T) {
	home := internaltest.SandboxedHome(t)
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

	if err := (&Pi{}).Edit([]launch.LaunchModel{{Name: "kept-model"}}); err != nil {
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
