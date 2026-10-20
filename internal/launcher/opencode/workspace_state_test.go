package opencode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

// Two launches from two projects hand opencode two state homes, and the
// picker state each Edit seeds is the file its own launch reads.
func TestOpenCodeStateHomeIsPerWorkspace(t *testing.T) {
	sandboxedHome(t)
	homes := map[string]bool{}
	for _, model := range []string{"smart", "flash"} {
		t.Chdir(t.TempDir())
		o := &OpenCode{}
		if err := o.Edit([]launch.LaunchModel{{Name: model}}); err != nil {
			t.Fatal(err)
		}
		state, ok := envLookup(o.envVars(model, nil), "XDG_STATE_HOME")
		if !ok {
			t.Fatal("the launch sets no XDG_STATE_HOME")
		}
		homes[state] = true
		data, err := os.ReadFile(filepath.Join(state, "opencode", "model.json"))
		if err != nil {
			t.Fatalf("the seeded picker state is not where opencode reads it: %v", err)
		}
		if !strings.Contains(string(data), `"modelID": "`+model+`"`) {
			t.Errorf("model.json under %s does not list %s:\n%s", state, model, data)
		}
	}
	if len(homes) != 2 {
		t.Errorf("two projects share one state home: %v", homes)
	}
}

// The operator's own XDG_STATE_HOME rides in the carrier the shell-env plugin
// reads, and an inherited carrier never survives into the child.
func TestOpenCodeCarriesTheOperatorsStateHome(t *testing.T) {
	sandboxedHome(t)
	t.Chdir(t.TempDir())
	t.Setenv(openCodeUserStateEnv, "/stale")

	t.Setenv("XDG_STATE_HOME", "/operator/state")
	env := (&OpenCode{}).childEnv("smart", nil)
	if got, _ := envLookup(env, openCodeUserStateEnv); got != "/operator/state" {
		t.Errorf("%s = %q, want the operator's /operator/state", openCodeUserStateEnv, got)
	}
	if got, _ := envLookup(env, "XDG_STATE_HOME"); got == "/operator/state" {
		t.Error("opencode runs on the operator's state home, so workspaces share model.json")
	}

	unsetEnv(t, "XDG_STATE_HOME")
	env = (&OpenCode{}).childEnv("smart", nil)
	if got, ok := envLookup(env, openCodeUserStateEnv); ok {
		t.Errorf("%s = %q with XDG_STATE_HOME unset, want it absent so the shell sees none", openCodeUserStateEnv, got)
	}
}

// The plugin that restores the operator's state home sits in the config
// directory opencode globs for plugins, and reads the carrier.
func TestOpenCodeWritesTheShellEnvPlugin(t *testing.T) {
	sandboxedHome(t)
	dir, ok := envLookup((&OpenCode{}).envVars("smart", nil), "OPENCODE_CONFIG_DIR")
	if !ok {
		t.Fatal("the launch sets no OPENCODE_CONFIG_DIR")
	}
	src, err := os.ReadFile(filepath.Join(dir, "plugin", openCodeShellEnvPluginID+".js"))
	if err != nil {
		t.Fatalf("shell-env plugin missing: %v", err)
	}
	for _, want := range []string{`"shell.env"`, "process.env." + openCodeUserStateEnv} {
		if !strings.Contains(string(src), want) {
			t.Errorf("plugin lacks %s:\n%s", want, src)
		}
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
