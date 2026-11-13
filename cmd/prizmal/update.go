package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/PrizmalAi/prizmal-cli/internal/config"
	"github.com/PrizmalAi/prizmal-cli/internal/device"
	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
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
func checkForUpdate() error {
	o := update.RunCheck(time.Now(), update.Current(version), updateSwitch())
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

// updateSwitch is the Switch the check asks which release this install may be
// offered, and the credential it sends. The check runs before the launch loads
// its config, so this resolves the URL the way the launch will: the flag, then
// $PRIZMAL_SWITCH_URL, then the config file, then the default. It never prompts
// and never fails the launch.
//
// The credential is the device token this machine mints from its device key,
// else a switch key from the flag, the environment or the config file. It is
// looked up only when the check is due, so a day's second launch reads neither
// a config command nor the Switch. With neither a device nor a key, the check
// asks as an anonymous caller.
func updateSwitch() update.Switch {
	// The launch sets the same URL moments later, so setting it here changes
	// nothing it reads. The check asks the Switch itself, so it reads the
	// upstream URL: a relay's loopback address is never where it asks.
	envconfig.SetBaseURL(url)
	cfg, err := config.Load()
	if err != nil {
		cfg = nil
	}
	if cfg != nil && cfg.BaseURL != "" {
		envconfig.SetConfigBaseURL(cfg.BaseURL)
	}
	base := envconfig.UpstreamBaseURL()
	return update.Switch{
		BaseURL: base,
		Credential: func() string {
			return updateCredential(cfg, base)
		},
	}
}

// updateCredential returns the credential for the update check, or "" for none.
//
// It follows a launch. A machine with a device key signs in as that device, and
// the device token outranks every switch key, so the switch key never leaves a
// machine whose device can speak for it. A device that cannot refresh leaves the
// switch key in place, as a launch falls back to it.
//
// It reads each switch key source itself and never calls envconfig.APIKey. That
// function answers a device-mode or loopback-relay placeholder first, and a
// placeholder is not a credential the Switch accepts.
func updateCredential(cfg *config.Config, base string) string {
	if token := updateDeviceToken(base); token != "" {
		return token
	}
	if key := strings.TrimSpace(apiKey); key != "" {
		return key
	}
	if key := envconfig.EnvAPIKey(); key != "" {
		return key
	}
	if cfg != nil && cfg.APIKey != nil {
		if key, err := cfg.APIKey.Resolve(); err == nil && strings.TrimSpace(key) != "" {
			return key
		}
	}
	return ""
}

// updateDeviceToken mints, or reuses from the cache, the device token for this
// machine's device key. It returns "" when there is no device key or the device
// cannot refresh.
func updateDeviceToken(base string) string {
	if !deviceKeyExists() {
		return ""
	}
	key, err := device.LoadKey()
	if err != nil {
		return ""
	}
	token, cached, err := device.HelperToken(device.NewClient(base), key, time.Now(), loadCachedToken, time.Sleep)
	if err != nil {
		return ""
	}
	if cached != nil {
		// The launch that follows reads this cache instead of refreshing again
		// inside the Switch's refresh throttle.
		_ = cached.Save()
	}
	return token
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
	switch {
	case errors.Is(err, launcher.ErrCancelled), err == nil && choice == choiceExit:
		return launcher.ErrCancelled
	}
	// Any other menu failure continues on the current version, as the offer
	// above does.
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
