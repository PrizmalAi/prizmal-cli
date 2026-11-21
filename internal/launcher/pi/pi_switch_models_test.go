package pi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

// piCatalog is a Switch catalog shaped like the one Staging serves: the ids
// carry the [1m] context-budget suffix, which the launcher strips.
var piCatalog = []launch.LaunchModel{
	{Name: "frontier"},
	{Name: "core"},
	{Name: "flash"},
	{Name: "premier"},
	{Name: "test"},
}

// pinPiShowsModelList asserts at compile time that Pi fills its own picker.
// Pi's /model lists the models in the provider entry the launch writes, so a
// launch that writes only the model it runs leaves the picker unable to offer
// the rest, and a user who switches away cannot switch back.
func pinPiShowsModelList() bool {
	var shower launch.ModelListShower = &Pi{}
	return shower.ShowsModelList()
}

// TestPiEditWritesEveryCatalogModel pins the first contract: the provider entry
// a launch writes carries every model the Switch serves, so Pi's /model lists
// them all. main hands Edit the whole catalog only when the runner reports it
// shows a model list, and LaunchModels keeps the launched model first.
func TestPiEditWritesEveryCatalogModel(t *testing.T) {
	home := internaltest.SandboxedHome(t)
	if !pinPiShowsModelList() {
		t.Fatal("Pi reports no model list, so a launch writes only the model it runs")
	}

	const chosen = "flash"
	if err := (&Pi{}).Edit(launch.LaunchModels(chosen, piCatalog, true)); err != nil {
		t.Fatalf("Pi.Edit: %v", err)
	}

	provider := parseProvider(t, readFile(t, filepath.Join(home, ".pi", "agent", "models.json")), piProviderID)
	entries, _ := provider["models"].([]any)
	var ids []string
	for _, entry := range entries {
		modelObj, _ := entry.(map[string]any)
		id, _ := modelObj["id"].(string)
		ids = append(ids, id)
		if !isManagedModel(modelObj) {
			t.Fatalf("catalog entry %q is not launch-managed: %v", id, modelObj)
		}
	}
	if len(ids) != len(piCatalog) {
		t.Fatalf("provider lists %v, want all %d catalog models", ids, len(piCatalog))
	}
	if ids[0] != chosen {
		t.Fatalf("first entry is %q, want the launched model %q so a bare pi run starts on it", ids[0], chosen)
	}
	for _, model := range piCatalog {
		if !slices.Contains(ids, model.Name) {
			t.Fatalf("provider lists %v, missing %q", ids, model.Name)
		}
	}
}

// TestPiEditKeepsTheUsersModels pins the second contract: Edit only adds or
// updates the entries it owns. A foreign provider, an operator's own entry
// under the prizmal provider (one shaped like a model, one shaped like nothing
// pi knows), and the settings it does not own come out as they went in.
func TestPiEditKeepsTheUsersModels(t *testing.T) {
	home := internaltest.SandboxedHome(t)
	modelsPath := filepath.Join(home, ".pi", "agent", "models.json")
	if err := os.MkdirAll(filepath.Dir(modelsPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	modelsBefore := []byte(`{
  "providers": {
    "openrouter": {
      "baseUrl": "https://openrouter.example.invalid/v1",
      "api": "openai-completions",
      "apiKey": "$OPERATOR_KEY",
      "models": [
        {"id": "zeta", "input": ["text"]},
        {"id": "alpha", "input": ["text", "image"], "reasoning": true}
      ]
    },
    "prizmal": {
      "baseUrl": "https://switch.example.invalid/v1",
      "api": "openai-completions",
      "apiKey": "$SWITCH_KEY",
      "models": [
        {"id": "operator-model", "input": ["text"], "contextWindow": 65536},
        {"note": "an entry pi does not know"}
      ]
    }
  }
}
`)
	if err := os.WriteFile(modelsPath, modelsBefore, 0o600); err != nil {
		t.Fatalf("seed models.json: %v", err)
	}

	if err := (&Pi{}).Edit([]launch.LaunchModel{{Name: "frontier"}}); err != nil {
		t.Fatalf("Pi.Edit: %v", err)
	}

	after := readFile(t, modelsPath)
	foreignBefore := renderedProvider(t, modelsBefore, "openrouter")
	if foreignAfter := renderedProvider(t, after, "openrouter"); foreignBefore != foreignAfter {
		t.Fatalf("openrouter provider changed\nbefore: %s\nafter:  %s", foreignBefore, foreignAfter)
	}

	entries := modelEntries(t, after)
	if len(entries) != 3 {
		t.Fatalf("prizmal provider has %d entries, want the operator's two plus the launch's: %v", len(entries), entries)
	}
	first, _ := entries[0].(map[string]any)
	if first["id"] != "operator-model" || first["contextWindow"] != float64(65536) {
		t.Fatalf("the operator's own model moved or changed: %v", first)
	}
	if isManagedModel(first) {
		t.Fatalf("Edit claimed the operator's entry: %v", first)
	}
	second, _ := entries[1].(map[string]any)
	if second["note"] != "an entry pi does not know" {
		t.Fatalf("Edit dropped an entry it does not understand: %v", second)
	}
}

// TestPiEditRefusesToClobberAFileItCannotParse pins that a file Edit cannot
// parse is left exactly as it was. An ignored parse error starts from an
// empty document, so the write that follows drops every entry the operator
// had. Refusing is what keeps "never overwrites" true even for a file with a
// trailing comma, in models.json and in settings.json alike.
func TestPiEditRefusesToClobberAFileItCannotParse(t *testing.T) {
	for _, tc := range []struct {
		name string
		file string
	}{
		{name: "models.json", file: "models.json"},
		{name: "settings.json", file: "settings.json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := internaltest.SandboxedHome(t)
			path := filepath.Join(home, ".pi", "agent", tc.file)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			broken := []byte("{\n  \"providers\": {\n    \"openrouter\": {\"models\": []},\n")
			if err := os.WriteFile(path, broken, 0o600); err != nil {
				t.Fatalf("seed %s: %v", tc.file, err)
			}

			if err := (&Pi{}).Edit([]launch.LaunchModel{{Name: "frontier"}}); err == nil {
				t.Fatalf("Pi.Edit accepted a %s it cannot parse", tc.file)
			}
			if after := readFile(t, path); string(after) != string(broken) {
				t.Fatalf("Edit rewrote a file it cannot parse\nbefore: %s\nafter:  %s", broken, after)
			}
		})
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func parseJSON(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return value
}

func parseProvider(t *testing.T, data []byte, id string) map[string]any {
	t.Helper()
	config := parseJSON(t, data)
	providers, _ := config["providers"].(map[string]any)
	provider, ok := providers[id].(map[string]any)
	if !ok {
		t.Fatalf("no %q provider in %s", id, data)
	}
	return provider
}

// modelEntries returns the prizmal provider's model entries in file order.
func modelEntries(t *testing.T, data []byte) []any {
	t.Helper()
	entries, _ := parseProvider(t, data, piProviderID)["models"].([]any)
	return entries
}

// renderedProvider renders one provider's fields and model entries, sorted by
// field name, so two documents can be compared without their key order
// standing in for a content change. Model entries stay in file order.
func renderedProvider(t *testing.T, data []byte, id string) string {
	t.Helper()
	provider := parseProvider(t, data, id)
	keys := make([]string, 0, len(provider))
	for key := range provider {
		if key != "models" {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	rendered := make(map[string]any, len(provider))
	for _, key := range keys {
		rendered[key] = provider[key]
	}
	rendered["models"] = provider["models"]
	encoded, err := json.Marshal(rendered)
	if err != nil {
		t.Fatalf("marshal provider: %v", err)
	}
	return string(encoded)
}
