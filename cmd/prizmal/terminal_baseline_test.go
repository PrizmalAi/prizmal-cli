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

	// prizmalBaselineDir holds the screens prizmal's own subcommands draw: the
	// first-run prompt and the device-login flow. They come from the real
	// prizmal binary and the stub switch, with no harness involved.
	prizmalBaselineDir = "testdata/terminal/prizmal"

	// nonRoutableAppURL is the consent-page origin a login case gets. A window
	// that opened it would reach nothing, and PRIZMAL_ENV=testing already
	// refuses to open a browser at all.
	nonRoutableAppURL = "http://127.0.0.1:0"

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

// The login and first-run screens carry values that change per run and per
// machine, so the comparison replaces each with a placeholder. The values are
// still drawn correctly; only the baseline is made stable.
//
// Two of them are long enough to wrap at 100 columns, and where they wrap
// depends on the machine (a temp path) or the hostname. So each is normalized
// as the whole block between two stable lines, not as a token: a token match
// would leave the wrapped continuation behind, and its position would differ
// between the recorder's machine and the CI runner.
//
//   - The consent URL holds a freshly generated ed25519 public key (per run)
//     and the machine's hostname.
//   - The first-run screen prints the config path under the run's temp HOME.
//   - The device fingerprint is derived from the key, so it changes too, but it
//     is short and stays on one line.
var (
	baselineConsentURLPattern = regexp.MustCompile(`(?s)(To approve this device, open:\n\n).*?(\n\nDevice fingerprint: )`)
	// baselineConfigPathPattern matches the config path the first-run screen
	// prints. It ends on the stable ".prizmal/config.json" suffix rather than on
	// the next line's text, because in the colour capture that line opens with a
	// style escape the plain capture does not carry.
	baselineConfigPathPattern = regexp.MustCompile(`(?s)(No configuration found at ).*?\.prizmal/config\.json`)
	baselineFingerprint       = regexp.MustCompile(`Device fingerprint: [0-9a-f]{4}-[0-9a-f]{4}`)
	// baselineBrowserFailure matches the parenthetical reason the login prints
	// when it cannot open a browser. In a baseline run the reason is always the
	// testing guard, which is an artifact of the harness and differs on a real
	// host, so the line is normalized to its stable prefix.
	baselineBrowserFailure = regexp.MustCompile(`Could not open a browser automatically \([^)]*\)\.`)
)

// withStableDeviceValues replaces the per-run values on the login and first-run
// screens with placeholders, so their baselines do not depend on the generated
// key, the machine's hostname, or the temp directory of the run.
func withStableDeviceValues(screen string) string {
	screen = baselineConsentURLPattern.ReplaceAllString(screen, "${1}<consent-url>${2}")
	screen = baselineConfigPathPattern.ReplaceAllString(screen, "${1}<config-path>${2}")
	screen = baselineFingerprint.ReplaceAllString(screen, "Device fingerprint: <fp>")
	screen = baselineBrowserFailure.ReplaceAllString(screen, "Could not open a browser automatically.")
	return screen
}

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
	steps  []baselineStep
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
	// deviceKey writes a device.key into HOME before the launch. It is how a
	// case reaches the re-approve line (key present) or the no-key error
	// (auth token with no key).
	deviceKey bool
	// env is extra environment for the child, on top of the fixed set.
	env []string
}

var (
	stepPrizmalPicker = baselineStep{waitFor: "Select a model"}
	stepPickFirst     = baselineStep{key: "Enter", waitFor: "shift+tab to cycle"}
	stepClaudePrompt  = baselineStep{waitFor: "shift+tab to cycle"}
	stepOpenModel     = baselineStep{literal: "/model", waitFor: "/model"}
	stepModelPicker   = baselineStep{key: "Enter", waitFor: "Esc to cancel"}
	stepTypeContext   = baselineStep{literal: "/context", waitFor: "/context"}
	stepShowContext   = baselineStep{key: "Enter", waitFor: "Free space"}
	stepOpenUsage     = baselineStep{literal: "/usage", waitFor: "/usage"}
	stepShowUsage     = baselineStep{key: "Enter", waitFor: "Esc to cancel"}
	stepPrizmalExit   = baselineStep{waitFor: "[prizmal exited"}
	// stepClaudeReady waits for the prompt's footer in any permission mode. A
	// session on a model that auto mode is closed to starts in manual mode,
	// whose footer has no shift+tab hint.
	stepClaudeReady = baselineStep{waitFor: "for agents"}
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

// prizmalBaselineCases are the screens prizmal draws for its own commands: the
// first-run prompt and the device-login flow. They run the real prizmal binary
// against the stub switch with no harness, so a stand-in claude keeps the
// first-run case from offering an install.
//
// The login cases pin PRIZMAL_APP_URL to a non-routable host. PRIZMAL_ENV=testing
// already refuses to open a browser, and the pinned URL keeps even a stray
// window off the production consent page. The stub decides pending (404) versus
// approved (200), so neither needs the polling loop to advance.
var prizmalBaselineCases = []baselineCase{
	// The first-run menu. The config file is absent, so ensureConfig prompts with
	// the shared picker: the two ways to sign in, under the "Sign in" heading.
	{
		name: "firstrun-menu-100x30", cols: 100, rows: 30,
		args:      []string{"--model", "smart"},
		argsAfter: []string{"claude"},
		noConfig:  true,
		steps:     []baselineStep{{waitFor: "Paste a key"}},
	},
	// The browser login while the tenant has not approved yet. The login waits
	// for Enter before it opens a browser, so the case presses it; the stub then
	// answers the refresh 404, so the flow stops at "Waiting for approval..." and
	// the wait is stable to capture.
	{
		name: "login-pending-100x30", cols: 100, rows: 30,
		args: []string{"login"},
		env:  []string{"PRIZMAL_APP_URL=" + nonRoutableAppURL},
		steps: []baselineStep{
			{waitFor: "Press Enter to open the browser"},
			{key: "Enter", waitFor: "Waiting for approval"},
		},
	},
	// The approved login. The stub answers 200 with a token and a reauth_by, so
	// the screen shows the success line and the deadline.
	{
		name: "login-approved-100x30", cols: 100, rows: 30,
		args:           []string{"login"},
		deviceApproved: true,
		env:            []string{"PRIZMAL_APP_URL=" + nonRoutableAppURL},
		steps: []baselineStep{
			{waitFor: "Press Enter to open the browser"},
			{key: "Enter", waitFor: "This device is approved"},
		},
	},
	// Re-approving a machine that already has a device key: the same screen with
	// the re-approve line instead of "Generated a new device key".
	{
		name: "login-reapprove-100x30", cols: 100, rows: 30,
		args:           []string{"login"},
		deviceKey:      true,
		deviceApproved: true,
		env:            []string{"PRIZMAL_APP_URL=" + nonRoutableAppURL},
		steps: []baselineStep{
			{waitFor: "Re-approving the device key"},
			{key: "Enter", waitFor: "This device is approved"},
		},
	},
	// `auth token` with no device key is the only screen it draws: its success
	// path prints the token to stdout and nothing else.
	{
		name: "auth-token-no-key-100x30", cols: 100, rows: 30,
		args:  []string{"auth", "token"},
		steps: []baselineStep{{waitFor: "run prizmal login"}},
	},
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

func TestTerminalBaselinesClaude(t *testing.T) {
	runBaselineCases(t, claudeBaselineCases, claudeBaselineDir, true)
}

// TestTerminalBaselinesPrizmal renders the screens prizmal draws itself: the
// first-run prompt and the device-login flow. It needs no harness, so it runs
// wherever tmux is, and its screens live under testdata/terminal/prizmal.
func TestTerminalBaselinesPrizmal(t *testing.T) {
	runBaselineCases(t, prizmalBaselineCases, prizmalBaselineDir, false)
}

// runBaselineCases renders one group of cases in parallel and compares each to
// its baseline. needsClaude marks the group that runs the real Claude Code and
// so needs the pinned version installed; the others skip when tmux is absent.
func runBaselineCases(t *testing.T, cases []baselineCase, dir string, needsClaude bool) {
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

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Only a case that runs the real Claude Code needs the pinned
			// version. The prizmal picker cases in the same suite do not, so
			// they still run on a machine with another version installed.
			if tc.claude && claudeDir == "" {
				skipOrFail(t, "%s", claudeReason)
			}
			got, ansi := renderBaseline(t, tmuxPath, prizmalBin, claudeDir, tc)
			got = withStableElapsed(got)
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
	// The catalog and the device answer are independent options, so a case can
	// name a tenant and still be approved (or pending) without one field
	// shadowing the other.
	var opts []stubserver.ServerOption
	if len(tc.entries) > 0 {
		opts = append(opts, stubserver.WithEntries(tc.entries...))
	} else {
		opts = append(opts, stubserver.WithModels(catalog...))
	}
	if tc.deviceApproved {
		opts = append(opts, stubserver.WithDeviceApproval())
	}
	srv := stubserver.NewServer(opts...)
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
	if !tc.noConfig {
		writeJSONFile(t, filepath.Join(home, ".prizmal", "config.json"), map[string]any{
			"version":  1,
			"base_url": srv.URL,
			"api_key":  stubserver.StubKey,
		})
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
	command := "env -i " + strings.Join(envVars, " ") + " sh -c " + shellQuote(
		"cd "+shellQuote(project)+" && "+shellQuote(prizmalBin)+" "+shellJoin(launchArgs(tc))+`; echo "[prizmal exited $?]"; sleep 600`)

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
