package cline

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"golang.org/x/mod/semver"
)

// clineMinVersion is the oldest Cline the launch supports. It is the release
// the terminal baselines pin, and a test keeps the two equal.
const clineMinVersion = "3.0.73"

// clineVersion returns what the installed Cline prints for --version. It is a
// variable so a test can supply the output without Cline installed.
var clineVersion = func() (string, error) {
	bin, err := locateCline("")
	if err != nil {
		return "", fmt.Errorf("cline is not installed, install with: %s", strings.Join(clineUpgradeCommand, " "))
	}
	out, err := exec.Command(bin, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("failed to get cline version: %w", err)
	}
	return string(out), nil
}

// clineUpgradeCommand brings Cline to its latest release. It is the same npm
// install a missing Cline gets.
var clineUpgradeCommand = []string{"npm", "install", "-g", "cline@latest"}

// CheckUsable reports whether the installed Cline can serve a launch. An old
// release gets an upgrade offer. The launch runs it before sign-in.
func CheckUsable() error {
	err := checkClineVersion()
	var tooOld *clineTooOldError
	if !errors.As(err, &tooOld) {
		return err
	}
	if _, lookErr := exec.LookPath(clineUpgradeCommand[0]); lookErr != nil {
		return err
	}

	ok, promptErr := launch.ConfirmPrompt(fmt.Sprintf("Cline %s is older than the %s prizmal needs. Upgrade with `%s`?",
		tooOld.found, clineMinVersion, strings.Join(clineUpgradeCommand, " ")))
	if promptErr != nil {
		return promptErr
	}
	if !ok {
		return err
	}

	fmt.Fprintf(os.Stderr, "\nUpgrading Cline...\n")
	cmd := exec.Command(clineUpgradeCommand[0], clineUpgradeCommand[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if runErr := cmd.Run(); runErr != nil {
		return fmt.Errorf("failed to upgrade cline: %w", runErr)
	}
	if err := checkClineVersion(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "%sCline upgraded successfully%s\n\n", launch.AnsiGreen, launch.AnsiReset)
	return nil
}

// clineTooOldError says what version was found and how to upgrade it.
type clineTooOldError struct {
	found string
}

func (e *clineTooOldError) Error() string {
	return fmt.Sprintf("cline version %s is too old, minimum required is %s, update with: %s",
		e.found, clineMinVersion, strings.Join(clineUpgradeCommand, " "))
}

// checkClineVersion compares the installed Cline with the floor. Cline prints
// the bare version, such as "3.0.73".
func checkClineVersion() error {
	out, err := clineVersion()
	if err != nil {
		return err
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return fmt.Errorf("unexpected cline version output: %q", out)
	}
	found := strings.TrimPrefix(fields[len(fields)-1], "v")
	if !semver.IsValid("v" + found) {
		return fmt.Errorf("unexpected cline version output: %q", out)
	}
	if semver.Compare("v"+found, "v"+clineMinVersion) < 0 {
		return &clineTooOldError{found: found}
	}
	return nil
}
