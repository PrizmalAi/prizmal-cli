//go:build !windows

package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

// oldCodex lays out an installed Codex that reports version, behind the
// symlink a package manager leaves on PATH, and a recording stand-in for the
// package manager that upgrades it. realPath is where the binary really lives,
// relative to the sandbox, which is how the install source shows.
type oldCodex struct {
	log     string // the file the package manager stand-in appends its arguments to
	version string // the file the Codex stand-in reads its version from
}

func installOldCodex(t *testing.T, realPath, manager, version string) oldCodex {
	t.Helper()
	root := t.TempDir()
	o := oldCodex{log: filepath.Join(root, "manager.log"), version: filepath.Join(root, "version")}
	if err := os.WriteFile(o.version, []byte(version), 0o644); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(root, realPath)
	bin := filepath.Join(root, "bin")
	for _, dir := range []string{filepath.Dir(real), bin} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The Codex stand-in reads its version from a file, so the upgrade can
	// change what it reports.
	write := func(path, body string) {
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(real, `read v < '`+o.version+`'; echo "codex-cli $v"`)
	if err := os.Symlink(real, filepath.Join(bin, "codex")); err != nil {
		t.Fatal(err)
	}
	if manager != "" {
		write(filepath.Join(bin, manager), `echo "$@" >> '`+o.log+`'; echo `+codexMinVersion+` > '`+o.version+`'`)
	}
	t.Setenv("PATH", bin)
	return o
}

func (o oldCodex) managerCalls(t *testing.T) string {
	t.Helper()
	data, _ := os.ReadFile(o.log)
	return strings.TrimSpace(string(data))
}

// An old Codex is caught where a missing one is, before sign-in and the model
// pick, and a brew cask install is upgraded with the cask command.
func TestEnsureInstalledUpgradesAnOldBrewCaskCodex(t *testing.T) {
	skipWithoutShell(t)
	o := installOldCodex(t, "Caskroom/codex/0.134.0/bin/codex", "brew", "0.134.0")
	launch.SetConfirmPolicy(true)
	t.Cleanup(func() { launch.SetConfirmPolicy(false) })

	if err := EnsureInstalled(); err != nil {
		t.Fatalf("EnsureInstalled: %v", err)
	}
	if got := o.managerCalls(t); got != "upgrade --cask codex" {
		t.Fatalf("brew was called with %q, want %q", got, "upgrade --cask codex")
	}
}

func TestEnsureInstalledUpgradesAnOldNpmCodex(t *testing.T) {
	skipWithoutShell(t)
	o := installOldCodex(t, "lib/node_modules/@openai/codex/bin/codex.js", "npm", "0.134.0")
	launch.SetConfirmPolicy(true)
	t.Cleanup(func() { launch.SetConfirmPolicy(false) })

	if err := EnsureInstalled(); err != nil {
		t.Fatalf("EnsureInstalled: %v", err)
	}
	if got := o.managerCalls(t); got != "update -g @openai/codex" {
		t.Fatalf("npm was called with %q, want %q", got, "update -g @openai/codex")
	}
}

// Declining the offer stops the launch with the command that fits the install,
// and runs nothing.
func TestEnsureInstalledNamesTheUpgradeCommandWhenTheOfferIsDeclined(t *testing.T) {
	skipWithoutShell(t)
	for _, tc := range []struct {
		name, realPath, manager, want string
	}{
		{"brew cask", "Caskroom/codex/0.134.0/bin/codex", "brew", "brew upgrade --cask codex"},
		{"npm", "lib/node_modules/@openai/codex/bin/codex.js", "npm", "npm update -g @openai/codex"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := installOldCodex(t, tc.realPath, tc.manager, "0.134.0")
			var asked string
			previous := launch.DefaultConfirmPrompt
			launch.DefaultConfirmPrompt = func(prompt string, _ launch.ConfirmOptions) (bool, error) {
				asked = prompt
				return false, nil
			}
			t.Cleanup(func() { launch.DefaultConfirmPrompt = previous })

			err := EnsureInstalled()
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "too old") {
				t.Fatalf("EnsureInstalled = %v, want a too-old error naming %q", err, tc.want)
			}
			if !strings.Contains(asked, tc.want) {
				t.Errorf("the offer %q does not name %q", asked, tc.want)
			}
			if got := o.managerCalls(t); got != "" {
				t.Errorf("the package manager ran after the offer was declined: %q", got)
			}
		})
	}
}

// A Codex from a source prizmal does not recognise gets an error that names no
// command, rather than one for a manager that did not install it.
func TestEnsureInstalledDoesNotGuessTheUpgradeCommand(t *testing.T) {
	skipWithoutShell(t)
	installOldCodex(t, "opt/codex/bin/codex", "", "0.134.0")

	err := EnsureInstalled()
	if err == nil || !strings.Contains(err.Error(), "too old") {
		t.Fatalf("EnsureInstalled = %v, want a too-old error", err)
	}
	for _, guess := range []string{"npm", "brew"} {
		if strings.Contains(err.Error(), guess) {
			t.Errorf("the error guesses %q for an install it cannot identify: %v", guess, err)
		}
	}
}
