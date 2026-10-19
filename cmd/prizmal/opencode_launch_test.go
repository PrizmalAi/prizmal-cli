//go:build !windows

package main

import "testing"

// TestHarnessLaunchOpencode launches the real OpenCode through prizmal against
// the stub switch, on the switch key in the config file.
func TestHarnessLaunchOpencode(t *testing.T) { runHarnessLaunch(t, "opencode", harnessLaunchConfigKey) }

// TestHarnessLaunchOpencodeWithoutAModel runs OpenCode's one-shot mode on a pipe
// with no model named: the menu cannot open, so the launch fails and lists the
// names the stub's switch key routes.
func TestHarnessLaunchOpencodeWithoutAModel(t *testing.T) {
	runHarnessLaunchWithoutAModel(t, "opencode")
}
