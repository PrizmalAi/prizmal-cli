//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// piBaselineDir holds the screens of the real pi.
const piBaselineDir = "testdata/terminal/pi"

var (
	stepPiStartup    = baselineStep{waitFor: "Press ctrl+o"}
	stepPiOpenModels = baselineStep{literal: "/model"}
	stepPiModels     = baselineStep{key: "Enter", waitFor: "Enter to select"}
)

// piBaselineHarness is the real pi, run by the cases that name it.
var piBaselineHarness = baselineHarness{
	name:      "pi",
	pinnedDir: pinnedPiDir,
}

// piBaselineCatalog is the model list the fake Switch serves to the pi cases.
// The names are invented for the test; the case launches the one that leads.
var piBaselineCatalog = []string{"smart", "flash", "premier"}

// piHandwrittenModel is the model entry the cases write into models.json before
// the launch. A launch must keep it beside the rows it owns, and pi's picker
// must offer it, so the screen shows the launch neither dropped nor hid it.
const piHandwrittenModel = "handwritten"

// piBaselineCases are the screens of the real pi, each launched through
// prizmal with -m, so no prizmal picker shows first.
//
// pi asks the network for two things that change what it draws: an update
// check, which draws a release notice under the logo, and its search helpers,
// which it downloads the first time they are missing. The cases run pi
// offline, which switches off the check, and put stand-ins for its helpers on
// PATH, so nothing is downloaded and the screen holds only what the launch
// configured.
func piBaselineCases(t *testing.T) []baselineCase {
	t.Helper()
	env := []string{"PI_OFFLINE=1"}
	standIns := []string{"fd", "rg"}
	return []baselineCase{
		{
			name: "pi-startup-m-smart-100x30", cols: 100, rows: 30,
			args: []string{"-m", "smart", "pi"}, harness: "pi",
			catalog: piBaselineCatalog, setup: seedPiHandwrittenModel,
			env: env, standIns: standIns,
			steps: []baselineStep{stepPiStartup},
		},
		{
			name: "pi-model-picker-m-smart-100x30", cols: 100, rows: 30,
			args: []string{"-m", "smart", "pi"}, harness: "pi",
			catalog: piBaselineCatalog, setup: seedPiHandwrittenModel,
			env: env, standIns: standIns,
			steps: []baselineStep{stepPiStartup, stepPiOpenModels, stepPiModels},
		},
	}
}

// seedPiHandwrittenModel writes a models.json holding one model that prizmal
// does not manage, so the launch has to keep it.
func seedPiHandwrittenModel(t *testing.T, home, _ string) {
	t.Helper()
	path := filepath.Join(home, ".pi", "agent", "models.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSONFile(t, path, map[string]any{
		"providers": map[string]any{
			"prizmal": map[string]any{
				"models": []any{
					map[string]any{
						"id":            piHandwrittenModel,
						"input":         []string{"text"},
						"contextWindow": 65536,
					},
				},
			},
		},
	})
}

// TestTerminalBaselinesPi renders the screens of the real pi, launched through
// prizmal against the stub switch. Add a screen with a case in
// piBaselineCases and run with -update-baselines.
func TestTerminalBaselinesPi(t *testing.T) {
	runBaselineCases(t, piBaselineCases(t), piBaselineDir, piBaselineHarness)
}

// pinnedPiDir returns the directory of the pi binary on PATH when its version
// is the one the baselines were recorded with. pi prints the bare version.
func pinnedPiDir(t *testing.T) (string, string) {
	t.Helper()
	return pinnedHarnessDir(t, "pi", "pi", filepath.Join(piBaselineDir, "pi-version"), -1)
}

// piVersionPattern matches pi's startup banner, whose version is the only part
// that changes between pi releases. It is anchored to that line because the
// rewrite runs over every group's screens, and a bare version pattern would
// also rewrite the release numbers in prizmal's own update screens.
var piVersionPattern = regexp.MustCompile(`(?m)^[^\n]*\x{2580}\x{2580}\x{2588}[^\n]*v[0-9]+\.[0-9]+\.[0-9]+[^\n]*$`)

// withBaselinePiVersion rewrites the pi version in got to the one in want, the
// recorded baseline. A baseline without a version leaves got as is.
func withBaselinePiVersion(got, want string) string {
	recorded := piVersionPattern.FindString(want)
	if recorded == "" {
		return got
	}
	return piVersionPattern.ReplaceAllLiteralString(got, recorded)
}

// TestPiBaselineVersionRewrite keeps the banner rewrite wired: a recorded
// baseline supplies the version, so a pi release that changes nothing else
// leaves the file alone.
func TestPiBaselineVersionRewrite(t *testing.T) {
	recorded := " ▀▀█  v1.5.0\n0.0%/1.0M (auto)\n"
	got := " ▀▀█  v1.6.0\n0.0%/1.0M (auto)\n"
	want := recorded
	if after := withBaselinePiVersion(got, recorded); after != want {
		t.Fatalf("withBaselinePiVersion rewrote to\n%s\nwant\n%s", after, want)
	}
	if after := withBaselinePiVersion(got, "no version here\n"); after != got {
		t.Fatalf("a baseline with no version changed the screen to\n%s", after)
	}
	// The rewrite runs over every group's screens, so it must leave another
	// harness's release numbers alone.
	update := "prizmal v0.2.0 is available. You have v0.1.2.\n"
	if after := withBaselinePiVersion(update, update); after != update {
		t.Fatalf("the rewrite changed an update screen to\n%s", after)
	}
}
