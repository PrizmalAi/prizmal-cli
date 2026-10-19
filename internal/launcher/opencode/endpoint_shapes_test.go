package opencode

import (
	"encoding/json"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

// The switch base URL reaches opencode twice: the provider's baseURL in the
// inline config, and the search tool's endpoint on the child environment,
// because the tool runs in the same process as the model.
func TestOpenCodeEndpointShape(t *testing.T) {
	for _, tc := range []struct {
		name, base, want string
	}{
		{name: "plain host", base: "https://switch.example.com", want: "https://switch.example.com/v1"},
		{name: "a base carrying v1 sends exactly one version", base: "https://switch.example.com/v1", want: "https://switch.example.com/v1"},
		{name: "v1 with the trailing slash a user types", base: "https://switch.example.com/v1/", want: "https://switch.example.com/v1"},
		{name: "loopback is spelled the same way everywhere", base: "http://127.0.0.1:8080", want: "http://localhost:8080/v1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			envconfig.SetBaseURL(tc.base)
			t.Cleanup(func() { envconfig.SetBaseURL("") })

			if got := opencodeInlineBaseURL(t, launch.LaunchModel{Name: "probe-model"}); got != tc.want {
				t.Errorf("opencode baseURL = %q, want %q", got, tc.want)
			}
			if got, _ := envLookup((&OpenCode{}).envVars("probe-model", nil), openCodeSearchURLEnv); got != tc.want {
				t.Errorf("%s = %q, want %q", openCodeSearchURLEnv, got, tc.want)
			}
		})
	}
}

// opencodeInlineBaseURL reads the base URL out of the inline config a launch
// hands opencode through OPENCODE_CONFIG_CONTENT, which is the only copy of it
// a session sees.
func opencodeInlineBaseURL(t *testing.T, model launch.LaunchModel) string {
	t.Helper()
	content, err := buildInlineConfig(model, []launch.LaunchModel{model})
	if err != nil {
		t.Fatalf("buildInlineConfig: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(content), &cfg); err != nil {
		t.Fatalf("unmarshal inline config: %v", err)
	}
	provider, _ := cfg["provider"].(map[string]any)["prizmal"].(map[string]any)
	options, _ := provider["options"].(map[string]any)
	baseURL, _ := options["baseURL"].(string)
	return baseURL
}
