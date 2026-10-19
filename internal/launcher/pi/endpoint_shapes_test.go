package pi

import (
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

// The switch base URL reaches pi as the provider entry in ~/.pi/agent, which
// is a persisted edit: unlike an environment variable it survives the launch
// and is the copy pi dials with.
func TestPiEndpointShape(t *testing.T) {
	for _, tc := range []struct {
		name, base, want string
	}{
		{name: "plain host", base: "https://switch.example.com", want: "https://switch.example.com/v1"},
		{name: "a base carrying v1 sends exactly one version", base: "https://switch.example.com/v1", want: "https://switch.example.com/v1"},
		{name: "v1 with the trailing slash a user types", base: "https://switch.example.com/v1/", want: "https://switch.example.com/v1"},
		{name: "loopback is spelled the same way everywhere", base: "http://127.0.0.1:8080", want: "http://localhost:8080/v1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := internaltest.SandboxedHome(t)
			envconfig.SetBaseURL(tc.base)
			t.Cleanup(func() { envconfig.SetBaseURL("") })

			if got := piProviderBaseURL(t, home); got != tc.want {
				t.Errorf("pi baseUrl = %q, want %q", got, tc.want)
			}
		})
	}
}

// piProviderBaseURL writes the provider entry pi keeps in ~/.pi/agent and
// reads back the endpoint.
func piProviderBaseURL(t *testing.T, home string) string {
	t.Helper()
	if err := (&Pi{}).Edit([]launch.LaunchModel{{Name: "probe-model"}}); err != nil {
		t.Fatalf("Pi.Edit: %v", err)
	}
	cfg := internaltest.SandboxedJSON(t, home, piModelsPath)
	providers, _ := cfg["providers"].(map[string]any)
	provider, _ := providers[piProviderID].(map[string]any)
	baseURL, _ := provider["baseUrl"].(string)
	return baseURL
}
