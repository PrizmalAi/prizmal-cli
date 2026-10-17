//go:build !windows

package main

import "testing"

// TestHarnessLaunchCodex launches the real Codex CLI through prizmal against
// the stub switch, on the switch key in the config file.
func TestHarnessLaunchCodex(t *testing.T) { runHarnessLaunch(t, "codex", harnessLaunchConfigKey) }
