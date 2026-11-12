//go:build !windows

package main

import (
	"regexp"
	"testing"
)

// The screens prizmal draws for its own commands live under
// testdata/terminal/prizmal.

// prizmalBaselineDir holds the screens prizmal's own subcommands draw: the help
// text, the first-run prompt and the device-login flow. They come from the real
// prizmal binary and the stub switch, with no harness involved.
const prizmalBaselineDir = "testdata/terminal/prizmal"

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
	// baselineBackupPath matches the path the revoked-key flow prints for the
	// backup: a temp HOME plus a timestamp, both different on every run, and
	// long enough to wrap, so the whole block up to the next stable line goes.
	baselineBackupPath = regexp.MustCompile(`(?s)(Moved the revoked key to ).*?(\n\nSigning in with your browser)`)
	// baselineSwitchURL matches the stub switch's address in an error line. The
	// stub listens on a different port every run.
	baselineSwitchURL   = regexp.MustCompile(`http://127\.0\.0\.1:[0-9]+`)
	baselineFingerprint = regexp.MustCompile(`Device fingerprint: [0-9a-f]{4}-[0-9a-f]{4}`)
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
	screen = baselineBackupPath.ReplaceAllString(screen, "${1}<backup-path>${2}")
	screen = baselineSwitchURL.ReplaceAllString(screen, "<switch-url>")
	screen = baselineFingerprint.ReplaceAllString(screen, "Device fingerprint: <fp>")
	screen = baselineBrowserFailure.ReplaceAllString(screen, "Could not open a browser automatically.")
	return screen
}

// prizmalBaselineCases are the screens prizmal draws for its own commands: the
// help text, the first-run prompt and the device-login flow. They run the real
// prizmal binary against the stub switch with no harness, so a stand-in claude
// keeps the first-run case from offering an install.
//
// The stub reports a non-routable approval page, so even a stray window reaches
// nothing, and PRIZMAL_ENV=testing already refuses to open a browser. The stub
// decides pending (404) versus approved (200), so neither needs the polling loop
// to advance.
var prizmalBaselineCases = []baselineCase{
	// The help screen, which is where an operator learns the config file holds
	// anything beyond a key: it is the only screen that renders the per-harness
	// launch arguments. The pane is taller than the text on purpose: 30 rows show
	// the tail of it, the flag list, and leave the paragraph this case exists for
	// above the top of the capture, and the slack keeps a help text that grows by
	// a few lines from scrolling its own first line out of the baseline.
	{
		name: "help-100x90", cols: 100, rows: 90,
		args:  []string{"--help"},
		steps: []baselineStep{{waitFor: "[prizmal exited 0]"}},
	},
	// The first-run menu. The config file is absent, so ensureConfig prompts with
	// the shared picker: the two ways to sign in, under the "Sign in" heading.
	{
		name: "firstrun-menu-100x30", cols: 100, rows: 30,
		args:      []string{"--model", "smart"},
		argsAfter: []string{"claude"},
		noConfig:  true,
		steps:     []baselineStep{{waitFor: "Paste a key"}},
	},
	// The browser login while the tenant has not approved yet. The login
	// polls from the moment it prints the URL and opens a browser only on Enter,
	// so the case presses it; the stub answers the refresh 404, so the flow stays
	// at "Waiting for approval..." and the wait is stable to capture.
	{
		name: "login-pending-100x30", cols: 100, rows: 30,
		args: []string{"login"},
		steps: []baselineStep{
			{waitFor: "Press Enter to open the browser"},
			{key: "Enter", waitFor: "Could not open a browser"},
		},
	},
	// The login approved in another browser. Nobody presses Enter: the poll
	// starts when the URL prints, so the stub's 200 ends the wait, takes the
	// prompt off the screen and shows the success line and the deadline.
	{
		name: "login-approved-100x30", cols: 100, rows: 30,
		args:                []string{"login"},
		deviceApprovedAfter: 2,
		steps:               []baselineStep{{waitFor: "This device is approved"}},
	},
	// The operator presses Enter while the approval is still pending, then the
	// approval lands: the screen keeps the browser line Enter produced.
	{
		name: "login-enter-then-approved-100x30", cols: 100, rows: 30,
		args:                []string{"login"},
		deviceApprovedAfter: 4,
		steps: []baselineStep{
			{waitFor: "Press Enter to open the browser"},
			{key: "Enter", waitFor: "This device is approved"},
		},
	},
	// A key that is still inside its approval with more than a day left. The
	// first refresh succeeds, so the login says so, prints no link, names
	// --reapprove and exits 0.
	{
		name: "login-already-approved-100x30", cols: 100, rows: 30,
		args:           []string{"login"},
		deviceKey:      true,
		deviceApproved: true,
		deviceReauthBy: "2099-01-08T00:00:00Z",
		steps:          []baselineStep{{waitFor: "--reapprove"}},
	},
	// --reapprove on a key that is still approved: the link prints anyway, the
	// refreshes keep succeeding with the old deadline, and the wait ends when one
	// reports a later deadline.
	{
		name: "login-reapprove-flag-100x30", cols: 100, rows: 30,
		args:                  []string{"login", "--reapprove"},
		deviceKey:             true,
		deviceApproved:        true,
		deviceReauthBy:        "2099-01-08T00:00:00Z",
		deviceReapprovalAfter: 3,
		steps:                 []baselineStep{{waitFor: "This device is approved"}},
	},
	// A plain login on a key with under a day left re-approves on its own. The
	// stub's default deadline is already past, which is as far under a day as a
	// fixed text can say.
	{
		name: "login-reapprove-automatic-100x30", cols: 100, rows: 30,
		args:                  []string{"login"},
		deviceKey:             true,
		deviceApproved:        true,
		deviceReapprovalAfter: 3,
		steps:                 []baselineStep{{waitFor: "This device is approved"}},
	},
	// --reapprove on a key whose directory manages the sign-in: no deadline, so
	// nothing to extend.
	{
		name: "login-reapprove-no-deadline-100x30", cols: 100, rows: 30,
		args:           []string{"login", "--reapprove"},
		deviceKey:      true,
		deviceApproved: true,
		deviceReauthBy: "none",
		steps:          []baselineStep{{waitFor: "no sign-in deadline"}},
	},
	// A key whose sign-in expired: the first refresh is refused, so the login
	// re-approves the same key, shows the link, waits, and then the approval
	// lands.
	{
		name: "login-reapprove-100x30", cols: 100, rows: 30,
		args:                []string{"login"},
		deviceKey:           true,
		deviceReauthFirst:   true,
		deviceApprovedAfter: 2,
		steps:               []baselineStep{{waitFor: "This device is approved"}},
	},
	// A machine whose device key was revoked. The stub answers the first refresh
	// as the Switch answers a revoked key; the login says so, asks about a
	// backup, and goes on with a new key, which the next refresh approves. The
	// baseline run has a terminal, so the prompt is drawn.
	{
		name: "login-revoked-backup-100x30", cols: 100, rows: 30,
		args:                []string{"login"},
		deviceKey:           true,
		deviceApprovedAfter: 2,
		deviceRevokedFirst:  true,
		steps: []baselineStep{
			{waitFor: "Back up the revoked key"},
			{key: "Enter", waitFor: "This device is approved"},
		},
	},
	// The same login where the operator declines the backup: the key is replaced
	// and no file is kept.
	{
		name: "login-revoked-replace-100x30", cols: 100, rows: 30,
		args:                []string{"login"},
		deviceKey:           true,
		deviceApprovedAfter: 2,
		deviceRevokedFirst:  true,
		steps: []baselineStep{
			{waitFor: "Back up the revoked key"},
			{key: "n"},
			{key: "Enter", waitFor: "This device is approved"},
		},
	},
	// A Switch that does not report an approval page. The login says it uses the
	// default page and goes on to the same screen as any other login.
	{
		name: "login-approval-page-fallback-100x30", cols: 100, rows: 30,
		args:                []string{"login"},
		noApprovalPage:      true,
		deviceApprovedAfter: 2,
		steps:               []baselineStep{{waitFor: "This device is approved"}},
	},
	// Codex missing from PATH with npm present: the launch offers to install
	// it, as it does for the other npm-installed harnesses.
	{
		name: "codex-install-prompt-100x30", cols: 100, rows: 30,
		args:     []string{"--model", "smart", "codex"},
		standIns: []string{"npm"},
		steps:    []baselineStep{{waitFor: "Install with npm?"}},
	},
	// A --model the tenant's list does not carry, such as the legacy
	// claude-tier-opus alias of a config listed under its own name. The launch
	// warns and then starts the harness with the name as given, because the
	// Switch routes aliases its list leaves out.
	{
		name: "model-unlisted-warning-100x30", cols: 100, rows: 30,
		args:  []string{"--model", "claude-tier-opus", "claude"},
		steps: []baselineStep{stepPrizmalExit},
	},
	// `auth token` with no device key is the only screen it draws: its success
	// path prints the token to stdout and nothing else.
	{
		name: "auth-token-no-key-100x30", cols: 100, rows: 30,
		args:  []string{"auth", "token"},
		steps: []baselineStep{{waitFor: "run prizmal login"}},
	},
}

// TestTerminalBaselinesPrizmal renders the screens prizmal draws itself: the
// help text, the first-run prompt and the device-login flow. It needs no
// harness, so it runs wherever tmux is, and its screens live under
// testdata/terminal/prizmal.
func TestTerminalBaselinesPrizmal(t *testing.T) {
	runBaselineCases(t, append(append([]baselineCase{}, prizmalBaselineCases...), updateBaselineCases...), prizmalBaselineDir)
}

// updateBaselineCases are the screens of the update check. Each case poses as
// release 0.1.2 on an install method, against a release host that serves 0.2.0.
// The stubs stand in for brew and go, so the upgrade and the relaunch run for
// real, and the final screen shows the launch that follows.
var updateBaselineCases = func() []baselineCase {
	const latest = "v0.2.0"
	launch := []string{"-m", "smart", "claude"}
	offer := func(name, install string) baselineCase {
		return baselineCase{name: name, cols: 100, rows: 30, args: launch,
			update: &updateScenario{install: install, latest: latest},
			steps:  []baselineStep{{waitFor: "Upgrade now"}}}
	}
	upgrade := func(name, install string, fail bool) baselineCase {
		return baselineCase{name: name, cols: 100, rows: 30, args: launch,
			update: &updateScenario{install: install, latest: latest, failUpgrade: fail},
			steps:  []baselineStep{{waitFor: "Upgrade now"}, {key: "Enter", waitFor: "[prizmal exited"}}}
	}
	warn := func(name, install string) baselineCase {
		return baselineCase{name: name, cols: 100, rows: 30, args: launch,
			update: &updateScenario{install: install, latest: latest},
			steps:  []baselineStep{{waitFor: "Continue"}}}
	}
	return []baselineCase{
		offer("update-offer-homebrew-100x30", "homebrew"),
		offer("update-offer-go-install-100x30", "go-install"),
		upgrade("update-upgrade-homebrew-100x30", "homebrew", false),
		upgrade("update-upgrade-go-install-100x30", "go-install", false),
		upgrade("update-upgrade-homebrew-failed-100x30", "homebrew", true),
		upgrade("update-upgrade-go-install-failed-100x30", "go-install", true),
		{name: "update-declined-100x30", cols: 100, rows: 30, args: launch,
			update: &updateScenario{install: "homebrew", latest: latest},
			steps: []baselineStep{{waitFor: "Upgrade now"}, {key: "Down", waitFor: "Not now"},
				{key: "Enter", waitFor: "[prizmal exited"}}},
		{name: "update-offer-escape-100x30", cols: 100, rows: 30, args: launch,
			update: &updateScenario{install: "homebrew", latest: latest},
			steps:  []baselineStep{{waitFor: "Upgrade now"}, {key: "Escape", waitFor: "[prizmal exited"}}},
		warn("update-warning-archive-100x30", "archive"),
		warn("update-warning-source-100x30", "source"),
		{name: "update-warning-continue-100x30", cols: 100, rows: 30, args: launch,
			update: &updateScenario{install: "archive", latest: latest},
			steps:  []baselineStep{{waitFor: "Continue"}, {key: "Enter", waitFor: "[prizmal exited"}}},
		{name: "update-warning-exit-100x30", cols: 100, rows: 30, args: launch,
			update: &updateScenario{install: "archive", latest: latest},
			steps: []baselineStep{{waitFor: "Continue"}, {key: "Down", waitFor: "Exit"},
				{key: "Enter", waitFor: "[prizmal exited"}}},
		{name: "update-noninteractive-homebrew-100x30", cols: 100, rows: 30, args: launch,
			update: &updateScenario{install: "homebrew", latest: latest, noStdin: true},
			steps:  []baselineStep{stepPrizmalExit}},
		{name: "update-noninteractive-archive-100x30", cols: 100, rows: 30, args: launch,
			update: &updateScenario{install: "archive", latest: latest, noStdin: true},
			steps:  []baselineStep{stepPrizmalExit}},
		{name: "update-yes-100x30", cols: 100, rows: 30, args: append([]string{"--yes"}, launch...),
			update: &updateScenario{install: "homebrew", latest: latest},
			steps:  []baselineStep{stepPrizmalExit}},
		{name: "update-disabled-100x30", cols: 100, rows: 30, args: launch,
			update:      &updateScenario{install: "homebrew", latest: latest},
			configExtra: map[string]any{"check_updates": false},
			steps:       []baselineStep{stepPrizmalExit}},
	}
}()
