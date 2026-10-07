package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	launcher "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/PrizmalAi/prizmal-cli/internal/update"
)

// recorder stands in for everything the prompt touches and logs the order.
type recorder struct {
	choice      string // what the menu returns
	pickErr     error
	upgradeErr  error
	relaunchErr error
	events      []string
	menus       [][]launcher.Option
	headings    []string
	out         strings.Builder
	relaunched  struct {
		path string
		args []string
	}
}

func (r *recorder) ui(interactive bool) updateUI {
	return updateUI{
		interactive: interactive,
		pick: func(heading string, options []launcher.Option) (string, error) {
			r.events = append(r.events, "pick")
			r.headings = append(r.headings, heading)
			r.menus = append(r.menus, options)
			return r.choice, r.pickErr
		},
		upgrade: func(i update.Install, latest string) error {
			r.events = append(r.events, "upgrade "+latest)
			return r.upgradeErr
		},
		relaunch: func(path string, args []string) error {
			r.events = append(r.events, "relaunch")
			r.relaunched.path, r.relaunched.args = path, args
			return r.relaunchErr
		},
		snooze: func(update.Outcome) { r.events = append(r.events, "snooze") },
		out:    &r.out,
		args:   []string{"--model", "smart", "claude"},
	}
}

func (r *recorder) log() string { return strings.Join(r.events, ",") }

var (
	brewOutcome = update.Outcome{Action: update.Offer, Latest: "v0.2.0", Install: update.Install{
		Method: update.Homebrew, Version: "v0.1.2", Exe: "/b/Caskroom/prizmal/0.1.2/prizmal", BrewPrefix: "/b"}}
	goOutcome = update.Outcome{Action: update.Offer, Latest: "v0.2.0", Install: update.Install{
		Method: update.GoInstall, Version: "v0.1.2", Exe: "/g/bin/prizmal", Package: update.PackagePath}}
	archiveOutcome = update.Outcome{Action: update.Warn, Latest: "v0.2.0", Install: update.Install{
		Method: update.Archive, Version: "v0.1.2", Exe: "/usr/local/bin/prizmal"}}
)

func TestOfferUpgradeSnoozesThenUpgradesThenRelaunches(t *testing.T) {
	r := &recorder{choice: choiceUpgrade}
	if err := r.ui(true).handle(brewOutcome); err != nil {
		t.Fatalf("handle: %v", err)
	}
	// Snooze comes first so a relaunch that is still the old binary stays quiet.
	if got := r.log(); got != "pick,snooze,upgrade v0.2.0,relaunch" {
		t.Fatalf("events = %s", got)
	}
	if r.relaunched.path != filepath.Join("/b", "bin", "prizmal") {
		t.Errorf("relaunched %q, want the Homebrew link, because the Caskroom directory is gone", r.relaunched.path)
	}
	if strings.Join(r.relaunched.args, " ") != "--model smart claude" {
		t.Errorf("relaunch args = %v, want the original arguments", r.relaunched.args)
	}
	for _, want := range []string{"prizmal v0.2.0 is available. You have v0.1.2.", "Upgraded prizmal to v0.2.0. Restarting."} {
		if !strings.Contains(r.out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, r.out.String())
		}
	}
}

func TestOfferShowsTheCommandAndTheVersionItKeeps(t *testing.T) {
	r := &recorder{choice: choiceContinue}
	_ = r.ui(true).handle(goOutcome)
	if r.headings[0] != "Update available" {
		t.Errorf("heading = %q", r.headings[0])
	}
	opts := r.menus[0]
	if len(opts) != 2 || opts[0].Value != choiceUpgrade || opts[1].Value != choiceContinue {
		t.Fatalf("options = %+v, want upgrade then continue", opts)
	}
	if want := "go install " + update.PackagePath + "@v0.2.0"; opts[0].Description != want {
		t.Errorf("upgrade description = %q, want the exact command %q", opts[0].Description, want)
	}
	if opts[1].Description != "Continue with v0.1.2" {
		t.Errorf("not-now description = %q", opts[1].Description)
	}
}

func TestOfferNotNowSnoozesAndContinues(t *testing.T) {
	r := &recorder{choice: choiceContinue}
	if err := r.ui(true).handle(brewOutcome); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if got := r.log(); got != "pick,snooze" {
		t.Fatalf("events = %s, want pick,snooze", got)
	}
}

func TestOfferEscapeLeavesWithoutSnoozing(t *testing.T) {
	r := &recorder{pickErr: launcher.ErrCancelled}
	err := r.ui(true).handle(brewOutcome)
	if !errors.Is(err, launcher.ErrCancelled) {
		t.Fatalf("err = %v, want ErrCancelled: the menu's help line says esc quits", err)
	}
	if got := r.log(); got != "pick" {
		t.Fatalf("events = %s: leaving must not snooze", got)
	}
}

func TestOfferWithoutATerminalOnlyWarns(t *testing.T) {
	r := &recorder{choice: choiceUpgrade}
	if err := r.ui(false).handle(brewOutcome); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if r.log() != "" {
		t.Fatalf("events = %s, want none: a script must not be asked or upgraded", r.log())
	}
	for _, want := range []string{"prizmal v0.2.0 is available. You have v0.1.2.", "Upgrade with: brew upgrade --cask prizmal"} {
		if !strings.Contains(r.out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, r.out.String())
		}
	}
}

func TestFailedUpgradeContinuesOnTheOldVersion(t *testing.T) {
	r := &recorder{choice: choiceUpgrade, upgradeErr: errors.New("exit status 1")}
	if err := r.ui(true).handle(brewOutcome); err != nil {
		t.Fatalf("a failed upgrade ended the launch: %v", err)
	}
	if got := r.log(); got != "pick,snooze,upgrade v0.2.0" {
		t.Fatalf("events = %s: a failed upgrade must not relaunch", got)
	}
	if !strings.Contains(r.out.String(), "Upgrade failed: exit status 1. Continuing with v0.1.2.") {
		t.Errorf("output = %q", r.out.String())
	}
}

func TestFailedRelaunchContinuesOnTheOldVersion(t *testing.T) {
	r := &recorder{choice: choiceUpgrade, relaunchErr: errors.New("exec format error")}
	if err := r.ui(true).handle(goOutcome); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if !strings.Contains(r.out.String(), "Could not restart: exec format error. Continuing with v0.1.2.") {
		t.Errorf("output = %q", r.out.String())
	}
	if r.relaunched.path != "/g/bin/prizmal" {
		t.Errorf("go install relaunched %q, want the binary's own path", r.relaunched.path)
	}
}

func TestWarnAsksToContinueOrExit(t *testing.T) {
	r := &recorder{choice: choiceContinue}
	if err := r.ui(true).handle(archiveOutcome); err != nil {
		t.Fatalf("continue returned %v", err)
	}
	if got := r.log(); got != "pick,snooze" {
		t.Fatalf("events = %s", got)
	}
	if r.headings[0] != "Update available" {
		t.Errorf("heading = %q", r.headings[0])
	}
	opts := r.menus[0]
	if len(opts) != 2 || opts[0].Value != choiceContinue || opts[1].Value != choiceExit {
		t.Fatalf("options = %+v", opts)
	}
	if opts[0].Description != "Run v0.1.2" {
		t.Errorf("continue description = %q", opts[0].Description)
	}
}

func TestWarnExitEndsTheLaunchWithoutSnoozing(t *testing.T) {
	for name, r := range map[string]*recorder{
		"exit":   {choice: choiceExit},
		"escape": {pickErr: launcher.ErrCancelled},
	} {
		err := r.ui(true).handle(archiveOutcome)
		if !errors.Is(err, launcher.ErrCancelled) {
			t.Errorf("%s: err = %v, want ErrCancelled", name, err)
		}
		if got := r.log(); got != "pick" {
			t.Errorf("%s: events = %s: leaving must not snooze, so the next run asks again", name, got)
		}
	}
}

func TestWarnWithoutATerminalPrintsAndContinues(t *testing.T) {
	r := &recorder{choice: choiceExit}
	if err := r.ui(false).handle(archiveOutcome); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if r.log() != "" {
		t.Fatalf("events = %s, want none", r.log())
	}
	for _, want := range []string{"prizmal v0.2.0 is available. You have v0.1.2.",
		"Download it from https://github.com/PrizmalAi/prizmal-cli/releases/latest"} {
		if !strings.Contains(r.out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, r.out.String())
		}
	}
}

func TestUpdateAdvicePerInstall(t *testing.T) {
	src := update.Outcome{Latest: "v0.2.0", Install: update.Install{Method: update.Source}}
	if got := updateAdvice(src); got != "Pull the latest source and rebuild." {
		t.Errorf("source = %q", got)
	}
	gi := update.Outcome{Latest: "v0.2.0", Install: update.Install{Method: update.GoInstall, Package: update.PackagePath}}
	if got := updateAdvice(gi); got != "Upgrade with: go install "+update.PackagePath+"@v0.2.0" {
		t.Errorf("go install = %q", got)
	}
	if got := updateAdvice(archiveOutcome); !strings.HasPrefix(got, "Download it from https://github.com/") {
		t.Errorf("archive = %q", got)
	}
}

func TestAMenuThatFailsToReadContinuesInBothMenus(t *testing.T) {
	for name, o := range map[string]update.Outcome{"offer": brewOutcome, "warn": archiveOutcome} {
		r := &recorder{pickErr: errors.New("read /dev/stdin: input/output error")}
		if err := r.ui(true).handle(o); err != nil {
			t.Errorf("%s: a menu read error ended the launch: %v", name, err)
		}
		if got := r.log(); got != "pick,snooze" {
			t.Errorf("%s: events = %s, want pick,snooze", name, got)
		}
	}
}
