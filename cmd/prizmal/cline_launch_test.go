//go:build !windows

package main

import "testing"

// TestHarnessLaunchCline launches the real Cline through prizmal against the
// stub switch, on the switch key in the config file.
func TestHarnessLaunchCline(t *testing.T) { runHarnessLaunch(t, "cline", harnessLaunchConfigKey) }
