//go:build !windows

package main

import "testing"

// TestHarnessLaunchCodex launches the real Codex CLI through prizmal against
// the stub switch, on the switch key in the config file.
func TestHarnessLaunchCodex(t *testing.T) { runHarnessLaunch(t, "codex", harnessLaunchConfigKey) }

// TestHarnessLaunchCodexIgnoresDeviceLogin pins that Codex has no refresh
// contract: on a machine signed in with a device key it must run on the switch
// key the config already holds, with device login ignored for the launch
// entirely. The enrolled key cannot refresh against the stub, so the stub's
// reply on stdout proves the launch never tried device login, and the announce
// line names the config key as the one that ran.
//
// Pi is not in this set: it refreshes through its extension, so its
// device-mode launch is TestHarnessLaunchPiFromDeviceLogin.
func TestHarnessLaunchCodexIgnoresDeviceLogin(t *testing.T) {
	runHarnessLaunch(t, "codex", harnessLaunchIgnoredDevice)
}
