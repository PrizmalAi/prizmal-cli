//go:build !windows

package main

import "testing"

// TestHarnessLaunchCline launches the real Cline through prizmal against the
// stub switch, on the switch key in the config file.
func TestHarnessLaunchCline(t *testing.T) { runHarnessLaunch(t, "cline", harnessLaunchConfigKey) }

// TestHarnessLaunchClineWithoutAModel runs Cline's one-shot mode on a pipe
// with no model named: the menu cannot open, so the launch fails and lists the
// names the stub's switch key routes.
func TestHarnessLaunchClineWithoutAModel(t *testing.T) {
	runHarnessLaunchWithoutAModel(t, "cline")
}
