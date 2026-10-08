//go:build !windows

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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
// This file holds what every group shares: the runner, the tmux capture and
// the comparison. Each harness owns its cases, its normalization and its
// pinned version in its own file: terminal_baseline_claude_test.go,
// terminal_baseline_codex_test.go, and terminal_baseline_prizmal_test.go for
// prizmal's own screens. A new harness adds a file of its own.
//
// Regenerate a group's files with:
//
//	go test -run TestTerminalBaselinesClaude -update-baselines .
//
// A group that runs a real harness needs the harness version recorded beside
// its baselines, because its screens change with each release. The harness's
// workflow installs that version.

var updateBaselines = flag.Bool("update-baselines", false, "rewrite testdata/terminal from the current render")

const (
	// baselineRequireEnv set to "require" turns a missing tmux or harness
	// into a failure. CI sets it, so a runner without them cannot pass by
	// skipping every case.
	baselineRequireEnv = "PRIZMAL_TERMINAL_BASELINES"

	// baselineShotsEnv, when set to a directory, also saves each screen with
	// its colour codes as <name>.ansi, ready for termframe to draw as an image.
	baselineShotsEnv = "PRIZMAL_TERMINAL_SHOTS"

	// claudeTimeout bounds each wait for a Claude Code screen. Claude Code
	// takes about ten seconds to draw its prompt. pickerTimeout bounds each
	// wait for a screen prizmal draws, which takes well under a second on an
	// idle machine. Both are long because a wait returns the moment its text
	// shows, so the bound only matters when the machine is busy: parallel
	// cases, another package's tests or a second checkout's run share the
	// CPU, and a bound near the idle time made the baselines depend on what
	// else was running.
	claudeTimeout = 60 * time.Second
	pickerTimeout = 60 * time.Second
)

var (
	stepPrizmalPicker = baselineStep{waitFor: "Select a model"}
	stepPrizmalExit   = baselineStep{waitFor: "[prizmal exited"}
)

// baselineCatalog is the tenant the stub switch serves from GET /v1/models,
// which is where prizmal reads the rows of its picker and of Claude Code's
// /model menu. It lists the reserved placeholder too, which both must hide.
var baselineCatalog = []string{"smart", "flash", "default"}

// baselineStep sends keys, then waits until the screen shows waitFor and has
// stopped changing. literal is typed as text, key is a tmux key name such as
// Enter or Escape.
type baselineStep struct {
	literal string
	key     string
	waitFor string
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
	// codex marks a case that runs the real Codex.
	codex bool
	// codexDir is set by the runner: where the pinned codex binary lives.
	codexDir string
	steps    []baselineStep
	// entries replaces catalog when the stub must send a tier or description.
	entries []stubserver.Entry
	// argsAfter are appended after a `--` separator, so a case can pass text
	// the first integration name would otherwise capture. The first-run case
	// uses it to name the integration the menu would otherwise have chosen.
	argsAfter []string
	// noConfig starts the case without a config file, so the first-run prompt
	// fires. Every other case pre-writes the config the launch needs.
	noConfig bool
	// deviceApproved makes the stub approve the device refresh, so a login
	// case draws the approved screen. The default is pending: 404.
	deviceApproved bool
	// deviceApprovedAfter approves the device on that refresh request, so the
	// login shows its pending screen first and a case can press Enter before
	// the approval lands.
	deviceApprovedAfter int
	// deviceKey writes a device.key into HOME before the launch. It is how a
	// case reaches the re-approve line (key present) or the no-key error
	// (auth token with no key).
	deviceKey bool
	// env is extra environment for the child, on top of the fixed set.
	env []string
	// standIns names executables written to the case's bin directory, each
	// one a script that exits 0. A case that needs a tool on PATH without the
	// real one, such as the npm an install prompt checks for, lists it here.
	standIns []string
	// update poses prizmal as release 0.1.2 and serves a newer release.
	update *updateScenario
	// updateOld and updateNew are set by the runner: prizmal built as releases
	// 0.1.2 and 0.2.0.
	updateOld, updateNew string
	// configExtra adds fields to the config file the case starts with.
	configExtra map[string]any
	// recorder, when set, keeps every inference request the case's stub
	// receives, with the model name it carried and whether the stub served it.
	recorder *stubserver.Recorder
	// setup runs once the case's HOME and project directory exist and before
	// the harness starts, for a case that needs a git repository or a file.
	setup func(t *testing.T, home, project string)
}

// updateScenario describes the install a case poses as and the release host it
// talks to. The stub brew and go replace the binary with release 0.2.0 the way
// the real ones do, by renaming a new file over the old one.
type updateScenario struct {
	// install is homebrew, go-install, source or archive.
	install string
	// latest is the release the host serves.
	latest string
	// failUpgrade makes the stub brew or go exit 1.
	failUpgrade bool
	// noStdin runs prizmal with stdin closed, which is how a script runs it.
	noStdin bool
}

// launchArgs assembles the child's argument list: prizmal's own args, then a
// `--` separator when the case passes text after the integration name, so the
// first-run case can name the integration the menu would otherwise capture.
func launchArgs(tc baselineCase) []string {
	args := append([]string{}, tc.args...)
	if len(tc.argsAfter) > 0 {
		args = append(args, "--")
		args = append(args, tc.argsAfter...)
	}
	return args
}

// runBaselineCases renders one group of cases in parallel and compares each to
// its baseline. needsClaude and needsCodex mark a group that runs that real
// harness and so needs its pinned version installed; the others skip when tmux
// is absent.
func runBaselineCases(t *testing.T, cases []baselineCase, dir string, needsClaude, needsCodex bool) {
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

	claudeDir, claudeReason := "", ""
	if needsClaude {
		claudeDir, claudeReason = pinnedClaudeDir(t)
	}

	codexDir, codexReason := "", ""
	if needsCodex {
		codexDir, codexReason = pinnedCodexDir(t)
	}

	updateOld, updateNew := "", ""
	for _, tc := range cases {
		if tc.update != nil {
			updateOld = buildPrizmalVersion(t, "0.1.2")
			updateNew = buildPrizmalVersion(t, "0.2.0")
			break
		}
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Only a case that runs the real Claude Code needs the pinned
			// version. The prizmal picker cases in the same suite do not, so
			// they still run on a machine with another version installed.
			if tc.claude && claudeDir == "" {
				skipOrFail(t, "%s", claudeReason)
			}
			if tc.codex && codexDir == "" {
				skipOrFail(t, "%s", codexReason)
			}
			tc.codexDir = codexDir
			tc.updateOld, tc.updateNew = updateOld, updateNew
			got, ansi := renderBaseline(t, tmuxPath, prizmalBin, claudeDir, tc)
			got = withStableElapsed(got)
			got = withStableCodexValues(got)
			got = withStableDeviceValues(got)

			if shots := os.Getenv(baselineShotsEnv); shots != "" {
				// The saved capture is the one a screenshot is drawn from, so
				// it is cleaned of the per-run values too: the raw key, hostname
				// and temp path must not reach an image pasted into a pull
				// request. This is a text rewrite, so it leaves the colour
				// escapes around those lines intact.
				if err := os.WriteFile(filepath.Join(shots, tc.name+".ansi"), []byte(withStableDeviceValues(ansi)), 0o644); err != nil {
					t.Errorf("save colour capture: %v", err)
				}
			}

			path := filepath.Join(dir, tc.name+".txt")
			want, err := os.ReadFile(path)
			if err == nil {
				got = withBaselineVersion(got, string(want))
				got = withBaselineCodexVersion(got, string(want))
				got = withBaselineCodexVersion(got, string(want))
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

// pinnedHarnessDir returns the directory of the binary on PATH when the
// version it prints is the one recorded in versionFile. Otherwise it returns
// "" and the reason. field is the index of the version in the output of
// `<binary> --version`, and a negative index counts from the end.
func pinnedHarnessDir(t *testing.T, binary, label, versionFile string, field int) (string, string) {
	t.Helper()
	pinned, err := os.ReadFile(versionFile)
	if err != nil {
		t.Fatalf("read pinned %s version: %v", label, err)
	}
	want := strings.TrimSpace(string(pinned))

	path, err := exec.LookPath(binary)
	if err != nil {
		return "", label + " is not on PATH"
	}
	out, err := exec.Command(path, "--version").Output()
	if err != nil {
		return "", fmt.Sprintf("%s --version: %v", binary, err)
	}
	fields := strings.Fields(string(out))
	if field < 0 {
		field += len(fields)
	}
	if field < 0 || field >= len(fields) || fields[field] != want {
		return "", fmt.Sprintf("%s %s is installed, and the baselines were recorded with %s", label, strings.TrimSpace(string(out)), want)
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
	// The catalog and the device answer are independent options, so a case can
	// name a tenant and still be approved (or pending) without one field
	// shadowing the other.
	var opts []stubserver.ServerOption
	if len(tc.entries) > 0 {
		opts = append(opts, stubserver.WithEntries(tc.entries...))
	} else {
		opts = append(opts, stubserver.WithModels(catalog...))
	}
	if tc.recorder != nil {
		opts = append(opts, stubserver.WithRecorder(tc.recorder))
	}
	if tc.deviceApproved {
		opts = append(opts, stubserver.WithDeviceApproval())
	}
	if tc.deviceApprovedAfter > 0 {
		opts = append(opts, stubserver.WithDeviceApprovalAfter(tc.deviceApprovedAfter))
	}
	srv := stubserver.NewServer(opts...)
	t.Cleanup(srv.Close)

	// Claude Code keeps writing into HOME while tmux closes it, so a
	// t.TempDir cleanup would fail the test on a file created mid-removal.
	//
	// Codex on Linux refuses to create its helper binaries under /tmp and warns
	// instead, so its cases run from the user cache directory.
	tempBase := ""
	if tc.codex {
		cache, err := os.UserCacheDir()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(cache, 0o755); err != nil {
			t.Fatal(err)
		}
		tempBase = cache
	}
	root, err := os.MkdirTemp(tempBase, "prizmal-baseline-")
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
	if tc.setup != nil {
		tc.setup(t, home, project)
	}
	if !tc.noConfig {
		cfg := map[string]any{
			"version":  1,
			"base_url": srv.URL,
			"api_key":  stubserver.StubKey,
		}
		for k, v := range tc.configExtra {
			cfg[k] = v
		}
		writeJSONFile(t, filepath.Join(home, ".prizmal", "config.json"), cfg)
	}
	// A device key on disk is what makes prizmal enter device mode: the login
	// case re-approves it and the auth case without it hits the no-key error.
	// The bytes are arbitrary; the CLI reads them only as an ed25519 seed.
	if tc.deviceKey {
		if err := os.WriteFile(filepath.Join(home, ".prizmal", "device.key"), make([]byte, 32), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	path := binDir
	if tc.codex {
		path += ":" + tc.codexDir
		seedCodexState(t, home, project)
	}
	if tc.claude {
		path += ":" + claudeDir
		seedClaudeState(t, home, project)
	} else if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range tc.standIns {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	path += ":/usr/bin:/bin"

	// env -i starts the child from nothing, so the runner's own DEBUG, CI or
	// ANTHROPIC_* variables cannot change what it draws. The trailing echo
	// and sleep keep the pane open when prizmal exits early, so the capture
	// shows its error instead of an empty screen.
	envVars := []string{
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
	}
	for _, kv := range tc.env {
		envVars = append(envVars, shellQuote(kv))
	}
	stdin := ""
	if tc.update != nil {
		var updateEnv []string
		prizmalBin, updateEnv = seedUpdate(t, root, binDir, tc)
		for _, kv := range updateEnv {
			envVars = append(envVars, shellQuote(kv))
		}
		if tc.update.noStdin {
			stdin = " < /dev/null"
		}
	}
	command := "env -i " + strings.Join(envVars, " ") + " sh -c " + shellQuote(
		"cd "+shellQuote(project)+" && "+shellQuote(prizmalBin)+" "+shellJoin(launchArgs(tc))+stdin+`; echo "[prizmal exited $?]"; sleep 600`)

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
	if tc.claude || tc.codex {
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
		if !waitForSettledScreen(func() string { return capture(false) }, step.waitFor, timeout) {
			t.Errorf("screen did not show %q within %s", step.waitFor, timeout)
			break
		}
	}
	return normalizeScreen(capture(false)), capture(true)
}

// waitForSettledScreen polls until the screen shows want and then stays the
// same for a second. Claude Code draws its startup in several passes, and a
// capture between two of them would make the baseline depend on timing. It
// reports false when the screen did not settle on want before the timeout.
func waitForSettledScreen(capture func() string, want string, timeout time.Duration) bool {
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
		case !strings.Contains(screen, want):
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

// normalizeScreen drops the padding tmux adds: trailing spaces on each row and
// the blank rows below the last line of text. The rows above keep their
// position, so a layout change still shows in the diff.
func normalizeScreen(screen string) string {
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

// buildPrizmalVersion builds prizmal as the given release, so a case can pose
// as an installed release without a published one.
func buildPrizmalVersion(t *testing.T, release string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "prizmal-"+release)
	build := exec.Command("go", "build", "-ldflags", "-X main.version="+release, "-o", out, ".")
	if outb, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build prizmal %s: %v\n%s", release, err, outb)
	}
	return out
}

// seedUpdate lays out the install a case poses as and returns the path of the
// prizmal to run and the extra environment. The binary sits at
// <root>/prefix/bin/prizmal beside a stub brew. The stub go on PATH reports
// that directory as GOBIN. Both stubs rename release 0.2.0 over the binary, as
// brew and go install do, or exit 1 when the case fails the upgrade.
func seedUpdate(t *testing.T, root, binDir string, tc baselineCase) (string, []string) {
	t.Helper()
	u := tc.update
	bin := filepath.Join(root, "prefix", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(bin, "prizmal")
	copyExecutable(t, tc.updateOld, target)

	replace := fmt.Sprintf("cp %s %s.new && mv %s.new %s", shellQuote(tc.updateNew), shellQuote(target), shellQuote(target), shellQuote(target))
	brewBody, goBody := replace+"\n"+`echo "==> prizmal 0.1.2 -> 0.2.0"`, replace+"\n"+`echo "go: downloading github.com/PrizmalAi/prizmal-cli v0.2.0"`
	if u.failUpgrade {
		brewBody = `echo "Error: prizmal: download failed" >&2; exit 1`
		goBody = `echo "go: github.com/PrizmalAi/prizmal-cli@v0.2.0: reading proxy: 404 Not Found" >&2; exit 1`
	}
	writeScript(t, filepath.Join(bin, "brew"), brewBody)
	writeScript(t, filepath.Join(binDir, "go"), fmt.Sprintf(
		"case \"$1\" in\nenv) echo %s; echo /nonexistent;;\ninstall)\n%s\n;;\nesac", shellQuote(bin), goBody))

	releases := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/repos/"):
			_, _ = fmt.Fprintf(w, `{"tag_name":%q}`, u.latest)
		case strings.HasSuffix(r.URL.Path, "/Casks/prizmal.rb"):
			_, _ = fmt.Fprintf(w, "cask \"prizmal\" do\n  version %q\nend\n", strings.TrimPrefix(u.latest, "v"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(releases.Close)

	return target, []string{"PRIZMAL_TEST_INSTALL=" + u.install, "PRIZMAL_TEST_UPDATE_URL=" + releases.URL}
}

func copyExecutable(t *testing.T, from, to string) {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, data, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}
