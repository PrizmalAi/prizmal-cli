//go:build !windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/PrizmalAi/prizmal-cli/internal/stubserver"
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

// piSwitchCaseUserModels and piSwitchCaseUserSettings are the operator's own
// configuration in TestPiModelSwitchInsideALaunchLeavesTheUserConfig: another
// provider, a default of their own, and a scope that does not name prizmal.
const (
	piSwitchCaseUserModels = `{
  "providers": {
    "openrouter": {
      "baseUrl": "https://openrouter.example.invalid/v1",
      "api": "openai-completions",
      "apiKey": "$OPERATOR_KEY",
      "models": [{"id": "zeta", "input": ["text"]}]
    }
  }
}
`
	piSwitchCaseUserSettings = "{\n  \"theme\": \"dark\",\n  \"defaultProvider\": \"openrouter\",\n  \"defaultModel\": \"zeta\",\n  \"enabledModels\": [\"prizmal/*\", \"openrouter/zeta\"]\n}\n"
)

// TestPiModelSwitchInsideALaunchLeavesTheUserConfig drives Pi's own /model
// switch inside a launch, which is the one write-back the CLI cannot prevent by
// writing less: Pi persists the choice into its settings. The launch points Pi
// at an agent directory of its own, so the switch lands there and the operator's
// defaultProvider, defaultModel and enabledModels come out of the session
// exactly as they went in.
func TestPiModelSwitchInsideALaunchLeavesTheUserConfig(t *testing.T) {
	seed := func(t *testing.T, home, _ string) {
		t.Helper()
		agentDir := filepath.Join(home, ".pi", "agent")
		if err := os.MkdirAll(agentDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(piSwitchCaseUserModels), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(piSwitchCaseUserSettings), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	assert := func(t *testing.T, home, _ string) {
		t.Helper()
		agentDir := filepath.Join(home, ".pi", "agent")
		for path, want := range map[string]string{
			filepath.Join(agentDir, "models.json"):   piSwitchCaseUserModels,
			filepath.Join(agentDir, "settings.json"): piSwitchCaseUserSettings,
		} {
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != want {
				t.Fatalf("%s changed inside the launch\nbefore: %s\nafter:  %s", path, want, got)
			}
		}
		// The switch itself happened, and the launch's transcript survived it:
		// Pi writes the session into the user's sessions directory through the
		// launch's link, and the transcript records a model change away from
		// the one the launch started on.
		sessions, _ := filepath.Glob(filepath.Join(agentDir, "sessions", "*", "*.jsonl"))
		if len(sessions) == 0 {
			t.Fatal("the launch recorded no session")
		}
		var transcript strings.Builder
		for _, path := range sessions {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			transcript.Write(data)
		}
		switched := false
		for _, line := range strings.Split(transcript.String(), "\n") {
			var entry struct {
				Type     string `json:"type"`
				Provider string `json:"provider"`
				ModelID  string `json:"modelId"`
			}
			if json.Unmarshal([]byte(line), &entry) == nil && entry.Type == "model_change" &&
				entry.Provider == "prizmal" && entry.ModelID != "" && entry.ModelID != "smart" {
				switched = true
			}
		}
		if !switched {
			t.Fatalf("the transcript records no /model switch away from smart:\n%s", transcript.String())
		}
	}

	cases := []baselineCase{{
		name: "pi-model-switch-leaves-the-user-config", cols: 100, rows: 30,
		args: []string{"-m", "smart", "pi"}, harness: "pi",
		catalog: piBaselineCatalog, setup: seed,
		env: []string{"PI_OFFLINE=1"}, standIns: []string{"fd", "rg"},
		steps: []baselineStep{
			stepPiStartup,
			stepPiOpenModels,
			stepPiModels,
			// The picker's first row is the current model; one Down reaches the
			// operator's own model under the prizmal provider.
			{key: "Down"},
			{key: "Enter", pause: 2 * time.Second},
			// One turn on the new model, so Pi writes a transcript: the model
			// switch is only recorded in it once the session has something to
			// save.
			{literal: "Reply with a short greeting."},
			{key: "Enter", waitFor: stubserver.Reply},
			// Leave the session, so the transcript and Pi's own writes are
			// settled before the assertions read them.
			{literal: "/quit"},
			{key: "Enter", waitFor: "[prizmal exited"},
		},
		assert: assert,
	}}
	runBaselineCases(t, cases, piBaselineDir, piBaselineHarness)
}
