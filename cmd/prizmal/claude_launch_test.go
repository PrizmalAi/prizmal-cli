//go:build !windows

package main

import "testing"

// TestHarnessLaunchClaude launches the real Claude Code through prizmal
// against the stub switch. Claude Code needs no device credential here: the
// launch runs on the switch key in the config file.
func TestHarnessLaunchClaude(t *testing.T) { runHarnessLaunch(t, "claude", harnessLaunchConfigKey) }
