package claude

import (
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
)

func withClientVersion(t *testing.T, v string) {
	t.Helper()
	envconfig.SetClientVersion(v)
	t.Cleanup(func() { envconfig.SetClientVersion("") })
}

// Claude Code adds each line of ANTHROPIC_CUSTOM_HEADERS to every model request.
func TestClaudeSendsTheClientVersion(t *testing.T) {
	withClientVersion(t, "1.4.2")
	t.Setenv("ANTHROPIC_CUSTOM_HEADERS", "")

	got := internaltest.EnvValue((&Claude{}).envVars(), "ANTHROPIC_CUSTOM_HEADERS=")
	if want := "x-prizmal-client-version: 1.4.2"; got != want {
		t.Errorf("ANTHROPIC_CUSTOM_HEADERS = %q, want %q", got, want)
	}
}

// An operator's own custom headers stay, and a client-version line of their own
// is replaced rather than sent twice.
func TestClaudeKeepsTheOperatorsCustomHeaders(t *testing.T) {
	withClientVersion(t, "1.4.2")
	t.Setenv("ANTHROPIC_CUSTOM_HEADERS", "X-Team: blue\nX-Prizmal-Client-Version: spoofed\nX-Run: 7")

	got := internaltest.EnvValue((&Claude{}).envVars(), "ANTHROPIC_CUSTOM_HEADERS=")
	if want := "X-Team: blue\nX-Run: 7\nx-prizmal-client-version: 1.4.2"; got != want {
		t.Errorf("ANTHROPIC_CUSTOM_HEADERS = %q, want %q", got, want)
	}
}

// A build with no version still sends a value.
func TestClaudeSendsDevUnknownWithoutAVersion(t *testing.T) {
	withClientVersion(t, "")
	t.Setenv("ANTHROPIC_CUSTOM_HEADERS", "")

	got := internaltest.EnvValue((&Claude{}).envVars(), "ANTHROPIC_CUSTOM_HEADERS=")
	if want := "x-prizmal-client-version: dev-unknown"; got != want {
		t.Errorf("ANTHROPIC_CUSTOM_HEADERS = %q, want %q", got, want)
	}
}
