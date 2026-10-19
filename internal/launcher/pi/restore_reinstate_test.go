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
