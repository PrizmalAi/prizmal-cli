package envconfig

import (
	"os"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/internaltest/homeguard"
)

// TestMain keeps the caller's Switch key and URL out of the tests, keeps them
// away from the real ~/.prizmal, and fails the package when they change it. It
// does not use internaltest.Main because internaltest imports this package.
func TestMain(m *testing.M) {
	for _, name := range []string{KeyEnvVar, EnvVar} {
		if err := os.Unsetenv(name); err != nil {
			panic(err)
		}
	}
	os.Exit(homeguard.Run(m))
}
