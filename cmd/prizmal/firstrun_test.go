package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/config"
	launcher "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

// useTempHome points the Prizmal config path at a temp directory, so a test
// can inspect exactly which files a first run leaves behind.
func useTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows
	return home
}

// configFileExists reports whether ~/.prizmal/config.json is on disk.
func configFileExists(t *testing.T, home string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(home, ".prizmal", "config.json"))
	return err == nil
}

// scriptedReader stands in for the terminal: it answers the prompt with a
// fixed string instead of reading os.Stdin, so a test never depends on what
// the machine's stdin happens to be and never consumes a developer's
// keystrokes.
func scriptedReader(in string) keyReader {
	line, _, _ := strings.Cut(in, "\n")
	return func(string) (string, error) {
		return strings.TrimSpace(line), nil
	}
}

// pickerReturns is the picker seam a test supplies: it returns the given option
// value as the operator's choice, without a terminal. It records the heading
// and options it was offered so a test can assert the menu's contents.
func pickerReturns(value string) (menuPicker, *struct {
	heading string
	options []launcher.Option
}) {
	seen := &struct {
		heading string
		options []launcher.Option
	}{}
	return func(heading string, options []launcher.Option) (string, error) {
		seen.heading, seen.options = heading, options
		return value, nil
	}, seen
}

// menuCancelled is the picker seam for a backed-out menu.
func menuCancelled(string, []launcher.Option) (string, error) {
	return "", launcher.ErrCancelled
}

// TestEnsureConfigEmptyKeyLeavesNoConfigFile is the report: pressing Enter at
// the key prompt left a config file holding base_url and no key, so the next
// run found a config file and never prompted again. An empty answer names no
// credential, so the first run has nothing to persist.
func TestEnsureConfigEmptyKeyLeavesNoConfigFile(t *testing.T) {
	home := useTempHome(t)
	pick, _ := pickerReturns("key")

	cfg, err := ensureConfigWithMenu(scriptedReader("\n"), true, pick, func() error { return nil })
	if err != nil {
		t.Fatalf("ensureConfig: %v", err)
	}
	if cfg != nil {
		t.Errorf("cfg = %+v, want nil for a skipped key", cfg)
	}
	if configFileExists(t, home) {
		t.Error("an empty key created a config file; the operator asked to skip, not to save")
	}
}

// The empty answer is not only a bare newline: a line of spaces and an EOF
// with nothing before it are the same answer.
func TestEnsureConfigBlankAnswersLeaveNoConfigFile(t *testing.T) {
	cases := map[string]string{
		"newline":         "\n",
		"spaces":          "   \n",
		"eof with none":   "",
		"carriage return": "\r\n",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			home := useTempHome(t)
			pick, _ := pickerReturns("key")
			cfg, err := ensureConfigWithMenu(scriptedReader(in), true, pick, func() error { return nil })
			if err != nil {
				t.Fatalf("ensureConfig: %v", err)
			}
			if cfg != nil {
				t.Errorf("cfg = %+v, want nil", cfg)
			}
			if configFileExists(t, home) {
				t.Error("a blank answer created a config file")
			}
		})
	}
}

// A key that was actually typed still lands in the config file, written once.
func TestEnsureConfigKeyWritesTheConfigFile(t *testing.T) {
	home := useTempHome(t)
	pick, _ := pickerReturns("key")

	cfg, err := ensureConfigWithMenu(scriptedReader("sk-typed-key\n"), true, pick, func() error { return nil })
	if err != nil {
		t.Fatalf("ensureConfig: %v", err)
	}
	if cfg == nil || cfg.APIKey == nil {
		t.Fatalf("cfg = %+v, want a config carrying the key", cfg)
	}
	if got, err := cfg.APIKey.Resolve(); err != nil || got != "sk-typed-key" {
		t.Fatalf("resolved key = %q, %v; want sk-typed-key", got, err)
	}
	if !configFileExists(t, home) {
		t.Fatal("a typed key did not write the config file")
	}
	reloaded, err := config.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got, err := reloaded.APIKey.Resolve(); err != nil || got != "sk-typed-key" {
		t.Fatalf("reloaded key = %q, %v; want sk-typed-key", got, err)
	}
}

// The browser answer of the first-run menu signs in and writes no config file:
// the device key is the credential, and a config file would shadow it.
func TestEnsureConfigBrowserSignInWritesNoConfigFile(t *testing.T) {
	home := useTempHome(t)
	pick, seen := pickerReturns("browser")

	var called bool
	cfg, err := ensureConfigWithMenu(scriptedReader(""), true, pick, func() error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("ensureConfig: %v", err)
	}
	if !called {
		t.Fatal("choosing browser sign-in did not run the sign-in")
	}
	if cfg != nil {
		t.Errorf("cfg = %+v, want nil for the browser path", cfg)
	}
	if configFileExists(t, home) {
		t.Error("the browser sign-in created a config file")
	}
	// The menu is the shared picker under the "Sign in" heading, offering the
	// two ways to authenticate.
	if seen.heading != firstRunSignInHeading {
		t.Errorf("picker heading = %q, want %q", seen.heading, firstRunSignInHeading)
	}
	if len(seen.options) != 2 || seen.options[0].Value != "browser" || seen.options[1].Value != "key" {
		t.Errorf("picker options = %+v, want browser and key", seen.options)
	}
}

// A backed-out menu is the operator's decision: nothing is saved and the first
// run returns without a config.
func TestEnsureConfigMenuCancellationWritesNothing(t *testing.T) {
	home := useTempHome(t)

	cfg, err := ensureConfigWithMenu(scriptedReader(""), true, menuCancelled, func() error {
		return errors.New("should not sign in")
	})
	if err != nil {
		t.Fatalf("ensureConfig: %v", err)
	}
	if cfg != nil {
		t.Errorf("cfg = %+v, want nil after a cancelled menu", cfg)
	}
	if configFileExists(t, home) {
		t.Error("a cancelled menu created a config file")
	}
}

// The browser path is unreachable from the test seam: ensureConfigWith supplies
// a picker that cancels and a no-op sign-in, so a test can never open the
// operator's terminal or browser.
func TestEnsureConfigWithSeamNeverSignsInBrowser(t *testing.T) {
	useTempHome(t)
	if _, err := ensureConfigWith(scriptedReader(""), true); err != nil {
		t.Fatalf("ensureConfig: %v", err)
	}
}

// A non-interactive stdin never prompts and never writes, which is what keeps
// scripts and tests from blocking or creating a config they did not ask for.
func TestEnsureConfigNonInteractiveWritesNothing(t *testing.T) {
	home := useTempHome(t)

	cfg, err := ensureConfigWith(scriptedReader("sk-should-not-be-read\n"), false)
	if err != nil {
		t.Fatalf("ensureConfig: %v", err)
	}
	if cfg != nil {
		t.Errorf("cfg = %+v, want nil without a terminal", cfg)
	}
	if configFileExists(t, home) {
		t.Error("a non-interactive run created a config file")
	}
}

// A machine that already signed in with a device key has no config file, and
// the first run must not offer to replace what is working.
func TestEnsureConfigSkipsMenuWhenDeviceKeyExists(t *testing.T) {
	home := useTempHome(t)
	dir := filepath.Join(home, ".prizmal")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "device.key"), make([]byte, 32), 0o600); err != nil {
		t.Fatal(err)
	}

	var asked bool
	pick := func(string, []launcher.Option) (string, error) {
		asked = true
		return "", nil
	}
	cfg, err := ensureConfigWithMenu(scriptedReader(""), true, pick, func() error { return nil })
	if err != nil {
		t.Fatalf("ensureConfig: %v", err)
	}
	if cfg != nil {
		t.Errorf("cfg = %+v, want nil on a device-keyed machine", cfg)
	}
	if asked {
		t.Error("the first-run menu was shown on a machine that already has a device key")
	}
	if configFileExists(t, home) {
		t.Error("a device-keyed machine got a config file")
	}
}

// The menu's heading and its two rows name the two ways to sign in, which is
// what a person reads on the shared picker.
func TestFirstRunMenuOffersBothChoices(t *testing.T) {
	if strings.ToLower(firstRunSignInHeading) == "" {
		t.Fatalf("heading %q is empty", firstRunSignInHeading)
	}
	opts := firstRunSignInOptions()
	if len(opts) != 2 {
		t.Fatalf("menu offers %d options, want 2", len(opts))
	}
	joined := strings.ToLower(opts[0].Label + " " + opts[1].Label)
	if !strings.Contains(joined, "browser") {
		t.Errorf("menu does not offer browser sign-in: %+v", opts)
	}
	if !strings.Contains(joined, "key") {
		t.Errorf("menu does not offer pasting a key: %+v", opts)
	}
	// The paste path's key prompt must not promise a skip it cannot deliver: an
	// empty answer leaves no key and no config file.
	lowerKey := strings.ToLower(apiKeyPrompt)
	for _, unwanted := range []string{"empty", "skip", "optional"} {
		if strings.Contains(lowerKey, unwanted) {
			t.Errorf("prompt %q still offers %q; an empty answer saves nothing", apiKeyPrompt, unwanted)
		}
	}
	if !strings.Contains(lowerKey, "api key") {
		t.Errorf("prompt %q must say what it wants", apiKeyPrompt)
	}
}
