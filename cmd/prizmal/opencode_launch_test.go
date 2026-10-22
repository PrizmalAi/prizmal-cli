//go:build !windows

package main

import (
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/stubserver"
)

// TestHarnessLaunchOpencode launches the real OpenCode through prizmal against
// the stub switch, on the switch key in the config file.
func TestHarnessLaunchOpencode(t *testing.T) { runHarnessLaunch(t, "opencode", harnessLaunchConfigKey) }

// TestHarnessLaunchOpencodeWithoutAModel runs OpenCode's one-shot mode on a pipe
// with no model named: the menu cannot open, so the launch fails and lists the
// names the stub's switch key routes.
func TestHarnessLaunchOpencodeWithoutAModel(t *testing.T) {
	runHarnessLaunchWithoutAModel(t, "opencode")
}

// TestHarnessLaunchOpencodeFromDeviceLogin pins that a device-login launch of
// OpenCode starts and authenticates. The stub accepts only the device token its
// own refresh handed out, and the inline config carries no key, so the reply on
// stdout proves the plugin's chat.headers hook ran `prizmal auth token` and set
// the Authorization header.
func TestHarnessLaunchOpencodeFromDeviceLogin(t *testing.T) {
	runHarnessLaunch(t, "opencode", harnessLaunchDeviceLogin)
}

// TestHarnessLaunchOpencodeOutlivesOneDeviceToken runs a real OpenCode session
// across the expiry of a device token. The stub mints a short-lived token on
// every refresh, refuses an expired one, and answers the first two turns with a
// bash call that sleeps, so the session makes several requests over more than
// one token life. It fails when OpenCode keeps sending the first token.
func TestHarnessLaunchOpencodeOutlivesOneDeviceToken(t *testing.T) {
	skipHarnessLaunchInShortMode(t)
	harnessPath := harnessLaunchOnPath(t, "opencode")

	issuer := stubserver.NewDeviceTokenIssuer(piDeviceTokenLife)
	srv := stubserver.NewServer(
		stubserver.WithModels(harnessLaunchModel),
		stubserver.WithShortLivedDeviceTokens(issuer),
		stubserver.WithPiToolLoop(2, piToolTurnSleep),
	)
	t.Cleanup(srv.Close)

	prizmalBin, home, project := harnessLaunchSandbox(t, srv.URL, true)
	harnessArgs := harnessLaunchCases["opencode"].args
	stdout, stderr, timedOut, runErr := runHarnessCommand(t, prizmalBin, home, project, harnessPath, "opencode", harnessArgs)

	if !strings.Contains(stdout, stubserver.Reply) {
		t.Fatalf("opencode did not finish the session (run error: %v, timed out: %v)\n--- stdout ---\n%s\n--- stderr ---\n%s",
			runErr, timedOut, tail(stdout, 50), tail(stderr, 50))
	}
	if accepted := issuer.AcceptedValues(); len(accepted) < 2 {
		t.Fatalf("opencode presented %d accepted device token(s) %v; it kept the first token instead of refreshing",
			len(accepted), accepted)
	}
	if !issuer.CrossedExpiry() {
		t.Fatalf("no accepted request arrived after the first token's life passed; the session never crossed an expiry")
	}
	if n := issuer.RejectedExpired(); n > 0 {
		t.Fatalf("the stub refused %d expired token(s); opencode sent a token past its life", n)
	}
}
