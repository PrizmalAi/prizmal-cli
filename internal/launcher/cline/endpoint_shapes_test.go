package cline

import (
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
)

// Cline carries the endpoint twice, in two spellings: the host in
// globalState.json and the versioned base in providers.json.
func TestClineEndpointShape(t *testing.T) {
	for _, tc := range []struct {
		name, base, wantHost, wantBase string
	}{
		{name: "plain host", base: "https://switch.example.com", wantHost: "https://switch.example.com", wantBase: "https://switch.example.com/v1"},
		{name: "a base carrying v1 sends exactly one version", base: "https://switch.example.com/v1", wantHost: "https://switch.example.com", wantBase: "https://switch.example.com/v1"},
		{name: "v1 with the trailing slash a user types", base: "https://switch.example.com/v1/", wantHost: "https://switch.example.com", wantBase: "https://switch.example.com/v1"},
		{name: "loopback is spelled the same way everywhere", base: "http://127.0.0.1:8080", wantHost: "http://localhost:8080", wantBase: "http://localhost:8080/v1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			envconfig.SetBaseURL(tc.base)
			t.Cleanup(func() { envconfig.SetBaseURL("") })

			if got := clineProviderHost(); got != tc.wantHost {
				t.Errorf("cline globalState host = %q, want %q", got, tc.wantHost)
			}
			if got := clineProviderBaseURL(); got != tc.wantBase {
				t.Errorf("cline provider baseUrl = %q, want %q", got, tc.wantBase)
			}
		})
	}
}
