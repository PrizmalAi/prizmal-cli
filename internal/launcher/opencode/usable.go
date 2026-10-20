package opencode

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"golang.org/x/mod/semver"
)

// openCodeMinVersion is the oldest OpenCode the launch supports. It is the
// release the terminal baselines pin, and a test keeps the two equal.
const openCodeMinVersion = "1.19.3"

// openCodeVersion returns what the installed OpenCode prints for --version.
// It is a variable so a test can supply the output without OpenCode
// installed.
var openCodeVersion = func() (string, error) {
	path, err := findOpenCode(openCodeGOOS)
	if err != nil {
		return "", fmt.Errorf("opencode is not installed, install with: %s", openCodeInstallScript)
	}
	out, err := exec.Command(path, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("failed to get opencode version: %w", err)
	}
	return string(out), nil
}

// openCodeUpgradeArgs are the arguments to OpenCode's own upgrade command,
// which upgrades whichever way OpenCode was installed.
var openCodeUpgradeArgs = []string{"upgrade", openCodeMinVersion}

// CheckUsable reports whether the installed OpenCode can serve a launch. An old
// release gets an upgrade offer. The launch runs it before sign-in.
func CheckUsable() error {
	err := checkOpenCodeVersion()
	var tooOld *openCodeTooOldError
	if !errors.As(err, &tooOld) {
		return err
	}
	path, lookErr := findOpenCode(openCodeGOOS)
	if lookErr != nil {
		return err
	}

	ok, promptErr := launch.ConfirmPrompt(fmt.Sprintf("OpenCode %s is older than the %s prizmal needs. Upgrade with `%s`?",
		tooOld.found, openCodeMinVersion, openCodeUpgradeCommand()))
	if promptErr != nil {
		return promptErr
	}
	if !ok {
		return err
	}

	fmt.Fprintf(os.Stderr, "\nUpgrading OpenCode...\n")
	cmd := exec.Command(path, openCodeUpgradeArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if runErr := cmd.Run(); runErr != nil {
		return fmt.Errorf("failed to upgrade opencode: %w", runErr)
	}
	if err := checkOpenCodeVersion(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "%sOpenCode upgraded successfully%s\n\n", launch.AnsiGreen, launch.AnsiReset)
	return nil
}

func openCodeUpgradeCommand() string {
	return "opencode " + strings.Join(openCodeUpgradeArgs, " ")
}

// openCodeTooOldError says what version was found and how to upgrade it.
type openCodeTooOldError struct {
	found string
}

func (e *openCodeTooOldError) Error() string {
	return fmt.Sprintf("opencode version %s is too old, minimum required is %s, update with: %s",
		e.found, openCodeMinVersion, openCodeUpgradeCommand())
}

// checkOpenCodeVersion compares the installed OpenCode with the floor.
// OpenCode prints the bare version, such as "1.19.3".
func checkOpenCodeVersion() error {
	out, err := openCodeVersion()
	if err != nil {
		return err
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return fmt.Errorf("unexpected opencode version output: %q", out)
	}
	found := strings.TrimPrefix(fields[len(fields)-1], "v")
	if !semver.IsValid("v" + found) {
		return fmt.Errorf("unexpected opencode version output: %q", out)
	}
	if semver.Compare("v"+found, "v"+openCodeMinVersion) < 0 {
		return &openCodeTooOldError{found: found}
	}
	return nil
}
