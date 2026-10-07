package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	launcher "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/PrizmalAi/prizmal-cli/internal/update"
	"golang.org/x/term"
)

// updateUI is everything the update prompt touches, so a test drives each
// answer without a terminal, a package manager or a re-exec.
type updateUI struct {
	interactive bool
	pick        func(heading string, options []launcher.Option) (string, error)
	upgrade     func(update.Install, string) error
	relaunch    func(path string, args []string) error
	snooze      func(update.Outcome)
	out         io.Writer
	args        []string
}

// checkForUpdate runs before a launch. It never fails the launch: only the
// operator choosing Exit ends it.
func checkForUpdate(args []string) error {
	o := update.RunCheck(time.Now(), update.Current(version))
	if o.Action == update.None {
		return nil
	}
	ui := updateUI{
		interactive: !yes && updateTerminal(),
		pick:        launcher.PickOption,
		upgrade: func(i update.Install, latest string) error {
			return update.Upgrade(i, latest, os.Stderr, os.Stderr)
		},
		relaunch: relaunchSelf,
		snooze:   func(o update.Outcome) { _ = update.Snooze(time.Now(), o) },
		out:      os.Stderr,
		args:     os.Args[1:],
	}
	return ui.handle(o)
}

// updateTerminal reports whether prizmal can ask: a person on stdin, and a
// terminal for the menu. A launch piped through another command only warns.
var updateTerminal = func() bool {
	return launcher.StdinIsTerminal() && term.IsTerminal(int(os.Stdout.Fd()))
}

const updateHeading = "Update available"

const (
	choiceUpgrade  = "upgrade"
	choiceContinue = "continue"
	choiceExit     = "exit"
)

func (ui updateUI) handle(o update.Outcome) error {
	have := o.Install.Version
	cmd := strings.Join(o.Install.UpgradeCommand(o.Latest), " ")

	if o.Action == update.Offer {
		ui.say("%sprizmal %s is available. You have %s.%s\n", launcher.AnsiYellow, o.Latest, have, launcher.AnsiReset)
		if !ui.interactive {
			ui.say("%sUpgrade with: %s%s\n", launcher.AnsiYellow, cmd, launcher.AnsiReset)
			return nil
		}
		choice, err := ui.pick(updateHeading, []launcher.Option{
			{Label: "Upgrade now", Value: choiceUpgrade, Description: cmd},
			{Label: "Not now", Value: choiceContinue, Description: "Continue with " + have},
		})
		switch {
		case errors.Is(err, launcher.ErrCancelled):
			// Esc leaves, as it does in every other menu.
			return launcher.ErrCancelled
		case err != nil || choice != choiceUpgrade:
			ui.snooze(o)
			return nil
		}
		return ui.upgradeAndRelaunch(o)
	}

	ui.say("%sprizmal %s is available. You have %s.%s\n", launcher.AnsiYellow, o.Latest, have, launcher.AnsiReset)
	ui.say("%s%s%s\n", launcher.AnsiYellow, updateAdvice(o), launcher.AnsiReset)
	if !ui.interactive {
		return nil
	}
	choice, err := ui.pick(updateHeading, []launcher.Option{
		{Label: "Continue", Value: choiceContinue, Description: "Run " + have},
		{Label: "Exit", Value: choiceExit},
	})
	if err != nil || choice == choiceExit {
		return launcher.ErrCancelled
	}
	ui.snooze(o)
	return nil
}

// updateAdvice is how this install gets the release, for the installs prizmal
// cannot run an upgrade for.
func updateAdvice(o update.Outcome) string {
	switch o.Install.Method {
	case update.GoInstall:
		return "Upgrade with: " + strings.Join(o.Install.UpgradeCommand(o.Latest), " ")
	case update.Source:
		return "Pull the latest source and rebuild."
	default:
		return "Download it from https://github.com/" + update.RepoPath + "/releases/latest"
	}
}

func (ui updateUI) upgradeAndRelaunch(o update.Outcome) error {
	have := o.Install.Version
	// Snooze first: a relaunch that is still the old binary must not ask again.
	ui.snooze(o)
	if err := ui.upgrade(o.Install, o.Latest); err != nil {
		ui.say("%sUpgrade failed: %v. Continuing with %s.%s\n", launcher.AnsiYellow, err, have, launcher.AnsiReset)
		return nil
	}
	ui.say("%sUpgraded prizmal to %s. Restarting.%s\n", launcher.AnsiGreen, o.Latest, launcher.AnsiReset)
	if err := ui.relaunch(relaunchPath(o.Install), ui.args); err != nil {
		ui.say("%sCould not restart: %v. Continuing with %s.%s\n", launcher.AnsiYellow, err, have, launcher.AnsiReset)
	}
	return nil
}

// relaunchPath is where the new binary lives. Homebrew deletes the old
// Caskroom directory, so its link in <prefix>/bin is the path that still works.
func relaunchPath(i update.Install) string {
	if i.Method == update.Homebrew {
		return i.BrewLink()
	}
	return i.Exe
}

// relaunchSelf replaces this process with the binary at path. It returns only
// when the exec failed.
var relaunchSelf = func(path string, args []string) error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("a running binary cannot be replaced on Windows")
	}
	return execProcess(path, args, os.Environ())
}

// say writes to the prompt's output. A closed stderr is no reason to stop the
// launch, so the write error is dropped.
func (ui updateUI) say(format string, a ...any) { _, _ = fmt.Fprintf(ui.out, format, a...) }
