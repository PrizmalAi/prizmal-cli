package launch

import (
	"encoding/json"
	"testing"
)

// piModelSetting pulls one numeric setting off a pi model config as an int.
func piModelSetting(t *testing.T, cfg map[string]any, key string) int {
	t.Helper()
	switch v := cfg[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		t.Fatalf("pi config has no integer %q: %v", key, cfg[key])
		return 0
	}
}

// Every model the Switch serves has a 1M window, and GET /v1/models carries no
// context length, so the launch states the window rather than learning it. Pi
// budgets and compacts from the declared window, so declaring pi's own 128000
// default makes a session compact at a fraction of the window the Switch
// accepts.
func TestPiModelConfigDeclaresTheMillionTokenWindow(t *testing.T) {
	cfg := createConfig(LaunchModel{Name: "smart"})

	if got := piModelSetting(t, cfg, "contextWindow"); got != 1_000_000 {
		t.Fatalf("contextWindow = %d, want 1000000: every model the Switch serves has a 1M window", got)
	}
}

// $HARNESS_CONTEXT_LENGTH is the operator's way to state a real window, so it
// must win over the fallback, exactly as it does for Codex.
func TestPiModelConfigHonoursTheOperatorStatedWindow(t *testing.T) {
	t.Setenv("HARNESS_CONTEXT_LENGTH", "200000")

	cfg := createConfig(LaunchModel{Name: "smart"})

	if got := piModelSetting(t, cfg, "contextWindow"); got != 200000 {
		t.Fatalf("contextWindow = %d, want the operator's 200000", got)
	}
}

// pi's own default output budget is 16384, which cuts a long answer short. The
// launch asks for the most any route allows, the whole window, and leaves the
// Switch to cap the request to the limit of the route that serves it.
func TestPiModelConfigDeclaresTheLargestOutputBudget(t *testing.T) {
	cfg := createConfig(LaunchModel{Name: "smart"})

	if got := piModelSetting(t, cfg, "maxTokens"); got != 1_000_000 {
		t.Fatalf("maxTokens = %d, want 1000000: ask for the most a route allows and let the Switch cap it", got)
	}
}

// The device-launch extension reads its window and output budget from the
// sidecar config, so a device launch and a switch-key launch of the same model
// size it the same way. This pins the sidecar's two numbers.
func TestPiDeviceConfigCarriesWindowAndOutputBudget(t *testing.T) {
	data := piDeviceConfigJSON("smart", []LaunchModel{{Name: "smart"}})
	var config struct {
		Models []struct {
			ID            string `json:"id"`
			ContextWindow int    `json:"contextWindow"`
			MaxTokens     int    `json:"maxTokens"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("unmarshal the sidecar config: %v", err)
	}
	if len(config.Models) != 1 {
		t.Fatalf("models = %+v, want one", config.Models)
	}
	if got := config.Models[0].ContextWindow; got != 1_000_000 {
		t.Errorf("contextWindow = %d, want 1000000", got)
	}
	if got := config.Models[0].MaxTokens; got != 1_000_000 {
		t.Errorf("maxTokens = %d, want 1000000", got)
	}
}
