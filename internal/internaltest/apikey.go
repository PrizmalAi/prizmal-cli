// Package internaltest holds test-only helpers shared by packages whose
// tests exercise the same credential-handling invariants from different
// packages. It is a leaf: it imports testing and envconfig and nothing else,
// so any internal package's tests can use it without creating a cycle.
package internaltest

import (
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
)

// WithAPIKey points envconfig at key for the duration of a test: the
// environment variable is cleared so the override is the only key source,
// and the override is emptied again when the test ends.
func WithAPIKey(t *testing.T, key string) {
	t.Helper()
	t.Setenv(envconfig.KeyEnvVar, "")
	envconfig.SetAPIKey(key)
	t.Cleanup(func() { envconfig.SetAPIKey("") })
}
