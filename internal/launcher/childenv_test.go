package launch

import (
	"slices"
	"strings"
	"testing"
)

func TestChildEnvDropsNamedAndFixedVariablesThenAppendsFixed(t *testing.T) {
	t.Setenv("CHILDENV_KEEP", "kept")
	t.Setenv("CHILDENV_DROP", "dropped")
	t.Setenv("CHILDENV_FIXED", "inherited")

	env := ChildEnv([]string{"CHILDENV_DROP"}, []string{"CHILDENV_FIXED=launch", "CHILDENV_NEW=new"})

	if !slices.Contains(env, "CHILDENV_KEEP=kept") {
		t.Error("an inherited variable the launch did not touch is missing")
	}
	counts := map[string]int{}
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		counts[name]++
	}
	if counts["CHILDENV_DROP"] != 0 {
		t.Errorf("a dropped variable survives: %v", env)
	}
	if counts["CHILDENV_FIXED"] != 1 || !slices.Contains(env, "CHILDENV_FIXED=launch") {
		t.Errorf("CHILDENV_FIXED must appear once with the launch's value: %v", env)
	}
	if !slices.Contains(env, "CHILDENV_NEW=new") {
		t.Error("a fixed variable is missing")
	}
	// A variable defined twice would leave the child's value to whichever copy
	// the OS reads first.
	for name, n := range counts {
		if n > 1 {
			t.Errorf("%s appears %d times", name, n)
		}
	}
}
