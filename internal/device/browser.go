package device

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"golang.org/x/term"
)

// EnvTesting, when set to exactly "testing", marks a process a test launched.
// In that state opening a browser is refused outright.
const EnvTesting = "PRIZMAL_ENV"

// TestingEnv returns the environment entries a test subprocess should carry so
// an accidental sign-in cannot leave the machine: the testing marker that
// disables the browser, and a consent-page origin at a non-routable host.
func TestingEnv() []string {
	return []string{
		EnvTesting + "=testing",
		"PRIZMAL_APP_URL=http://127.0.0.1:0",
	}
}

// errTestingNoBrowser is returned by BestEffortOpenBrowser in a testing
// process. A test that signs in must inject its own opener; falling through to
// the real one would pop a browser window on the operator's desktop and point
// it at whatever consent page the process resolved to, which is production
// whenever no URL source is set.
var errTestingNoBrowser = errors.New("refusing to open a browser from a testing process")

// BestEffortOpenBrowser opens url in the platform's default browser. It is
// best effort by design: enrollment works over SSH, where there is no browser
// to open, and the URL is printed either way, so a failure is not fatal.
//
// A testing process never opens one. The check is not a courtesy: an e2e test
// runs the real binary, and any accidental path into sign-in would otherwise
// drive a real browser at the production consent page.
func BestEffortOpenBrowser(url string) error {
	if os.Getenv(EnvTesting) == "testing" {
		return errTestingNoBrowser
	}
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd, args = "open", []string{url}
	case "windows":
		cmd, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		cmd, args = "xdg-open", []string{url}
	}
	return exec.Command(cmd, args...).Start()
}

// readConfirm waits for the operator to press Enter, after Login has printed
// the prompt. It returns true to continue (the browser should open) and false
// when the input is not a terminal or the answer is anything but a bare Enter,
// so a script that runs the CLI without a person never has a browser opened on
// its behalf.
//
// It reads the terminal the way the CLI's readSecret does: a TTY reads a line,
// and a non-TTY is treated as "no" without blocking.
func readConfirm() (bool, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return false, nil
	}
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	return strings.TrimSpace(line) == "", nil
}

// WarnReauthWindow is how long before the sign-in deadline the launcher starts
// warning: under 24 hours, the operator still has time to re-approve without
// an interruption.
const WarnReauthWindow = 24 * time.Hour

// ReauthWarning returns the line the launcher prints when a device's sign-in
// deadline is within WarnReauthWindow, or "" when there is nothing to say. A
// nil deadline is a Directory Sync-managed member, who has none.
func ReauthWarning(reauthBy *time.Time, now time.Time) string {
	if reauthBy == nil {
		return ""
	}
	remaining := reauthBy.Sub(now)
	if remaining > WarnReauthWindow {
		return ""
	}
	if remaining < 0 {
		return "Your Prizmal sign-in is past its deadline. Run prizmal login to re-approve this device."
	}
	return fmt.Sprintf("Your Prizmal sign-in expires in %s. Run prizmal login to re-approve this device.", formatRemaining(remaining))
}

// formatRemaining spells a duration the way a warning reads: whole days or
// hours, floored, and "less than an hour" below that rather than a minutes
// count nobody acts on. A passed deadline never reaches it: ReauthWarning
// answers that with its own sentence.
func formatRemaining(d time.Duration) string {
	if d >= 24*time.Hour {
		return fmt.Sprintf("%d day(s)", int(d.Hours()/24))
	}
	if d >= time.Hour {
		return fmt.Sprintf("%d hour(s)", int(d.Hours()))
	}
	return "less than an hour"
}
