//go:build !windows

package main

import (
	"regexp"
	"testing"
)

// The screens prizmal draws for its own commands live under
// testdata/terminal/prizmal.

// prizmalBaselineDir holds the screens prizmal's own subcommands draw: the
// first-run prompt and the device-login flow. They come from the real
// prizmal binary and the stub switch, with no harness involved.
const prizmalBaselineDir = "testdata/terminal/prizmal"

// nonRoutableAppURL is the consent-page origin a login case gets. A window
// that opened it would reach nothing, and PRIZMAL_ENV=testing already
// refuses to open a browser at all.
const nonRoutableAppURL = "http://127.0.0.1:0"

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
	// The browser login while the tenant has not approved yet. The login
	// polls from the moment it prints the URL and opens a browser only on Enter,
	// so the case presses it; the stub answers the refresh 404, so the flow stays
	// at "Waiting for approval..." and the wait is stable to capture.
	{
		name: "login-pending-100x30", cols: 100, rows: 30,
		args: []string{"login"},
		env:  []string{"PRIZMAL_APP_URL=" + nonRoutableAppURL},
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
		args:           []string{"login"},
		deviceApproved: true,
		env:            []string{"PRIZMAL_APP_URL=" + nonRoutableAppURL},
		steps:          []baselineStep{{waitFor: "This device is approved"}},
	},
	// The operator presses Enter while the approval is still pending, then the
	// approval lands: the screen keeps the browser line Enter produced.
	{
		name: "login-enter-then-approved-100x30", cols: 100, rows: 30,
		args:                []string{"login"},
		deviceApprovedAfter: 4,
		env:                 []string{"PRIZMAL_APP_URL=" + nonRoutableAppURL},
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
		steps:          []baselineStep{{waitFor: "This device is approved"}},
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
// first-run prompt and the device-login flow. It needs no harness, so it runs
// wherever tmux is, and its screens live under testdata/terminal/prizmal.
func TestTerminalBaselinesPrizmal(t *testing.T) {
	runBaselineCases(t, append(append([]baselineCase{}, prizmalBaselineCases...), updateBaselineCases...), prizmalBaselineDir, false, false)
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
