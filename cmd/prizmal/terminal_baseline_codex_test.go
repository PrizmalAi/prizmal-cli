//go:build !windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// The Codex screens live under testdata/terminal/codex. The codex workflow
// installs the Codex version recorded in codex-version.

// codexBaselineDir holds the screens of the real Codex, which the codex
// workflow records with the version in codex-version beside them.
const codexBaselineDir = "testdata/terminal/codex"

var (
	stepCodexPrompt      = baselineStep{waitFor: "default · ~/project"}
	stepCodexOpenModel   = baselineStep{literal: "/model", waitFor: "/model"}
	stepCodexModelPicker = baselineStep{key: "Enter", waitFor: "Select Model"}
	stepCodexSendHi      = baselineStep{literal: "hi", waitFor: "hi"}
	stepCodexSubmit      = baselineStep{key: "Enter", waitFor: "PRIZMAL-STUB-REPLY"}
	stepCodexTypeStatus  = baselineStep{literal: "/status", waitFor: "/status"}
	stepCodexStatus      = baselineStep{key: "Enter", waitFor: "Session"}
)

// codexBaselineCases are the screens of the real Codex. Each launches it
// through prizmal with a model passed as -m, so no picker shows first.
var codexBaselineCases = []baselineCase{
	{
		name: "codex-startup-m-smart-100x30", cols: 100, rows: 30,
		args: []string{"-m", "smart", "codex"}, codex: true,
		steps: []baselineStep{stepCodexPrompt},
	},
	{
		name: "codex-model-picker-m-smart-100x30", cols: 100, rows: 30,
		args: []string{"-m", "smart", "codex"}, codex: true,
		steps: []baselineStep{stepCodexPrompt, stepCodexOpenModel, stepCodexModelPicker},
	},
	{
		name: "codex-status-m-smart-100x30", cols: 100, rows: 30,
		args: []string{"-m", "smart", "codex"}, codex: true,
		steps: []baselineStep{stepCodexPrompt, stepCodexTypeStatus, stepCodexStatus},
	},
	// One prompt first, so /status shows the stub's usage and the context window.
	{
		name: "codex-status-after-turn-m-smart-100x30", cols: 100, rows: 30,
		args: []string{"-m", "smart", "codex"}, codex: true,
		steps: []baselineStep{stepCodexPrompt, stepCodexSendHi, stepCodexSubmit, stepCodexTypeStatus, stepCodexStatus},
	},
	{
		name: "codex-exec-m-smart-100x30", cols: 100, rows: 30,
		args:      []string{"-m", "smart", "codex"},
		argsAfter: []string{"exec", "--skip-git-repo-check", "-s", "read-only", "hi"}, codex: true,
		steps: []baselineStep{stepPrizmalExit},
	},
}

// TestTerminalBaselinesCodex renders the screens of the real Codex, launched
// through prizmal against the stub switch. Add a screen with a case in
// codexBaselineCases and run with -update-baselines.
func TestTerminalBaselinesCodex(t *testing.T) {
	runBaselineCases(t, codexBaselineCases, codexBaselineDir, false, true)
}

// codexVersionPattern matches the version in Codex's banner, in the TUI and in
// a codex exec header. The comparison reads it as the recorded one for the
// same reason it does for Claude Code's.
var codexVersionPattern = regexp.MustCompile(`OpenAI Codex (\(v[0-9]+\.[0-9]+\.[0-9]+\)|v[0-9]+\.[0-9]+\.[0-9]+)`)

// The session id on /status and on a codex exec run changes per run.
var codexSessionPattern = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// Codex greets a new session with a phrase it picks at random, and a codex
// exec run prints the working directory, which is a temp path that wraps at
// 100 columns. The greeting is the line under the folder row of the header.
var (
	codexGreetingPattern = regexp.MustCompile(`(?m)(^     ~/project\n\n)  [^\n]+`)
	codexWorkdirPattern  = regexp.MustCompile(`(?s)(workdir: ).*?(\nmodel: )`)
	// codexWorkedPattern matches the line a turn ends with: its duration and
	// the wall-clock time it finished.
	codexWorkedPattern = regexp.MustCompile(`(?m)^(\s*Worked for ).*$`)
)

// withStableCodexValues replaces the per-run values on Codex's screens with a
// placeholder.
func withStableCodexValues(screen string) string {
	screen = codexSessionPattern.ReplaceAllString(screen, "<session-id>")
	screen = codexGreetingPattern.ReplaceAllString(screen, "${1}  <greeting>")
	screen = codexWorkedPattern.ReplaceAllString(screen, "${1}<elapsed>")
	return codexWorkdirPattern.ReplaceAllString(screen, "${1}<workdir>${2}")
}

// withBaselineCodexVersion rewrites the Codex version in got to the one in
// want, the recorded baseline.
func withBaselineCodexVersion(got, want string) string {
	recorded := codexVersionPattern.FindString(want)
	if recorded == "" {
		return got
	}
	return codexVersionPattern.ReplaceAllLiteralString(got, recorded)
}

// pinnedCodexDir returns the directory of the codex binary on PATH when its
// version is the one the baselines were recorded with, as pinnedClaudeDir does.
func pinnedCodexDir(t *testing.T) (string, string) {
	t.Helper()
	return pinnedHarnessDir(t, "codex", "Codex", filepath.Join(codexBaselineDir, "codex-version"), -1)
}

// seedCodexState writes the Codex state a returning user has, so the launch
// opens at the prompt: the project folder trusted. Prizmal merges its own
// profile into this file at launch.
func seedCodexState(t *testing.T, home, project string) {
	t.Helper()
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The welcome screen animates and picks a random greeting; with animations
	// off it still greets, but without the motion a capture could land in.
	//
	// The update check asks the network for a newer release and draws a banner
	// when there is one, so a new Codex release would change every screen.
	config := fmt.Sprintf("check_for_update_on_startup = false\n\n[tui]\nanimations = false\n\n[projects.%q]\ntrust_level = \"trusted\"\n", project)
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
}
