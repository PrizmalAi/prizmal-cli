package launch

import (
	"os"
	"strings"
	"testing"
)

// A launch states the context window to Claude Code in the child's
// environment. Claude Code reads CLAUDE_CODE_AUTO_COMPACT_WINDOW ahead of every
// other source, and a window read there turns the resolver's source from
// "auto" into "env". The ids a launch spells are not in Claude Code's model
// catalogue, resolve with the source "auto", and its threshold check returns
// early for that source, so without the variable a session never compacts on
// its own. The end-to-end test against the pinned Claude Code proves the
// effect; these tests pin the value the child gets.

// childCompactWindow returns the value of the variable in the child
// environment, and how many times it appears.
func childCompactWindow(t *testing.T) (string, int) {
	t.Helper()
	var value string
	count := 0
	for _, kv := range claudeChildEnv("smart", nil) {
		if name, v, _ := strings.Cut(kv, "="); name == claudeCompactWindowEnv {
			value = v
			count++
		}
	}
	return value, count
}

func TestClaudeChildEnvStatesTheCompactWindow(t *testing.T) {
	resetAPIKey(t)
	// t.Setenv restores the shell's value afterward; the unset is the case.
	t.Setenv(claudeCompactWindowEnv, "")
	if err := os.Unsetenv(claudeCompactWindowEnv); err != nil {
		t.Fatal(err)
	}

	value, count := childCompactWindow(t)

	if count != 1 || value != "1000000" {
		t.Errorf("child env has %s=%q %d time(s), want exactly one 1000000", claudeCompactWindowEnv, value, count)
	}
}

// An operator's export changes the window only toward a smaller one, and the
// child never carries the variable twice.
func TestClaudeChildEnvKeepsOnlyASmallerOperatorWindow(t *testing.T) {
	resetAPIKey(t)
	cases := []struct {
		name      string
		inherited string
		want      string
	}{
		{"empty", "", "1000000"},
		{"smaller is kept", "500000", "500000"},
		{"padded smaller is kept", " 500000 ", "500000"},
		{"just under the launch's", "999999", "999999"},
		{"equal", "1000000", "1000000"},
		{"larger is capped", "2000000", "1000000"},
		{"not a number", "lots", "1000000"},
		{"fractional", "5e5", "1000000"},
		{"zero", "0", "1000000"},
		{"negative", "-5", "1000000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(claudeCompactWindowEnv, tc.inherited)

			value, count := childCompactWindow(t)

			if count != 1 || value != tc.want {
				t.Errorf("inherited %q: child env has %s=%q %d time(s), want exactly one %q", tc.inherited, claudeCompactWindowEnv, value, count, tc.want)
			}
		})
	}
}

// The window rides the environment and nothing else: the inline settings stay
// free of it, so a /model switch cannot find a second, conflicting value.
func TestClaudeSettingsJSONStatesNoCompactWindow(t *testing.T) {
	settings, err := claudeSettingsJSON("smart", nil)
	if err != nil {
		t.Fatalf("claudeSettingsJSON: %v", err)
	}
	for _, key := range []string{"autoCompactWindow", "modelSettings"} {
		if strings.Contains(settings, key) {
			t.Errorf("settings = %s, want no %s: the window is stated in the environment", settings, key)
		}
	}
}
