package internaltest

import (
	"os"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
)

// Main runs a package's tests with the caller's Switch credential and URL
// removed from the process environment, so a developer who exports
// PRIZMAL_SWITCH_KEY or PRIZMAL_SWITCH_URL gets the same result as CI. Call it
// from TestMain: os.Exit(internaltest.Main(m)).
//
// A test that needs a key or URL sets its own with t.Setenv or WithAPIKey.
func Main(m *testing.M) int {
	for _, name := range []string{envconfig.KeyEnvVar, envconfig.EnvVar} {
		if err := os.Unsetenv(name); err != nil {
			panic(err)
		}
	}
	return m.Run()
}
