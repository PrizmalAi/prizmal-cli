//go:build !windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/stubserver"
)

// The Claude Code screens live under testdata/terminal/claude. The claude-code
// workflow installs the Claude Code version recorded in claude-code-version.

const claudeBaselineDir = "testdata/terminal/claude"

// tierCatalog is a tenant with a router config whose name carries a Claude
// Code tier word, hidden inside a longer id as tenant-named models do. Like
// every tenant, it lists router configs only. The tier aliases come from the
// CLI.
var tierCatalog = []string{"team-opus-blend", "smart", "default"}

// proposedCatalog is a tenant whose Switch lists router configs only. Each
// config that holds a tier alias carries that tier, and every config carries
// its tenant's description but one. The tier rows stand for the four tier
// configs, so only the two others get rows of their own.
var proposedCatalog = []stubserver.Entry{
	{ID: "default"},
	{ID: "balanced", Tier: "sonnet", Description: "Balanced cost and speed"},
	{ID: "deep", Tier: "fable", Description: "Long, hard tasks"},
	{ID: "experimental"},
	{ID: "flash", Tier: "haiku", Description: "Quick answers"},
	{ID: "smart", Tier: "opus", Description: "Everyday coding"},
	{ID: "team-opus-blend", Description: "Team blend for refactors"},
}

// claudeVersionPattern matches the version in Claude Code's banner. A baseline
// keeps the version it was recorded with, and the comparison reads the
// banner as that version, so moving to a new release changes no baseline
// unless something else on the screen changed too.
var claudeVersionPattern = regexp.MustCompile(`Claude Code v[0-9]+\.[0-9]+\.[0-9]+`)

// claudeElapsedPattern matches the wall-clock duration the /usage screen prints.
// The screen reads it when it opens, so a loaded machine records a larger
// value and a baseline would depend on how busy the runner was. The
// comparison normalizes it instead, the way it does the banner version.
var claudeElapsedPattern = regexp.MustCompile(`(?m)^(.*Total duration \(wall\): ).*$`)

var (
	stepPickFirst    = baselineStep{key: "Enter", waitFor: "shift+tab to cycle"}
	stepClaudePrompt = baselineStep{waitFor: "shift+tab to cycle"}
	stepOpenModel    = baselineStep{literal: "/model", waitFor: "/model"}
	stepModelPicker  = baselineStep{key: "Enter", waitFor: "Esc to cancel"}
	stepTypeContext  = baselineStep{literal: "/context", waitFor: "/context"}
	stepShowContext  = baselineStep{key: "Enter", waitFor: "Free space"}
	stepOpenUsage    = baselineStep{literal: "/usage", waitFor: "/usage"}
	stepShowUsage    = baselineStep{key: "Enter", waitFor: "Esc to cancel"}

	// stepClaudeReady waits for the prompt's footer in any permission mode. A
	// session on a model that auto mode is closed to starts in manual mode,
	// whose footer has no shift+tab hint.
	stepClaudeReady = baselineStep{waitFor: "for agents"}
)

// claudeBaselineCases are the screens of the real Claude Code, and the prizmal picker shown before it. = []baselineCase{
var claudeBaselineCases = []baselineCase{
	{
		name: "prizmal-picker-100x30", cols: 100, rows: 30,
		args:  []string{"claude"},
		steps: []baselineStep{stepPrizmalPicker},
	},
	{
		name: "prizmal-picker-filter-100x30", cols: 100, rows: 30,
		args: []string{"claude"},
		steps: []baselineStep{
			stepPrizmalPicker,
			{literal: "/", waitFor: "Filter:"},
			{literal: "fla", waitFor: "Filter: fla"},
		},
	},
	{
		name: "claude-startup-picked-100x30", cols: 100, rows: 30,
		args: []string{"claude"}, claude: true,
		steps: []baselineStep{stepPrizmalPicker, stepPickFirst},
	},
	{
		name: "claude-model-picker-picked-100x30", cols: 100, rows: 30,
		args: []string{"claude"}, claude: true,
		steps: []baselineStep{stepPrizmalPicker, stepPickFirst, stepOpenModel, stepModelPicker},
	},
	{
		name: "claude-startup-m-smart-100x30", cols: 100, rows: 30,
		args: []string{"-m", "smart", "claude"}, claude: true,
		steps: []baselineStep{stepClaudePrompt},
	},
	{
		name: "claude-model-picker-m-smart-100x30", cols: 100, rows: 30,
		args: []string{"-m", "smart", "claude"}, claude: true,
		steps: []baselineStep{stepClaudePrompt, stepOpenModel, stepModelPicker},
	},
	{
		name: "claude-startup-model-flash-100x30", cols: 100, rows: 30,
		args: []string{"--model", "flash", "claude"}, claude: true,
		steps: []baselineStep{stepClaudePrompt},
	},
	{
		name: "claude-model-picker-model-flash-100x30", cols: 100, rows: 30,
		args: []string{"--model", "flash", "claude"}, claude: true,
		steps: []baselineStep{stepClaudePrompt, stepOpenModel, stepModelPicker},
	},
	// A --model after the integration name is prizmal's own flag. Forwarded to
	// Claude Code, it outranked the settings model and dropped its [1m], so
	// the session fell back to the 200k window of a model Claude Code does not
	// know. /context shows the window and the model the session runs as, the
	// /model picker shows one row per model with its tier, and a print run
	// shows any unknown-model warning.
	{
		name: "claude-context-model-opus-100x40", cols: 100, rows: 40,
		args: []string{"claude", "--model", "team-opus-blend"}, catalog: tierCatalog, claude: true,
		steps: []baselineStep{stepClaudeReady, stepTypeContext, stepShowContext},
	},
	{
		name: "claude-model-picker-model-opus-100x40", cols: 100, rows: 40,
		args: []string{"claude", "--model", "team-opus-blend"}, catalog: tierCatalog, claude: true,
		steps: []baselineStep{stepClaudeReady, stepOpenModel, stepModelPicker},
	},
	{
		name: "claude-print-model-opus-100x40", cols: 100, rows: 40,
		args: []string{"claude", "--model", "team-opus-blend", "-p", "hi"}, catalog: tierCatalog, claude: true,
		steps: []baselineStep{stepPrizmalExit},
	},
	// A haiku alias runs as Sonnet 5 with a 1M window, in auto mode. As Haiku
	// 4.5, the profile its tier names, it started in manual mode with no way
	// to cycle into auto, because Claude Code refuses auto mode to a model
	// released before Claude Opus 4.6.
	{
		name: "claude-context-model-haiku-100x40", cols: 100, rows: 40,
		args: []string{"claude", "--model", "claude-tier-haiku"}, catalog: tierCatalog, claude: true,
		steps: []baselineStep{stepClaudeReady, stepTypeContext, stepShowContext},
	},
	{
		name: "claude-model-picker-model-haiku-100x40", cols: 100, rows: 40,
		args: []string{"claude", "--model", "claude-tier-haiku"}, catalog: tierCatalog, claude: true,
		steps: []baselineStep{stepClaudeReady, stepOpenModel, stepModelPicker},
	},
	{
		name: "claude-print-model-haiku-100x40", cols: 100, rows: 40,
		args: []string{"claude", "--model", "claude-tier-haiku", "-p", "hi"}, catalog: tierCatalog, claude: true,
		steps: []baselineStep{stepPrizmalExit},
	},
	// The first picker row is the Opus tier, which runs as Opus 5 with a 1M
	// window. A router config without a tier word runs with a 1M window too.
	{
		name: "claude-context-picked-100x40", cols: 100, rows: 40,
		args: []string{"claude"}, claude: true,
		steps: []baselineStep{stepPrizmalPicker, stepPickFirst, stepTypeContext, stepShowContext},
	},
	{
		name: "claude-context-m-smart-100x40", cols: 100, rows: 40,
		args: []string{"-m", "smart", "claude"}, claude: true,
		steps: []baselineStep{stepClaudePrompt, stepTypeContext, stepShowContext},
	},
	// /usage is the screen a client opens to read its balance. The stub Switch
	// sends the same unified status header the real Switch sends, so this
	// records the funded-tenant screen.
	{
		name: "claude-usage-100x40", cols: 100, rows: 40,
		args: []string{"claude"}, claude: true,
		steps: []baselineStep{stepPrizmalPicker, stepPickFirst, stepClaudeReady, stepOpenUsage, stepShowUsage},
	},
	{
		name: "claude-print-m-smart-100x40", cols: 100, rows: 40,
		args: []string{"-m", "smart", "claude", "-p", "hi"}, claude: true,
		steps: []baselineStep{stepPrizmalExit},
	},
	// The proposed Switch: each tier row shows the description of the config
	// that holds the tier's alias, and that config has no row of its own
	// unless the launch names it.
	{
		name: "prizmal-picker-proposed-100x30", cols: 100, rows: 30,
		args: []string{"claude"}, entries: proposedCatalog,
		steps: []baselineStep{stepPrizmalPicker},
	},
	{
		name: "claude-model-picker-proposed-100x40", cols: 100, rows: 40,
		args: []string{"claude"}, entries: proposedCatalog, claude: true,
		steps: []baselineStep{stepPrizmalPicker, stepPickFirst, stepOpenModel, stepModelPicker},
	},
	{
		name: "claude-context-proposed-100x40", cols: 100, rows: 40,
		args: []string{"claude"}, entries: proposedCatalog, claude: true,
		steps: []baselineStep{stepPrizmalPicker, stepPickFirst, stepTypeContext, stepShowContext},
	},
	{
		name: "claude-print-proposed-100x40", cols: 100, rows: 40,
		args: []string{"claude", "--model", "claude-tier-opus", "-p", "hi"}, entries: proposedCatalog, claude: true,
		steps: []baselineStep{stepPrizmalExit},
	},
	{
		name: "claude-model-picker-proposed-m-smart-100x40", cols: 100, rows: 40,
		args: []string{"-m", "smart", "claude"}, entries: proposedCatalog, claude: true,
		steps: []baselineStep{stepClaudePrompt, stepOpenModel, stepModelPicker},
	},
	{
		name: "claude-print-proposed-experimental-100x40", cols: 100, rows: 40,
		args: []string{"claude", "--model", "experimental", "-p", "hi"}, entries: proposedCatalog, claude: true,
		steps: []baselineStep{stepPrizmalExit},
	},
}

func TestTerminalBaselinesClaude(t *testing.T) {
	runBaselineCases(t, claudeBaselineCases, claudeBaselineDir, true, false)
}

// withStableElapsed replaces the wall-clock duration the /usage screen prints
// with a fixed placeholder, so the baseline does not depend on how long the
// session took to reach the screen.
func withStableElapsed(screen string) string {
	return claudeElapsedPattern.ReplaceAllString(screen, "${1}<elapsed>")
}

// withBaselineVersion rewrites the Claude Code version in got to the one in
// want, the recorded baseline. A baseline without a version leaves got as is.
func withBaselineVersion(got, want string) string {
	recorded := claudeVersionPattern.FindString(want)
	if recorded == "" {
		return got
	}
	return claudeVersionPattern.ReplaceAllLiteralString(got, recorded)
}

// pinnedClaudeDir returns the directory of the claude binary on PATH when its
// version is the one the baselines were recorded with. Otherwise it returns ""
// and the reason.
func pinnedClaudeDir(t *testing.T) (string, string) {
	t.Helper()
	return pinnedHarnessDir(t, "claude", "Claude Code", filepath.Join(claudeBaselineDir, "claude-code-version"), 0)
}

// seedClaudeState writes the Claude Code state a returning user has, so the
// launch opens at the prompt: onboarding done, release notes read, a theme
// chosen, and the project folder trusted. Without it the first screen is the
// theme picker.
func seedClaudeState(t *testing.T, home, project string) {
	t.Helper()
	pinned, err := os.ReadFile(filepath.Join(claudeBaselineDir, "claude-code-version"))
	if err != nil {
		t.Fatal(err)
	}
	version := strings.TrimSpace(string(pinned))
	writeJSONFile(t, filepath.Join(home, ".claude.json"), map[string]any{
		"hasCompletedOnboarding": true,
		"lastOnboardingVersion":  version,
		"lastReleaseNotesSeen":   version,
		"theme":                  "dark",
		"numStartups":            1,
		"autoUpdates":            false,
		"projects": map[string]any{
			project: map[string]any{"hasTrustDialogAccepted": true},
		},
	})
	// The logo animates, and a capture could land on any frame.
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSONFile(t, filepath.Join(home, ".claude", "settings.json"), map[string]any{
		"prefersReducedMotion": true,
	})
}

// Claude Code animates its logo: the eyes blink and glance, and the logo
// jumps. A capture can land on any frame, so the seeded state turns animation
// off, which Claude Code offers as its reduced-motion setting.
func TestSeedClaudeStateTurnsOffAnimation(t *testing.T) {
	home := t.TempDir()
	seedClaudeState(t, home, filepath.Join(home, "project"))

	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read seeded settings: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("seeded settings do not parse: %v\n%s", err, data)
	}
	if settings["prefersReducedMotion"] != true {
		t.Fatalf("prefersReducedMotion = %v, want true", settings["prefersReducedMotion"])
	}
}
