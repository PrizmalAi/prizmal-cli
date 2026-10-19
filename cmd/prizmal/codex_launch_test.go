//go:build !windows

package main

import "testing"

// TestHarnessLaunchCodex launches the real Codex CLI through prizmal against
// the stub switch, on the switch key in the config file.
func TestHarnessLaunchCodex(t *testing.T) { runHarnessLaunch(t, "codex", harnessLaunchConfigKey) }

// TestHarnessLaunchCodexWithoutAModel runs Codex's one-shot mode on a pipe
// with no model named: the menu cannot open, so the launch sends the config
// the stub's switch key is bound to.
func TestHarnessLaunchCodexWithoutAModel(t *testing.T) {
	runHarnessLaunchWithoutAModel(t, "codex")
}
