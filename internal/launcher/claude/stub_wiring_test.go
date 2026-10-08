package claude

import (
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
	"github.com/PrizmalAi/prizmal-cli/internal/stubserver"
)

// These tests prove each harness's config builder carries the stub server's
// URL and key when pointed at it through envconfig. They do not launch the
// real harness binary (that is CI-only); they prove the wiring is correct
// against a real HTTP endpoint shape.

func TestStubServerClaudeWiring(t *testing.T) {
	srv := stubserver.New()
	defer srv.Close()
	envconfig.SetBaseURL(srv.URL)
	envconfig.SetAPIKey(stubserver.StubKey)
	defer func() { envconfig.SetBaseURL(""); envconfig.SetAPIKey("") }()

	c := &Claude{}
	env := c.envVars()
	val := internaltest.EnvValue(env, "ANTHROPIC_BASE_URL=")
	if val != srv.URL {
		t.Fatalf("ANTHROPIC_BASE_URL = %q, want %q", val, srv.URL)
	}
	keyVal := internaltest.EnvValue(env, "ANTHROPIC_AUTH_TOKEN=")
	if keyVal != stubserver.StubKey {
		t.Fatalf("ANTHROPIC_AUTH_TOKEN = %q, want %q", keyVal, stubserver.StubKey)
	}
}
