package kilo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func lookup(env []string, name string) (string, bool) {
	for _, entry := range env {
		if after, ok := strings.CutPrefix(entry, name+"="); ok {
			return after, true
		}
	}
	return "", false
}

// TestKiloLaunchDisablesAutoupdate checks the launch sets Kilo's own variable,
// not OpenCode's: Kilo reads KILO_DISABLE_AUTOUPDATE and ignores
// OPENCODE_DISABLE_AUTOUPDATE. Without it Kilo announces any minor or major
// release, and installs patch releases without asking.
func TestKiloLaunchDisablesAutoupdate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	env := (&Kilo{}).envVars("smart", "{}")
	if got, _ := lookup(env, "KILO_DISABLE_AUTOUPDATE"); got != "1" {
		t.Fatalf("KILO_DISABLE_AUTOUPDATE = %q, want 1", got)
	}
	if _, ok := lookup(env, "OPENCODE_DISABLE_AUTOUPDATE"); ok {
		t.Fatal("launch sets OPENCODE_DISABLE_AUTOUPDATE, which Kilo does not read")
	}
}

// TestKiloAutoupdateGuardSurvivesEarlyReturns covers envVars returning before
// it wires the search tool: the guard cannot sit behind that branch.
func TestKiloAutoupdateGuardSurvivesEarlyReturns(t *testing.T) {
	t.Run("search directory cannot be resolved", func(t *testing.T) {
		t.Setenv("HOME", "")
		t.Setenv("USERPROFILE", "") // windows
		env := (&Kilo{}).envVars("smart", "{}")
		if _, ok := lookup(env, "KILO_CONFIG_DIR"); ok {
			t.Fatal("test premise broken: the search directory resolved")
		}
		if got, _ := lookup(env, "KILO_DISABLE_AUTOUPDATE"); got != "1" {
			t.Fatalf("KILO_DISABLE_AUTOUPDATE = %q, want 1", got)
		}
	})
	t.Run("search directory cannot be written", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		// A regular file where the cache directory belongs makes MkdirAll fail.
		if err := os.WriteFile(filepath.Join(home, ".cache"), []byte("not a directory"), 0o600); err != nil {
			t.Fatal(err)
		}
		env := (&Kilo{}).envVars("smart", "{}")
		if _, ok := lookup(env, "KILO_CONFIG_DIR"); ok {
			t.Fatal("test premise broken: the search tool was written")
		}
		if got, _ := lookup(env, "KILO_DISABLE_AUTOUPDATE"); got != "1" {
			t.Fatalf("KILO_DISABLE_AUTOUPDATE = %q, want 1", got)
		}
	})
}
