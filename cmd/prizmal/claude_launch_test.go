//go:build !windows

package main

import "testing"

// TestHarnessLaunchClaude launches the real Claude Code through prizmal
// against the stub switch. Claude Code needs no device credential here: the
// launch runs on the switch key in the config file.
func TestHarnessLaunchClaude(t *testing.T) { runHarnessLaunch(t, "claude", harnessLaunchConfigKey) }

// TestHarnessLaunchClaudeRelay launches the real Claude Code through the
// loopback relay against the stub switch.
func TestHarnessLaunchClaudeRelay(t *testing.T) {
	runHarnessLaunchThroughRelay(t, "claude", harnessLaunchConfigKey)
}

// TestHarnessLaunchClaudeWithoutAModel runs Claude Code's one-shot mode on a pipe
// with no model named: the menu cannot open, so the launch fails and lists the
// names the stub's switch key routes.
func TestHarnessLaunchClaudeWithoutAModel(t *testing.T) {
	runHarnessLaunchWithoutAModel(t, "claude")
}
