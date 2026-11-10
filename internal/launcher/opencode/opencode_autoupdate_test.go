package opencode

import (
	"os"
	"testing"
)

// TestOpenCodeLaunchDisablesAutoupdate checks the launch tells opencode not to
// offer or accept a release on another major version. A pinned 1.x otherwise
// shows an upgrade prompt for 2.x when no 1.x update exists, and 2.x calls
// neither the auth plugin nor the generated search tool.
func TestOpenCodeLaunchDisablesAutoupdate(t *testing.T) {
	sandboxedHome(t)
	env := (&OpenCode{}).childEnv("wire-test-model", wireKeyTestModel())
	if got, _ := envLookup(env, "OPENCODE_DISABLE_AUTOUPDATE"); got != "1" {
		t.Fatalf("OPENCODE_DISABLE_AUTOUPDATE = %q, want 1", got)
	}
}

// TestOpenCodeAutoupdateGuardSurvivesEarlyReturns covers envVars returning
// before it wires the search tool. The guard protects the session whether or
// not the tool could be written, so it cannot sit behind that branch.
func TestOpenCodeAutoupdateGuardSurvivesEarlyReturns(t *testing.T) {
	t.Run("search directory cannot be resolved", func(t *testing.T) {
		t.Setenv("HOME", "")
		t.Setenv("USERPROFILE", "") // windows
		env := (&OpenCode{}).envVars("wire-test-model", wireKeyTestModel())
		if _, ok := envLookup(env, "OPENCODE_CONFIG_DIR"); ok {
			t.Fatal("test premise broken: the search directory resolved")
		}
		if got, _ := envLookup(env, "OPENCODE_DISABLE_AUTOUPDATE"); got != "1" {
			t.Fatalf("OPENCODE_DISABLE_AUTOUPDATE = %q, want 1", got)
		}
	})
	t.Run("search directory cannot be written", func(t *testing.T) {
		home := sandboxedHome(t)
		// A regular file where the cache directory belongs makes MkdirAll fail.
		writeFileAt(t, home+"/.cache", "not a directory")
		env := (&OpenCode{}).envVars("wire-test-model", wireKeyTestModel())
		if _, ok := envLookup(env, "OPENCODE_CONFIG_DIR"); ok {
			t.Fatal("test premise broken: the search tool was written")
		}
		if got, _ := envLookup(env, "OPENCODE_DISABLE_AUTOUPDATE"); got != "1" {
			t.Fatalf("OPENCODE_DISABLE_AUTOUPDATE = %q, want 1", got)
		}
	})
}

func writeFileAt(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
