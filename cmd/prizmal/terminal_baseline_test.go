//go:build !windows

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/PrizmalAi/prizmal-cli/internal/stubserver"
)

// Terminal baselines render a screen in a real terminal of a fixed size and
// compare its text, without colour, to a file under testdata/terminal/<harness>.
// Each harness has its own test and its own workflow, so they run in parallel
// and fail under their own names. A pull
// request that changes what an operator sees changes one of those files, and
// the diff shows the change row by row.
//
// tmux is the terminal. It answers the queries Claude Code sends at startup,
// and its capture-pane prints the rendered screen, so the test reads what a
// person would read rather than the byte stream that drew it.
//
// Regenerate the files with:
//
//	go test -run TestTerminalBaselinesClaude -update-baselines .
//
// The Claude Code cases need the Claude Code version recorded in
// testdata/terminal/claude/claude-code-version, because its screens change
// with each release. The claude-code workflow installs that version.

var updateBaselines = flag.Bool("update-baselines", false, "rewrite testdata/terminal from the current render")

const (
	claudeBaselineDir = "testdata/terminal/claude"

	// baselineRequireEnv set to "require" turns a missing tmux or Claude Code
	// into a failure. CI sets it, so a runner without them cannot pass by
	// skipping every case.
	baselineRequireEnv = "PRIZMAL_TERMINAL_BASELINES"

	// baselineShotsEnv, when set to a directory, also saves each screen with
	// its colour codes as <name>.ansi, ready for termframe to draw as an image.
	baselineShotsEnv = "PRIZMAL_TERMINAL_SHOTS"

	// claudeTimeout bounds each wait for a Claude Code screen. Claude Code
	// takes about ten seconds to draw its prompt, and parallel cases share the
	// CPU. The prizmal picker draws in well under a second.
	claudeTimeout = 60 * time.Second
	pickerTimeout = 10 * time.Second
)

// baselineCatalog is the tenant the stub switch serves from GET /v1/models,
// which is where prizmal reads the rows of its picker and of Claude Code's
// /model menu. It lists the reserved placeholder too, which both must hide.
var baselineCatalog = []string{"smart", "flash", "default"}

// tierCatalog is a tenant whose model names carry Claude Code tier words, the
// case where a launch maps each row onto a model Claude Code knows. One name
// hides its tier inside a longer id, as tenant-named models do.
var tierCatalog = []string{"team-opus-blend", "claude-tier-sonnet", "claude-tier-haiku", "claude-tier-fable", "smart", "default"}

// claudeVersionPattern matches the version in Claude Code's banner. A baseline
// keeps the version it was recorded with, and the comparison reads the
// banner as that version, so moving to a new release changes no baseline
// unless something else on the screen changed too.
var claudeVersionPattern = regexp.MustCompile(`Claude Code v[0-9]+\.[0-9]+\.[0-9]+`)

// baselineStep sends keys, then waits until the screen shows waitFor and has
// stopped changing. literal is typed as text, key is a tmux key name such as
// Enter or Escape.
type baselineStep struct {
	literal string
	key     string
	waitFor string
	// andFor is a second string the screen must show, when set.
	andFor string
}

type baselineCase struct {
	name       string
	cols, rows int
	// args are prizmal's own arguments.
	args []string
	// catalog is the tenant the stub switch serves. Empty means baselineCatalog.
	catalog []string
	// claude marks a case that runs the real Claude Code. The other cases put
	// a stand-in on PATH, because prizmal asks to install Claude Code before
	// it opens its picker when none is found.
	claude bool
	steps  []baselineStep
}

// claudeLogoOpen is the logo's top row with its eyes open.
const claudeLogoOpen = "▐▛███▛█"

// claudeLogoFrame matches the logo's top row on any animation frame: seven
// block glyphs before the banner text. The eyes blink and glance aside, and a
// capture can land on any frame.
var claudeLogoFrame = regexp.MustCompile(`(?m)^ [▐▌▛▜▟▙█▀▄▝▘▗▖]{7}(   Claude Code v)`)

// claudeLogo is a row of the logo in Claude Code's startup banner. Claude Code
// can draw the prompt before the logo, and a busy machine can hold the screen
// still for a second in between, so a startup step waits for both.
const claudeLogo = "▝▜██████▀"

var (
	stepPrizmalPicker = baselineStep{waitFor: "Select a model"}
	stepPickFirst     = baselineStep{key: "Enter", waitFor: "shift+tab to cycle", andFor: claudeLogo}
	stepClaudePrompt  = baselineStep{waitFor: "shift+tab to cycle", andFor: claudeLogo}
	stepOpenModel     = baselineStep{literal: "/model", waitFor: "/model"}
	stepModelPicker   = baselineStep{key: "Enter", waitFor: "Esc to cancel"}
	stepTypeContext   = baselineStep{literal: "/context", waitFor: "/context"}
	stepShowContext   = baselineStep{key: "Enter", waitFor: "Free space"}
	stepPrizmalExit   = baselineStep{waitFor: "[prizmal exited"}
	// stepClaudeReady waits for the prompt's footer in any permission mode. A
	// haiku session starts in manual mode, whose footer has no shift+tab hint.
	stepClaudeReady = baselineStep{waitFor: "for agents", andFor: claudeLogo}
)

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
	// A haiku alias runs as Haiku 4.5 with its 200k window. The alias is also a
	// modelOverrides value, and with sorted keys it ran as the retired Claude
	// 3.5 Haiku. Its /model row needs no [1m], which drops the row.
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
}

func TestTerminalBaselinesClaude(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping terminal baselines in short mode")
	}
	require := os.Getenv(baselineRequireEnv) == "require"
	skipOrFail := func(t *testing.T, format string, args ...any) {
		t.Helper()
		if require {
			t.Fatalf(format, args...)
		}
		t.Skipf(format, args...)
	}

	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		skipOrFail(t, "tmux is not on PATH")
	}

	prizmalBin := filepath.Join(t.TempDir(), "prizmal")
	if out, err := exec.Command("go", "build", "-o", prizmalBin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build prizmal: %v\n%s", err, out)
	}

	claudeDir, claudeReason := pinnedClaudeDir(t)

	for _, tc := range claudeBaselineCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.claude && claudeDir == "" {
				skipOrFail(t, "%s", claudeReason)
			}
			got, ansi := renderBaseline(t, tmuxPath, prizmalBin, claudeDir, tc)

			if dir := os.Getenv(baselineShotsEnv); dir != "" {
				if err := os.WriteFile(filepath.Join(dir, tc.name+".ansi"), []byte(ansi), 0o644); err != nil {
					t.Errorf("save colour capture: %v", err)
				}
			}

			path := filepath.Join(claudeBaselineDir, tc.name+".txt")
			want, err := os.ReadFile(path)
			if err == nil {
				got = withBaselineVersion(got, string(want))
			}
			if *updateBaselines {
				// A screen that differs only in the Claude Code version is
				// left alone, so the file keeps its recorded version.
				if err == nil && got == string(want) {
					return
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatalf("read baseline (run with -update-baselines to create it): %v", err)
			}
			if got != string(want) {
				t.Errorf("screen differs from %s. Run with -update-baselines if the change is intended, and put a screenshot in the pull request body.\n%s\n--- got ---\n%s",
					path, lineDiff(string(want), got), got)
			}
		})
	}
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
	pinned, err := os.ReadFile(filepath.Join(claudeBaselineDir, "claude-code-version"))
	if err != nil {
		t.Fatalf("read pinned Claude Code version: %v", err)
	}
	want := strings.TrimSpace(string(pinned))

	path, err := exec.LookPath("claude")
	if err != nil {
		return "", "Claude Code is not on PATH"
	}
	out, err := exec.Command(path, "--version").Output()
	if err != nil {
		return "", fmt.Sprintf("claude --version: %v", err)
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 || fields[0] != want {
		return "", fmt.Sprintf("Claude Code %s is installed, and the baselines were recorded with %s", strings.TrimSpace(string(out)), want)
	}
	return filepath.Dir(path), ""
}

// renderBaseline runs one case in its own tmux server and returns the final
// screen as plain text and with its colour codes.
func renderBaseline(t *testing.T, tmuxPath, prizmalBin, claudeDir string, tc baselineCase) (string, string) {
	t.Helper()

	catalog := tc.catalog
	if len(catalog) == 0 {
		catalog = baselineCatalog
	}
	srv := stubserver.NewWithModels(catalog...)
	t.Cleanup(srv.Close)

	// Claude Code keeps writing into HOME while tmux closes it, so a
	// t.TempDir cleanup would fail the test on a file created mid-removal.
	root, err := os.MkdirTemp("", "prizmal-baseline-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	// Claude Code prints the working directory relative to HOME, as ~/project,
	// only when both are spelled the same way. macOS temp paths are symlinks.
	if root, err = filepath.EvalSymlinks(root); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	project := filepath.Join(home, "project")
	binDir := filepath.Join(root, "bin")
	for _, dir := range []string{project, binDir, filepath.Join(home, ".prizmal")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeJSONFile(t, filepath.Join(home, ".prizmal", "config.json"), map[string]any{
		"version":  1,
		"base_url": srv.URL,
		"api_key":  stubserver.StubKey,
	})

	path := binDir
	if tc.claude {
		path += ":" + claudeDir
		seedClaudeState(t, home, project)
	} else if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	path += ":/usr/bin:/bin"

	// env -i starts the child from nothing, so the runner's own DEBUG, CI or
	// ANTHROPIC_* variables cannot change what it draws. The trailing echo
	// and sleep keep the pane open when prizmal exits early, so the capture
	// shows its error instead of an empty screen.
	command := "env -i " + strings.Join([]string{
		shellQuote("HOME=" + home),
		shellQuote("PATH=" + path),
		"TERM=xterm-256color",
		"LANG=C.UTF-8",
		"PRIZMAL_ENV=testing",
		"DISABLE_AUTOUPDATER=1",
		// Claude Code picks its renderer from a feature flag that Anthropic
		// serves per account. With the flag on, it draws on the alternate
		// screen, which hides prizmal's picker and its key notice, and that
		// is what operators see. From an empty HOME the flag is off and the
		// classic renderer draws below the earlier output instead. This
		// variable selects the alternate-screen renderer whatever the flag.
		"CLAUDE_CODE_NO_FLICKER=1",
	}, " ") + " sh -c " + shellQuote(
		"cd "+shellQuote(project)+" && "+shellQuote(prizmalBin)+" "+shellJoin(tc.args)+`; echo "[prizmal exited $?]"; sleep 600`)

	socket := filepath.Join(root, "tmux.sock")
	tmux := func(args ...string) (string, error) {
		// -f /dev/null ignores the developer's tmux.conf, and the private
		// socket keeps this server apart from every other tmux session.
		full := append([]string{"-u", "-f", "/dev/null", "-S", socket}, args...)
		out, err := exec.Command(tmuxPath, full...).CombinedOutput()
		return string(out), err
	}
	if out, err := tmux("new-session", "-d", "-s", "baseline",
		"-x", fmt.Sprint(tc.cols), "-y", fmt.Sprint(tc.rows), command); err != nil {
		t.Fatalf("start tmux: %v\n%s", err, out)
	}
	t.Cleanup(func() { _, _ = tmux("kill-session", "-t", "baseline") })

	capture := func(colour bool) string {
		args := []string{"capture-pane", "-p", "-t", "baseline"}
		if colour {
			args = append(args, "-e")
		}
		out, err := tmux(args...)
		if err != nil {
			t.Fatalf("capture pane: %v\n%s", err, out)
		}
		return out
	}

	timeout := pickerTimeout
	if tc.claude {
		timeout = claudeTimeout
	}
	for _, step := range tc.steps {
		if step.literal != "" {
			if out, err := tmux("send-keys", "-t", "baseline", "-l", step.literal); err != nil {
				t.Fatalf("send %q: %v\n%s", step.literal, err, out)
			}
		}
		if step.key != "" {
			if out, err := tmux("send-keys", "-t", "baseline", step.key); err != nil {
				t.Fatalf("send %s: %v\n%s", step.key, err, out)
			}
		}
		// A screen that never shows the marker is itself a change, so the
		// remaining keys are skipped and the comparison below prints the diff.
		want := []string{step.waitFor}
		if step.andFor != "" {
			want = append(want, step.andFor)
		}
		if !waitForSettledScreen(func() string { return capture(false) }, want, timeout) {
			t.Errorf("screen did not show all of %q within %s", want, timeout)
			break
		}
	}
	return normalizeScreen(capture(false)), capture(true)
}

// containsAll reports whether screen shows every string in want.
func containsAll(screen string, want []string) bool {
	for _, w := range want {
		if !strings.Contains(screen, w) {
			return false
		}
	}
	return true
}

// waitForSettledScreen polls until the screen shows want and then stays the
// same for a second. Claude Code draws its startup in several passes, and a
// capture between two of them would make the baseline depend on timing. It
// reports false when the screen did not settle on want before the timeout.
func waitForSettledScreen(capture func() string, want []string, timeout time.Duration) bool {
	const (
		interval = 200 * time.Millisecond
		settle   = 5
	)
	deadline := time.Now().Add(timeout)
	var last string
	same := 0
	for time.Now().Before(deadline) {
		screen := capture()
		switch {
		case !containsAll(screen, want):
			same = 0
		case screen == last:
			same++
			if same >= settle {
				return true
			}
		default:
			same = 0
		}
		last = screen
		time.Sleep(interval)
	}
	return false
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
}

func writeJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// normalizeScreen reads every frame of the logo's top row as the open one, and
// drops the padding tmux adds: trailing spaces on each row and the blank rows
// below the last line of text. The rows above keep their position, so a layout
// change still shows in the diff.
func normalizeScreen(screen string) string {
	screen = claudeLogoFrame.ReplaceAllString(screen, " "+claudeLogoOpen+"$1")
	lines := strings.Split(screen, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n") + "\n"
}

// lineDiff lists the rows that differ, numbered from 1, so a failure in the CI
// log points at the row that changed.
func lineDiff(want, got string) string {
	w := strings.Split(want, "\n")
	g := strings.Split(got, "\n")
	var b strings.Builder
	for i := 0; i < max(len(w), len(g)); i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			fmt.Fprintf(&b, "row %d\n  want: %s\n  got:  %s\n", i+1, wl, gl)
		}
	}
	return b.String()
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shellJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellQuote(a)
	}
	return strings.Join(quoted, " ")
}

// Claude Code's logo animates: the top row of the logo draws the eyes open,
// blinking, or glancing aside. A capture taken on any frame must still match
// the baseline, so the comparison reads every frame of that row as the open
// one. Text beside the logo still counts.
func TestNormalizeScreenReadsEveryLogoFrameAsOpen(t *testing.T) {
	open := " ▐▛███▛█   Claude Code v2.1.283\n▝▜██████▀  smart\n"
	for _, frame := range []string{"▐▛███▛█", "▐▟███▟█", "▐█▟███▟", "▐▛███▛▌"} {
		screen := " " + frame + "   Claude Code v2.1.283\n▝▜██████▀  smart\n"
		if got := normalizeScreen(screen); got != open {
			t.Errorf("normalizeScreen(%q) = %q, want %q", frame, got, open)
		}
	}
	changed := " ▐▟███▟█   Claude Code v2.1.283 beta\n"
	if got := normalizeScreen(changed); got != " ▐▛███▛█   Claude Code v2.1.283 beta\n" {
		t.Errorf("normalizeScreen(%q) = %q, want the logo read as open and the text kept", changed, got)
	}
}
