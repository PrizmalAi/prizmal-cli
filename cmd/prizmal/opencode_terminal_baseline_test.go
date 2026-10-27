//go:build !windows

package main

import (
	"path/filepath"
	"regexp"
	"testing"
)

var (
	stepOpencodePrompt     = baselineStep{waitFor: "Build · smart"}
	stepOpencodeOpenModels = baselineStep{literal: "/models"}
	stepOpencodeModels     = baselineStep{key: "Enter", waitFor: "Select model"}
)

// opencodeBaselineHarness is the real OpenCode, run by the cases that name it.
var opencodeBaselineHarness = baselineHarness{
	name:      "opencode",
	pinnedDir: pinnedOpencodeDir,
	stable:    withStableOpencodeValues,
}

// opencodeBaselineCases are the screens of the real OpenCode, each launched
// through prizmal with -m, so no prizmal picker shows first.
//
// OpenCode reads two things from the network that change what it draws: the
// update check, which draws an upgrade line under the logo when a newer
// release is out, and the models.dev catalog, whose free models fill the
// picker. The cases switch off the first and point the second at an empty
// catalog, so the picker lists the launch's models alone.
func opencodeBaselineCases(t *testing.T) []baselineCase {
	t.Helper()
	catalog, err := filepath.Abs(filepath.Join(opencodeBaselineDir, "models-dev-empty.json"))
	if err != nil {
		t.Fatal(err)
	}
	env := []string{"OPENCODE_DISABLE_AUTOUPDATE=1", "OPENCODE_MODELS_PATH=" + catalog}
	return []baselineCase{
		{
			name: "opencode-startup-m-smart-100x30", cols: 100, rows: 30,
			args: []string{"-m", "smart", "opencode"}, harness: "opencode", env: env,
			steps: []baselineStep{stepOpencodePrompt},
		},
		{
			name: "opencode-model-picker-m-smart-100x30", cols: 100, rows: 30,
			args: []string{"-m", "smart", "opencode"}, harness: "opencode", env: env,
			steps: []baselineStep{stepOpencodePrompt, stepOpencodeOpenModels, stepOpencodeModels},
		},
		{
			name: "opencode-run-m-smart-100x30", cols: 100, rows: 30,
			args: []string{"-m", "smart", "opencode"}, argsAfter: []string{"run", "hi"},
			harness: "opencode", env: env,
			steps: []baselineStep{stepPrizmalExit},
		},
	}
}

// TestTerminalBaselinesOpencode renders the screens of the real OpenCode,
// launched through prizmal against the stub switch. Add a screen with a case
// in opencodeBaselineCases and run with -update-baselines.
func TestTerminalBaselinesOpencode(t *testing.T) {
	runBaselineCases(t, opencodeBaselineCases(t), opencodeBaselineDir, opencodeBaselineHarness)
}

// pinnedOpencodeDir returns the directory of the opencode binary on PATH when
// its version is the one the baselines were recorded with. OpenCode prints
// the bare version.
func pinnedOpencodeDir(t *testing.T) (string, string) {
	t.Helper()
	return pinnedHarnessDir(t, "opencode", "OpenCode", filepath.Join(opencodeBaselineDir, "opencode-version"), -1)
}

// OpenCode's empty prompt shows an example it picks at random, and the home
// screen and the picker can show a tip it picks at random.
var (
	opencodeExamplePattern = regexp.MustCompile(`(Ask anything… )"[^"\n]*"`)
	opencodeTipPattern     = regexp.MustCompile(`(● Tip ).*`)
)

// withStableOpencodeValues replaces the per-run values on OpenCode's screens
// with a placeholder.
func withStableOpencodeValues(screen string) string {
	screen = opencodeExamplePattern.ReplaceAllString(screen, `${1}"<example>"`)
	return opencodeTipPattern.ReplaceAllString(screen, "${1}<tip>")
}
