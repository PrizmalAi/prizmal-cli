package cline

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

var clineStatePath = filepath.Join(".cline", "data", "globalState.json")

// TestClineRestoreReinstatesPreviousProviderAndModes is cline's half: the
// provider selection in providers.json and the act/plan mode pointers in
// globalState.json go back to what the user had, and the pointers Edit added
// for the Switch's endpoint are gone rather than left dangling.
func TestClineRestoreReinstatesPreviousProviderAndModes(t *testing.T) {
	home := internaltest.SandboxedHome(t)
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)

	internaltest.SandboxedSeed(t, home, clineProvPath, `{"version":1,"lastUsedProvider":"anthropic","providers":{`+
		`"anthropic":{"settings":{"provider":"anthropic","model":"their-model","apiKey":"their-own-fake-key"},"tokenSource":"manual"}}}`)
	internaltest.SandboxedSeed(t, home, clineStatePath, `{"actModeApiProvider":"anthropic","actModeApiModelId":"their-model",`+
		`"planModeApiProvider":"anthropic","planModeApiModelId":"their-model","welcomeViewCompleted":true}`)

	c := &Cline{}
	for _, model := range []string{"probe-model-a", "probe-model-b"} {
		if err := c.Edit([]launch.LaunchModel{{Name: model}}); err != nil {
			t.Fatalf("Cline.Edit(%s): %v", model, err)
		}
	}
	outcome, err := c.Restore()
	if err != nil {
		t.Fatalf("Cline.Restore: %v", err)
	}

	prov := internaltest.SandboxedJSON(t, home, clineProvPath)
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

	state := internaltest.SandboxedJSON(t, home, clineStatePath)
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
