package codex

import (
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
)

// Codex names OPENAI_API_KEY with env_key in the provider overrides, and
// prizmal sets it on the child.
func TestCodexEnvVarsCarryAPIKey(t *testing.T) {
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)

	if got := internaltest.EnvValue((&Codex{}).envVars(), "OPENAI_API_KEY="); got != internaltest.FakeKeyAtRest {
		t.Fatalf("OPENAI_API_KEY = %q, want the configured key", got)
	}
}
