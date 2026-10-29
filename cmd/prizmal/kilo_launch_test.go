package main

import "testing"

// TestHarnessLaunchKilo launches the real Kilo Code CLI through prizmal against
// the stub switch, on the switch key in the config file.
func TestHarnessLaunchKilo(t *testing.T) { runHarnessLaunch(t, "kilo", harnessLaunchConfigKey) }

// TestHarnessLaunchKiloWithoutAModel runs Kilo's one-shot mode on a pipe with
// no model named: the menu cannot open, so the launch fails and lists the names
// the stub's switch key routes.
func TestHarnessLaunchKiloWithoutAModel(t *testing.T) {
	runHarnessLaunchWithoutAModel(t, "kilo")
}

// TestHarnessLaunchKiloFromDeviceLogin pins that a device-login launch of Kilo
// starts and authenticates. The stub accepts only the device token its own
// refresh handed out and the inline config holds a placeholder, so the reply on
// stdout proves the relay swapped in a fresh device token.
func TestHarnessLaunchKiloFromDeviceLogin(t *testing.T) {
	runHarnessLaunch(t, "kilo", harnessLaunchDeviceLogin)
}
