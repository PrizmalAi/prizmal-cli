//go:build !windows

package main

import "testing"

// TestHarnessLaunchOpencode launches the real OpenCode through prizmal against
// the stub switch, on the switch key in the config file.
func TestHarnessLaunchOpencode(t *testing.T) { runHarnessLaunch(t, "opencode", harnessLaunchConfigKey) }
