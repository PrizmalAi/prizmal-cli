//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PrizmalAi/prizmal-cli/internal/stubserver"
)

// Pi can refresh a device token through the extension its launch loads: the
// provider there resolves its credential by running `prizmal auth token` for
// every request. Its launch tests live here; the ones for the other harnesses
// live beside this file, one file per harness.

// TestHarnessLaunchPi launches the real Pi through prizmal against the stub
// switch, on the switch key in the config file.
func TestHarnessLaunchPi(t *testing.T) { runHarnessLaunch(t, "pi", harnessLaunchConfigKey) }

// TestHarnessLaunchPiFromDeviceLogin pins that a device-login launch of Pi
// starts and authenticates. With a device key enrolled and a stub that accepts
// only the device token its own refresh handed out, the stub's reply on stdout
// proves Pi ran in device mode on a token the helper minted, not on the switch
// key the config file held.
func TestHarnessLaunchPiFromDeviceLogin(t *testing.T) {
	runHarnessLaunch(t, "pi", harnessLaunchDeviceLogin)
}

// piDeviceTokenLife is how long the short-lived stub's device tokens last in
// TestHarnessLaunchPiOutlivesOneDeviceToken. It is far shorter than
// device.CacheValidMargin, so the helper cannot serve a cached token and must
// refresh for each request, which is what lets a few seconds of session cross an
// expiry.
const piDeviceTokenLife = 2 * time.Second

// piToolTurnSleep is how long each stub-issued bash call sleeps. Pi runs bash
// between turns, so the session's requests are spread by at least this much,
// which puts a second request after the first token's life.
const piToolTurnSleep = 3 * time.Second

// TestHarnessLaunchPiOutlivesOneDeviceToken runs a real Pi session across the
// expiry of a device token. The stub mints a short-lived token on every
// refresh, refuses any token whose life has passed, and answers the first two
// turns with a bash call that sleeps. Pi makes a request per turn, so the
// session spans more than one token life. The test passes only when Pi
// presented a token minted after the first expired; it fails when Pi kept
// sending the first token, because the stub refuses it with 401 and Pi stops.
func TestHarnessLaunchPiOutlivesOneDeviceToken(t *testing.T) {
	skipHarnessLaunchInShortMode(t)
	harnessPath := harnessLaunchOnPath(t, "pi")

	issuer := stubserver.NewDeviceTokenIssuer(piDeviceTokenLife)
	srv := stubserver.NewServer(
		stubserver.WithModels(harnessLaunchModel),
		stubserver.WithShortLivedDeviceTokens(issuer),
		stubserver.WithPiToolLoop(2, piToolTurnSleep),
	)
	t.Cleanup(srv.Close)

	prizmalBin, home, project := harnessLaunchSandbox(t, srv.URL, true)
	harnessArgs := harnessLaunchCases["pi"].args
	stdout, stderr, timedOut, runErr := runHarnessCommand(t, prizmalBin, home, project, harnessPath, "pi", harnessArgs)

	if !strings.Contains(stdout, stubserver.Reply) {
		t.Fatalf("pi did not finish the session (run error: %v, timed out: %v)\n--- stdout ---\n%s\n--- stderr ---\n%s",
			runErr, timedOut, tail(stdout, 50), tail(stderr, 50))
	}
	if got := issuer.Issued(); got < 2 {
		t.Fatalf("the stub minted %d device token(s); the session never crossed an expiry", got)
	}
	if accepted := issuer.AcceptedValues(); len(accepted) < 2 {
		t.Fatalf("pi presented %d accepted device token(s) %v; it kept the first token instead of refreshing",
			len(accepted), accepted)
	}
	if !issuer.CrossedExpiry() {
		t.Fatalf("no accepted request arrived after the first token's life passed; the session never crossed an expiry")
	}
	if n := issuer.RejectedExpired(); n > 0 {
		t.Fatalf("the stub refused %d expired token(s); pi sent a token past its life instead of refreshing", n)
	}
}

// piUserExtensionFileName is the name of the user extension
// TestHarnessLaunchPiKeepsTheUsersExtensions plants in Pi's agent directory.
// The extension writes a marker file when its factory runs, so the test can
// tell whether Pi loaded it.
const piUserExtensionFileName = "prizmal-user-probe.ts"

// TestHarnessLaunchPiKeepsTheUsersExtensions pins that a device-login launch
// leaves the user's own extensions loaded. Pi's --extension adds to discovery
// rather than replacing it, so an extension in the user's agent directory still
// loads beside the one the launch adds. A user who has extensions of their own
// must not lose them to a prizmal launch.
func TestHarnessLaunchPiKeepsTheUsersExtensions(t *testing.T) {
	skipHarnessLaunchInShortMode(t)
	harnessPath := harnessLaunchOnPath(t, "pi")

	srv := stubserver.NewServer(
		stubserver.WithModels(harnessLaunchModel),
		stubserver.WithDeviceTokenOnly(),
	)
	t.Cleanup(srv.Close)

	prizmalBin, home, project := harnessLaunchSandbox(t, srv.URL, true)

	// A user extension in Pi's own agent directory, the place a person's global
	// extensions load from. Its factory appends to a marker file beside it, so
	// the test knows whether Pi loaded it.
	logPath := filepath.Join(home, "user-extension.log")
	userExtension := `import { appendFileSync } from "node:fs";
export default function (pi) {
  appendFileSync(` + strconv.Quote(logPath) + `, "user extension loaded\n");
}
`
	agentExtensions := filepath.Join(home, ".pi", "agent", "extensions")
	if err := os.MkdirAll(agentExtensions, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentExtensions, piUserExtensionFileName), []byte(userExtension), 0o600); err != nil {
		t.Fatal(err)
	}

	harnessArgs := harnessLaunchCases["pi"].args
	stdout, stderr, timedOut, runErr := runHarnessCommand(t, prizmalBin, home, project, harnessPath, "pi", harnessArgs)

	if !strings.Contains(stdout, stubserver.Reply) {
		t.Fatalf("pi did not finish the session (run error: %v, timed out: %v)\n--- stdout ---\n%s\n--- stderr ---\n%s",
			runErr, timedOut, tail(stdout, 50), tail(stderr, 50))
	}
	marker, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("the user's extension did not run: %v\n--- stderr ---\n%s", err, tail(stderr, 30))
	}
	if !strings.Contains(string(marker), "user extension loaded") {
		t.Fatalf("the user's extension ran but wrote nothing: %q", marker)
	}
}
