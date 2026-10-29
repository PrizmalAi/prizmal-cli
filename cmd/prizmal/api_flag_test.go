package main

import (
	"strings"
	"testing"
)

// TestAPIFlagIsRefusedBeforeALaunch pins the errors --api gives at the command
// line, before any harness starts: a harness that does not speak the API, an
// unknown API, and no integration to apply it to. The per-harness table lives
// in the registry package.
func TestAPIFlagIsRefusedBeforeALaunch(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess test in short mode")
	}
	prizmalBin := buildPrizmalTestBinary(t)
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"a harness that does not speak it": {[]string{"--api", "responses", "claude"}, "claude does not support --api responses: it supports anthropic"},
		"an unknown API":                   {[]string{"--api", "bogus", "kilo"}, "chat, responses, anthropic"},
		"no integration":                   {[]string{"--api", "chat"}, "--api needs an integration to launch"},
	} {
		t.Run(name, func(t *testing.T) {
			exitCode, _, stderr := runPrizmalList(t, prizmalBin, t.TempDir(), tc.args...)
			if exitCode == 0 || !strings.Contains(stderr, tc.want) {
				t.Fatalf("prizmal %s exited %d, stderr %q, want %q", strings.Join(tc.args, " "), exitCode, stderr, tc.want)
			}
		})
	}
}
