package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/config"
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

// scriptedLineReader answers successive prompts with successive lines of in,
// so a test can drive a multi-prompt flow (the first-run menu, then the key
// prompt). It repeats the last line if asked for more.
func scriptedLineReader(in string) keyReader {
	lines := strings.Split(in, "\n")
	i := 0
	return func(string) (string, error) {
		line := lines[i]
		if i < len(lines)-1 {
			i++
		}
		return strings.TrimSpace(line), nil
	}
}

// TestEnsureConfigEmptyKeyLeavesNoConfigFile is the report: pressing Enter at
// the prompt left a config file holding base_url and no key, so the next run
// found a config file and never prompted again. An empty answer names no
// credential, so the first run has nothing to persist.
func TestEnsureConfigEmptyKeyLeavesNoConfigFile(t *testing.T) {
	home := useTempHome(t)

	cfg, err := ensureConfigWith(scriptedReader("\n"), true)
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
			cfg, err := ensureConfigWith(scriptedReader(in), true)
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

	cfg, err := ensureConfigWith(scriptedLineReader("2\nsk-typed-key\n"), true)
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

	var called bool
	cfg, err := ensureConfigWithMenu(scriptedLineReader("1\n"), true, func() error {
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
}

// An unexpected answer must not open a browser. The sign-in is an outward
// action; a stray keystroke is not consent to take it.
func TestEnsureConfigUnexpectedAnswerDoesNotSignIn(t *testing.T) {
	useTempHome(t)

	var called bool
	if _, err := ensureConfigWithMenu(scriptedReader("yes\n"), true, func() error {
		called = true
		return nil
	}); err == nil {
		t.Fatal("an unexpected answer did not return an error")
	}
	if called {
		t.Fatal("an unexpected answer opened the browser")
	}
}

// The browser path is unreachable from the test seam: ensureConfigWith supplies
// a no-op sign-in, so a test can never open the operator's browser or reach the
// production consent page.
func TestEnsureConfigWithSeamNeverSignsInBrowser(t *testing.T) {
	useTempHome(t)
	if _, err := ensureConfigWith(scriptedReader("1\n"), true); err != nil {
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
	read := func(string) (string, error) {
		asked = true
		return "", nil
	}
	cfg, err := ensureConfigWithMenu(read, true, func() error { return nil })
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

// The prompt promised a skip it cannot deliver: an empty answer leaves no key
// and no config file, so the next run asks again. Pin the wording.
func TestFirstRunPromptOffersBothChoices(t *testing.T) {
	lower := strings.ToLower(firstRunMenuPrompt)
	if !strings.Contains(lower, "browser") {
		t.Errorf("prompt %q does not offer browser sign-in", firstRunMenuPrompt)
	}
	if !strings.Contains(lower, "key") {
		t.Errorf("prompt %q does not offer pasting a key", firstRunMenuPrompt)
	}
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
