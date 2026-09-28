package launch

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
)

// The settings files the reinstatement tests inspect, relative to the
// sandboxed home. The credential-at-rest tests own the key-bearing paths.
var (
	piSettingsPath = filepath.Join(".pi", "agent", "settings.json")
	clineStatePath = filepath.Join(".cline", "data", "globalState.json")
)

// TestPiRestoreReinstatesPreviousDefaults: a user who had a provider and a
// model of their own before the first launch gets both back on --restore,
// and everything else in settings.json stays as it was. The second persist
// stands in for a later `prizmal --model other --persist pi`: the pre-launch
// copy has to survive it, and so does a second persist inside the same
// second, which is how the backups get named.
func TestPiRestoreReinstatesPreviousDefaults(t *testing.T) {
	home := cdkSandboxHome(t)
	internaltest.WithAPIKey(t, fakeKeyAtRest)

	sandboxedSeed(t, home, piModelsPath, `{"providers":{"myprov":{"baseUrl":"https://myprov.example/v1",`+
		`"api":"openai-completions","apiKey":"their-own-fake-key","models":[{"id":"user-model-1"}]}}}`)
	sandboxedSeed(t, home, piSettingsPath, `{"defaultProvider":"myprov","defaultModel":"user-model-1","theme":"dark"}`)

	p := &Pi{}
	for _, model := range []string{"probe-model-a", "probe-model-b"} {
		if err := p.Edit([]LaunchModel{{Name: model}}); err != nil {
			t.Fatalf("Pi.Edit(%s): %v", model, err)
		}
	}
	outcome, err := p.Restore()
	if err != nil {
		t.Fatalf("Pi.Restore: %v", err)
	}

	settings := sandboxedJSON(t, home, piSettingsPath)
	if got, _ := settings["defaultProvider"].(string); got != "myprov" {
		t.Errorf("defaultProvider = %q, want myprov put back", got)
	}
	if got, _ := settings["defaultModel"].(string); got != "user-model-1" {
		t.Errorf("defaultModel = %q, want user-model-1 put back", got)
	}
	if got, _ := settings["theme"].(string); got != "dark" {
		t.Errorf("theme = %q, want dark left alone", got)
	}

	providers, _ := sandboxedJSON(t, home, piModelsPath)["providers"].(map[string]any)
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

// TestClineRestoreReinstatesPreviousProviderAndModes is cline's half: the
// provider selection in providers.json and the act/plan mode pointers in
// globalState.json go back to what the user had, and the pointers Edit added
// for the Switch's endpoint are gone rather than left dangling.
func TestClineRestoreReinstatesPreviousProviderAndModes(t *testing.T) {
	home := cdkSandboxHome(t)
	internaltest.WithAPIKey(t, fakeKeyAtRest)

	sandboxedSeed(t, home, clineProvPath, `{"version":1,"lastUsedProvider":"anthropic","providers":{`+
		`"anthropic":{"settings":{"provider":"anthropic","model":"their-model","apiKey":"their-own-fake-key"},"tokenSource":"manual"}}}`)
	sandboxedSeed(t, home, clineStatePath, `{"actModeApiProvider":"anthropic","actModeApiModelId":"their-model",`+
		`"planModeApiProvider":"anthropic","planModeApiModelId":"their-model","welcomeViewCompleted":true}`)

	c := &Cline{}
	for _, model := range []string{"probe-model-a", "probe-model-b"} {
		if err := c.Edit([]LaunchModel{{Name: model}}); err != nil {
			t.Fatalf("Cline.Edit(%s): %v", model, err)
		}
	}
	outcome, err := c.Restore()
	if err != nil {
		t.Fatalf("Cline.Restore: %v", err)
	}

	prov := sandboxedJSON(t, home, clineProvPath)
	if got, _ := prov["lastUsedProvider"].(string); got != "anthropic" {
		t.Errorf("lastUsedProvider = %q, want anthropic put back", got)
	}
	providers, _ := prov["providers"].(map[string]any)
	if _, ok := providers[clineApiProvider]; ok {
		t.Error("openai-compatible entry survived restore")
	}
	if _, ok := providers["anthropic"]; !ok {
		t.Error("restore removed the user's own provider")
	}

	state := sandboxedJSON(t, home, clineStatePath)
	for _, mode := range []string{"actMode", "planMode"} {
		if got, _ := state[mode+"ApiProvider"].(string); got != "anthropic" {
			t.Errorf("%sApiProvider = %q, want anthropic put back", mode, got)
		}
		if got, _ := state[mode+"ApiModelId"].(string); got != "their-model" {
			t.Errorf("%sApiModelId = %q, want their-model left alone", mode, got)
		}
		for _, key := range []string{"OpenAiCompatibleModelId", "OpenAiCompatibleBaseUrl"} {
			if v, ok := state[mode+key]; ok {
				t.Errorf("%s%s = %v, want it gone: the user never had one", mode, key, v)
			}
		}
	}
	if v, ok := state["openAiCompatibleBaseUrl"]; ok {
		t.Errorf("openAiCompatibleBaseUrl = %v, want it gone", v)
	}
	if got, _ := state["welcomeViewCompleted"].(bool); !got {
		t.Error("welcomeViewCompleted lost: it is the user's own onboarding state")
	}

	if !outcome.Removed {
		t.Error("outcome.Removed = false, want true: a persisted launch was taken out")
	}
	want := []string{"lastUsedProvider=anthropic", "actModeApiProvider=anthropic", "planModeApiProvider=anthropic"}
	if !slices.Equal(outcome.Reinstated, want) {
		t.Errorf("outcome.Reinstated = %v, want %v", outcome.Reinstated, want)
	}
}

// TestRestoreOnNeverRunHomeIsANoOp: on a machine prizmal never configured,
// --restore has nothing to remove and must say so instead of reporting a
// removal. It also writes nothing, so an untouched home stays untouched.
// restoreOnly is the --restore half of a launcher; codex has it without
// being an Editor, so the credential-at-rest table cannot drive it.
type restoreOnly interface {
	Restore() (RestoreOutcome, error)
}

func TestRestoreOnNeverRunHomeIsANoOp(t *testing.T) {
	cases := []struct {
		name     string
		launcher restoreOnly
	}{
		{"pi", &Pi{}},
		{"cline", &Cline{}},
		{"codex", &Codex{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := cdkSandboxHome(t)
			internaltest.WithAPIKey(t, fakeKeyAtRest)

			outcome, err := tc.launcher.Restore()
			if err != nil {
				t.Fatalf("Restore on an empty home: %v", err)
			}
			if outcome.Removed {
				t.Error("outcome.Removed = true on a home prizmal never configured")
			}
			if len(outcome.Reinstated) != 0 {
				t.Errorf("outcome.Reinstated = %v, want nothing put back", outcome.Reinstated)
			}
			entries, err := os.ReadDir(home)
			if err != nil {
				t.Fatalf("read home: %v", err)
			}
			for _, entry := range entries {
				t.Errorf("restore created %s in a home it had nothing to restore in", entry.Name())
			}
		})
	}
}
