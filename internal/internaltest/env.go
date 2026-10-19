package internaltest

import (
	"strings"
	"testing"
)

// EnvValue returns the value of the var named after "prefix=" in env, or "".
func EnvValue(env []string, prefix string) string {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, prefix); ok {
			return v
		}
	}
	return ""
}

// SandboxedHome points HOME and USERPROFILE at a temp dir and returns it.
func SandboxedHome(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	t.Setenv("HOME", d)
	t.Setenv("USERPROFILE", d) // windows
	return d
}
