package codex

import (
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
)

// The switch base URL reaches Codex as the profile's base_url, which is the
// OpenAI-compatible spelling with its version and trailing slash.
func TestCodexEndpointShape(t *testing.T) {
	for _, tc := range []struct {
		name, base, want string
	}{
		{name: "plain host", base: "https://switch.example.com", want: "https://switch.example.com/v1/"},
		{name: "a base carrying v1 sends exactly one version", base: "https://switch.example.com/v1", want: "https://switch.example.com/v1/"},
		{name: "v1 with the trailing slash a user types", base: "https://switch.example.com/v1/", want: "https://switch.example.com/v1/"},
		{name: "loopback is spelled the same way everywhere", base: "http://127.0.0.1:8080", want: "http://localhost:8080/v1/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			internaltest.SandboxedHome(t)
			envconfig.SetBaseURL(tc.base)
			t.Cleanup(func() { envconfig.SetBaseURL("") })

			if got := codexBaseURL(); got != tc.want {
				t.Errorf("codex base URL = %q, want %q", got, tc.want)
			}
		})
	}
}
