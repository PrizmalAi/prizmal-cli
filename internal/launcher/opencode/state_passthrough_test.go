package opencode

import (
	"os"
	"path/filepath"
	"testing"
)

// The launch leaves OpenCode's state directory to OpenCode: the operator's
// XDG_STATE_HOME reaches the child unchanged, and a child of an operator who
// had none has none.
func TestOpenCodeChildEnvPassesStateHomeThrough(t *testing.T) {
	sandboxedHome(t)

	t.Setenv("XDG_STATE_HOME", "/operator/state")
	env := (&OpenCode{}).childEnv("smart", nil)
	if got, ok := envLookup(env, "XDG_STATE_HOME"); !ok || got != "/operator/state" {
		t.Errorf("XDG_STATE_HOME = %q (set %v), want the operator's /operator/state", got, ok)
	}

	unsetEnv(t, "XDG_STATE_HOME")
	env = (&OpenCode{}).childEnv("smart", nil)
	if got, ok := envLookup(env, "XDG_STATE_HOME"); ok {
		t.Errorf("XDG_STATE_HOME = %q, want it absent when the operator had none", got)
	}
}

// The config directory holds the search tool and nothing that runs in the
// agent's shell.
func TestOpenCodeConfigDirHoldsNoPlugin(t *testing.T) {
	sandboxedHome(t)
	dir, ok := envLookup((&OpenCode{}).envVars("smart", nil), "OPENCODE_CONFIG_DIR")
	if !ok {
		t.Fatal("the launch sets no OPENCODE_CONFIG_DIR")
	}
	if _, err := os.Stat(filepath.Join(dir, "plugin")); !os.IsNotExist(err) {
		t.Errorf("a plugin directory exists under %s (stat error: %v)", dir, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "tool", openCodeSearchToolID+".ts")); err != nil {
		t.Errorf("the search tool is missing: %v", err)
	}
}

// Prizmal writes no OpenCode state.
func TestOpenCodeEditWritesNoState(t *testing.T) {
	home := sandboxedHome(t)
	if err := (&OpenCode{}).Edit(wireKeyTestModel()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".prizmal")); !os.IsNotExist(err) {
		t.Errorf("Edit wrote under ~/.prizmal (stat error: %v)", err)
	}
	if paths := (&OpenCode{}).Paths(); len(paths) != 0 {
		t.Errorf("Paths() = %v, want none", paths)
	}
}

// unsetEnv removes name from the environment for the rest of the test, and
// t.Setenv restores the earlier value when the test ends.
func unsetEnv(t *testing.T, name string) {
	t.Helper()
	t.Setenv(name, "")
	if err := os.Unsetenv(name); err != nil {
		t.Fatalf("unset %s: %v", name, err)
	}
}
