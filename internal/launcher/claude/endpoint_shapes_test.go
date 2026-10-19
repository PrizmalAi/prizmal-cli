package claude

import (
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/launchtest"
)

// The switch base URL reaches Claude Code as an environment variable whose
// spelling envconfig owns, whatever form the operator configured the base in.
func TestClaudeEndpointShape(t *testing.T) {
	for _, tc := range []struct {
		name, base, want string
	}{
		{name: "plain host", base: "https://switch.example.com", want: "https://switch.example.com"},
		{name: "a base carrying v1 sends exactly one version", base: "https://switch.example.com/v1", want: "https://switch.example.com"},
		{name: "v1 with the trailing slash a user types", base: "https://switch.example.com/v1/", want: "https://switch.example.com"},
		{name: "loopback is spelled the same way everywhere", base: "http://127.0.0.1:8080", want: "http://localhost:8080"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			envconfig.SetBaseURL(tc.base)
			t.Cleanup(func() { envconfig.SetBaseURL("") })

			if got := launchtest.EnvValue((&Claude{}).envVars(), "ANTHROPIC_BASE_URL="); got != tc.want {
				t.Errorf("ANTHROPIC_BASE_URL = %q, want %q", got, tc.want)
			}
		})
	}
}
