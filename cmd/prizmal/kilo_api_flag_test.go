package main

import (
	"strings"
	"testing"
)

// TestKiloAPIFlagIsRefusedOutsideKilo pins that --kilo-api answers with an error
// before anything launches: on another integration, and with no integration
// at all. Silently ignoring it would let an operator believe a Claude Code or
// Codex session ran over the API they named.
func TestKiloAPIFlagIsRefusedOutsideKilo(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess test in short mode")
	}
	prizmalBin := buildPrizmalTestBinary(t)
	for name, args := range map[string][]string{
		"another integration": {"--kilo-api", "responses", "claude"},
		"no integration":      {"--kilo-api", "responses", "--list"},
	} {
		t.Run(name, func(t *testing.T) {
			exitCode, _, stderr := runPrizmalList(t, prizmalBin, t.TempDir(), args...)
			if exitCode == 0 || !strings.Contains(stderr, "--kilo-api only applies to the kilo integration") {
				t.Fatalf("prizmal %s exited %d, stderr %q", strings.Join(args, " "), exitCode, stderr)
			}
		})
	}
}

// TestKiloAPIFlagRejectsAnUnknownMode pins that a mode other than the three
// names stops the launch and names the three.
func TestKiloAPIFlagRejectsAnUnknownMode(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess test in short mode")
	}
	prizmalBin := buildPrizmalTestBinary(t)
	exitCode, _, stderr := runPrizmalList(t, prizmalBin, t.TempDir(), "--kilo-api", "bogus", "kilo")
	if exitCode == 0 || !strings.Contains(stderr, "chat, responses, anthropic") {
		t.Fatalf("prizmal --kilo-api bogus kilo exited %d, stderr %q", exitCode, stderr)
	}
}
