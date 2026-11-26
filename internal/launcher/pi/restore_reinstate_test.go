package pi

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

var piSettingsPath = filepath.Join(".pi", "agent", "settings.json")

// TestPiRestoreReinstatesPreviousDefaults: a user who had a provider and a
// model of their own before the first launch gets both back on --restore,
// and everything else in settings.json stays as it was. The second persist
// stands in for a later `prizmal --model other --persist pi`: the pre-launch
// copy has to survive it, and so does a second persist inside the same
// second, which is how the backups get named.
func TestPiRestoreReinstatesPreviousDefaults(t *testing.T) {
	home := internaltest.SandboxedHome(t)
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)

	internaltest.SandboxedSeed(t, home, piModelsPath, `{"providers":{"myprov":{"baseUrl":"https://myprov.example/v1",`+
		`"api":"openai-completions","apiKey":"their-own-fake-key","models":[{"id":"user-model-1"}]}}}`)
	internaltest.SandboxedSeed(t, home, piSettingsPath, `{"defaultProvider":"myprov","defaultModel":"user-model-1","theme":"dark"}`)

	p := &Pi{}
	for _, model := range []string{"probe-model-a", "probe-model-b"} {
		if err := p.Edit([]launch.LaunchModel{{Name: model}}); err != nil {
			t.Fatalf("Pi.Edit(%s): %v", model, err)
		}
	}
	outcome, err := p.Restore()
	if err != nil {
		t.Fatalf("Pi.Restore: %v", err)
	}

	settings := internaltest.SandboxedJSON(t, home, piSettingsPath)
	if got, _ := settings["defaultProvider"].(string); got != "myprov" {
		t.Errorf("defaultProvider = %q, want myprov put back", got)
	}
	if got, _ := settings["defaultModel"].(string); got != "user-model-1" {
		t.Errorf("defaultModel = %q, want user-model-1 put back", got)
	}
	if got, _ := settings["theme"].(string); got != "dark" {
		t.Errorf("theme = %q, want dark left alone", got)
	}

	providers, _ := internaltest.SandboxedJSON(t, home, piModelsPath)["providers"].(map[string]any)
	if _, ok := providers[piProviderID]; ok {
		t.Error("prizmal provider entry survived restore")
	}
	if _, ok := providers["myprov"]; !ok {
		t.Error("restore removed the user's own provider")
	}

	if !outcome.Removed {
		t.Error("outcome.Removed = false, want true: a persisted launch was taken out")
	}
	want := []string{"defaultProvider=myprov", "defaultModel=user-model-1"}
	if !slices.Equal(outcome.Reinstated, want) {
		t.Errorf("outcome.Reinstated = %v, want %v", outcome.Reinstated, want)
	}
}

// TestPiRestoreDropsThePrizmalScope: a non-empty enabledModels decides what a
// session starts with, ahead of defaultModel, so a prizmal/* row left there
// keeps the machine pointed at a provider the restore removed. Restore drops
// those rows and keeps every row of another provider.
func TestPiRestoreDropsThePrizmalScope(t *testing.T) {
	home := internaltest.SandboxedHome(t)
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)

	internaltest.SandboxedSeed(t, home, piModelsPath, `{"providers":{"myprov":{"baseUrl":"https://myprov.example/v1",`+
		`"api":"openai-completions","apiKey":"their-own-fake-key","models":[{"id":"user-model-1"}]}}}`)
	internaltest.SandboxedSeed(t, home, piSettingsPath, `{"defaultProvider":"myprov","defaultModel":"user-model-1",`+
		`"enabledModels":["prizmal/*","openrouter/zeta","prizmal/frontier"]}`)

	p := &Pi{}
	if err := p.Edit([]launch.LaunchModel{{Name: "probe-model"}}); err != nil {
		t.Fatalf("Pi.Edit: %v", err)
	}
	outcome, err := p.Restore()
	if err != nil {
		t.Fatalf("Pi.Restore: %v", err)
	}
	if !outcome.Removed {
		t.Error("outcome.Removed = false, want true: the scope named the removed provider")
	}

	settings := internaltest.SandboxedJSON(t, home, piSettingsPath)
	scoped, ok := settings["enabledModels"].([]any)
	if !ok {
		t.Fatalf("enabledModels = %v, want the operator's own row kept", settings["enabledModels"])
	}
	if want := []any{"openrouter/zeta"}; !slices.Equal(scoped, want) {
		t.Errorf("enabledModels = %v, want %v", scoped, want)
	}
	if got, _ := settings["defaultProvider"].(string); got != "myprov" {
		t.Errorf("defaultProvider = %q, want myprov put back", got)
	}
}

// TestPiRestoreDropsAnEmptyScope: with only prizmal rows in it, enabledModels
// goes altogether, because an empty list has no meaning of its own and Pi reads
// a missing one as no scope to honor.
func TestPiRestoreDropsAnEmptyScope(t *testing.T) {
	home := internaltest.SandboxedHome(t)
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)

	internaltest.SandboxedSeed(t, home, piSettingsPath, `{"theme":"dark","enabledModels":["prizmal/frontier"]}`)

	p := &Pi{}
	if err := p.Edit([]launch.LaunchModel{{Name: "probe-model"}}); err != nil {
		t.Fatalf("Pi.Edit: %v", err)
	}
	if _, err := p.Restore(); err != nil {
		t.Fatalf("Pi.Restore: %v", err)
	}

	settings := internaltest.SandboxedJSON(t, home, piSettingsPath)
	if _, ok := settings["enabledModels"]; ok {
		t.Errorf("enabledModels = %v, want it gone", settings["enabledModels"])
	}
	if got, _ := settings["theme"].(string); got != "dark" {
		t.Errorf("theme = %q, want dark left alone", got)
	}
}

// TestPiRestoreWithOnlyAScopeToDrop: a machine whose settings point at another
// provider but whose enabledModels still scopes prizmal rows gets a restore that
// reports a removal and leaves the default alone.
func TestPiRestoreWithOnlyAScopeToDrop(t *testing.T) {
	home := internaltest.SandboxedHome(t)
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)

	internaltest.SandboxedSeed(t, home, piSettingsPath, `{"defaultProvider":"myprov","defaultModel":"user-model-1",`+
		`"enabledModels":["prizmal/*"]}`)

	outcome, err := (&Pi{}).Restore()
	if err != nil {
		t.Fatalf("Pi.Restore: %v", err)
	}
	if !outcome.Removed {
		t.Error("outcome.Removed = false, want true: the scope was dropped")
	}
	settings := internaltest.SandboxedJSON(t, home, piSettingsPath)
	if got, _ := settings["defaultProvider"].(string); got != "myprov" {
		t.Errorf("defaultProvider = %q, want myprov left alone", got)
	}
	if len(outcome.Reinstated) != 0 {
		t.Errorf("outcome.Reinstated = %v, want nothing put back", outcome.Reinstated)
	}
}
