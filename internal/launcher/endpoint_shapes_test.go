package launch

import (
	"encoding/json"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
)

// TestAdapterEndpointShapes pins the endpoint every harness is actually
// pointed at, for the configured URLs that used to produce three different
// answers. Before envconfig owned the shapes, each adapter trimmed and
// appended its own way: a base carrying /v1 became /v1/v1 for pi, opencode,
// codex and cline while claude and the model catalog got it right, and a
// loopback address was spelled 127.0.0.1 for some adapters and localhost for
// the others. Nothing asserted a full URL — the credential-at-rest test only
// checked that pi's baseUrl was non-empty — so the wrong one reached the
// harness.
//
// The catalog request needs a live server and is covered by
// TestFetchSwitchCatalog; the rows here are the six values a harness reads.
func TestAdapterEndpointShapes(t *testing.T) {
	for _, tc := range []struct {
		name string
		base string
		// claude is what ANTHROPIC_BASE_URL carries: no version, because
		// Claude Code appends /v1 itself.
		claude string
		// codex is the one OpenAI-shaped client whose config format wants
		// the trailing slash.
		codex string
		// clineHost is the /v1-less spelling Cline's globalState.json keys
		// carry; clineBase is the provider entry, which carries the version.
		clineHost string
		clineBase string
		pi        string
		opencode  string
	}{
		{
			name:      "plain host",
			base:      "https://switch.example.com",
			claude:    "https://switch.example.com",
			codex:     "https://switch.example.com/v1/",
			clineHost: "https://switch.example.com",
			clineBase: "https://switch.example.com/v1",
			pi:        "https://switch.example.com/v1",
			opencode:  "https://switch.example.com/v1",
		},
		{
			name:      "a base carrying v1 sends exactly one version",
			base:      "https://switch.example.com/v1",
			claude:    "https://switch.example.com",
			codex:     "https://switch.example.com/v1/",
			clineHost: "https://switch.example.com",
			clineBase: "https://switch.example.com/v1",
			pi:        "https://switch.example.com/v1",
			opencode:  "https://switch.example.com/v1",
		},
		{
			name:      "v1 with the trailing slash a user types",
			base:      "https://switch.example.com/v1/",
			claude:    "https://switch.example.com",
			codex:     "https://switch.example.com/v1/",
			clineHost: "https://switch.example.com",
			clineBase: "https://switch.example.com/v1",
			pi:        "https://switch.example.com/v1",
			opencode:  "https://switch.example.com/v1",
		},
		{
			name:      "loopback is spelled the same way everywhere",
			base:      "http://127.0.0.1:8080",
			claude:    "http://localhost:8080",
			codex:     "http://localhost:8080/v1/",
			clineHost: "http://localhost:8080",
			clineBase: "http://localhost:8080/v1",
			pi:        "http://localhost:8080/v1",
			opencode:  "http://localhost:8080/v1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := cdkSandboxHome(t)
			envconfig.SetBaseURL(tc.base)
			t.Cleanup(func() { envconfig.SetBaseURL("") })

			if got := envValue((&Claude{}).envVars(), "ANTHROPIC_BASE_URL="); got != tc.claude {
				t.Errorf("ANTHROPIC_BASE_URL = %q, want %q", got, tc.claude)
			}
			if got := codexBaseURL(); got != tc.codex {
				t.Errorf("codex base URL = %q, want %q", got, tc.codex)
			}
			if got := clineProviderHost(); got != tc.clineHost {
				t.Errorf("cline globalState host = %q, want %q", got, tc.clineHost)
			}
			if got := clineProviderBaseURL(); got != tc.clineBase {
				t.Errorf("cline provider baseUrl = %q, want %q", got, tc.clineBase)
			}
			if got := opencodeInlineBaseURL(t, LaunchModel{Name: "probe-model"}); got != tc.opencode {
				t.Errorf("opencode baseURL = %q, want %q", got, tc.opencode)
			}
			if got := piProviderBaseURL(t, home); got != tc.pi {
				t.Errorf("pi baseUrl = %q, want %q", got, tc.pi)
			}
			// The websearch tool runs in the same process as the model, so it
			// has to be told the same endpoint the provider was.
			if got, _ := envLookup((&OpenCode{}).envVars("probe-model", nil), openCodeSearchURLEnv); got != tc.opencode {
				t.Errorf("%s = %q, want %q", openCodeSearchURLEnv, got, tc.opencode)
			}
		})
	}
}

// opencodeInlineBaseURL reads the base URL out of the inline config a launch
// hands opencode through OPENCODE_CONFIG_CONTENT, which is the only copy of it
// a session sees.
func opencodeInlineBaseURL(t *testing.T, model LaunchModel) string {
	t.Helper()
	content, err := buildInlineConfig(model, []LaunchModel{model})
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

// piProviderBaseURL writes the provider entry pi keeps in ~/.pi/agent and
// reads back the endpoint, which is a persisted edit: unlike the environment
// variables above it survives the launch and is the copy pi dials with.
func piProviderBaseURL(t *testing.T, home string) string {
	t.Helper()
	if err := (&Pi{}).Edit([]LaunchModel{{Name: "probe-model"}}); err != nil {
		t.Fatalf("Pi.Edit: %v", err)
	}
	cfg := sandboxedJSON(t, home, piModelsPath)
	providers, _ := cfg["providers"].(map[string]any)
	provider, _ := providers[piProviderID].(map[string]any)
	baseURL, _ := provider["baseUrl"].(string)
	return baseURL
}
