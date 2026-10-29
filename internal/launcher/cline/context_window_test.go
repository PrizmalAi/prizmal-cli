package cline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

func clineSettingsOf(t *testing.T, dataDir string) map[string]any {
	t.Helper()
	cfg := internaltest.SandboxedJSON(t, dataDir, filepath.Join("settings", "providers.json"))
	providers, _ := cfg["providers"].(map[string]any)
	entry, _ := providers[clineApiProvider].(map[string]any)
	settings, _ := entry["settings"].(map[string]any)
	if settings == nil {
		t.Fatalf("no %s settings in %v", clineApiProvider, cfg)
	}
	return settings
}

// Cline sizes its compaction budget from the provider's contextWindow setting
// and falls back to 128,000 tokens when it is absent, so every launch declares
// the window the Switch rules for a model.
func TestClineLaunchDeclaresTheContextWindow(t *testing.T) {
	internaltest.SandboxedHome(t)
	dir := t.TempDir()
	t.Setenv("CLINE_DATA_DIR", dir)
	c := &Cline{}
	for _, model := range []string{"first-model", "second-model"} {
		if err := c.Edit([]launch.LaunchModel{{Name: model}}); err != nil {
			t.Fatal(err)
		}
		settings := clineSettingsOf(t, dir)
		if got, _ := settings["contextWindow"].(float64); got != 1_000_000 {
			t.Fatalf("%s: contextWindow = %v, want 1000000", model, settings["contextWindow"])
		}
		if settings["model"] != model {
			t.Fatalf("model = %v, want %s", settings["model"], model)
		}
	}
}

func TestClineRelocatedLaunchTreeDeclaresTheContextWindow(t *testing.T) {
	internaltest.SandboxedHome(t)
	dir := filepath.Join(t.TempDir(), "data")
	if err := writeClineDataDir(dir, true, "probe-model"); err != nil {
		t.Fatal(err)
	}
	if got, _ := clineSettingsOf(t, dir)["contextWindow"].(float64); got != 1_000_000 {
		t.Fatalf("contextWindow = %v, want 1000000", got)
	}
}

// A provider the user keeps beside prizmal's entry is not ours to touch.
func TestClineContextWindowLeavesOtherProvidersAlone(t *testing.T) {
	internaltest.SandboxedHome(t)
	dir := t.TempDir()
	t.Setenv("CLINE_DATA_DIR", dir)
	other := `{"settings":{"provider":"anthropic","model":"mine","contextWindow":4242},"updatedAt":"2026-01-01T00:00:00Z","tokenSource":"manual"}`
	internaltest.SandboxedSeed(t, dir, filepath.Join("settings", "providers.json"),
		`{"version":1,"lastUsedProvider":"anthropic","providers":{"anthropic":`+other+`}}`)
	c := &Cline{}
	if err := c.Edit([]launch.LaunchModel{{Name: "probe-model"}}); err != nil {
		t.Fatal(err)
	}
	var want, got any
	if err := json.Unmarshal([]byte(other), &want); err != nil {
		t.Fatal(err)
	}
	cfg := internaltest.SandboxedJSON(t, dir, filepath.Join("settings", "providers.json"))
	got = cfg["providers"].(map[string]any)["anthropic"]
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("anthropic entry = %v, want %v", got, want)
	}
}

// Restore removes the declaration with the entry prizmal wrote, and takes it
// out of an entry the user adopted as well, where only the credential fields
// come out.
func TestClineRestoreRemovesTheContextWindow(t *testing.T) {
	internaltest.SandboxedHome(t)
	dir := t.TempDir()
	t.Setenv("CLINE_DATA_DIR", dir)
	c := &Cline{}
	if err := c.Edit([]launch.LaunchModel{{Name: "probe-model"}}); err != nil {
		t.Fatal(err)
	}
	cfgPath := clineProvidersPath(dir)
	cfg, err := launch.ReadJSONFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	settings := cfg["providers"].(map[string]any)[clineApiProvider].(map[string]any)["settings"].(map[string]any)
	settings["temperature"] = 0.2 // the user's own field makes the entry theirs
	if err := launch.WriteJSONFile0600(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Restore(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(after, &out); err != nil {
		t.Fatal(err)
	}
	kept := out["providers"].(map[string]any)[clineApiProvider].(map[string]any)["settings"].(map[string]any)
	if _, ok := kept["contextWindow"]; ok {
		t.Fatalf("contextWindow survived Restore: %v", kept)
	}
	if kept["temperature"] != 0.2 {
		t.Fatalf("the user's field was lost: %v", kept)
	}
}

func TestClineRestoreDropsTheWholeEntryWithItsContextWindow(t *testing.T) {
	internaltest.SandboxedHome(t)
	dir := t.TempDir()
	t.Setenv("CLINE_DATA_DIR", dir)
	c := &Cline{}
	if err := c.Edit([]launch.LaunchModel{{Name: "probe-model"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Restore(); err != nil {
		t.Fatal(err)
	}
	cfg := internaltest.SandboxedJSON(t, dir, filepath.Join("settings", "providers.json"))
	if providers, _ := cfg["providers"].(map[string]any); providers[clineApiProvider] != nil {
		t.Fatalf("entry survived Restore: %v", providers)
	}
}
