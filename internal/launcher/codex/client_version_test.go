package codex

import (
	"slices"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
)

// Codex takes the header from a -c override on the provider the launch already
// declares, so nothing is written under ~/.codex.
func TestCodexSendsTheClientVersion(t *testing.T) {
	envconfig.SetClientVersion("1.4.2")
	t.Cleanup(func() { envconfig.SetClientVersion("") })

	overrides, err := codexManagedConfigOverrides("")
	if err != nil {
		t.Fatalf("codexManagedConfigOverrides: %v", err)
	}
	want := `model_providers.prizmal.http_headers={"x-prizmal-client-version"="1.4.2"}`
	if !slices.Contains(overrides, want) {
		t.Errorf("overrides lack %s:\n%q", want, overrides)
	}
}
