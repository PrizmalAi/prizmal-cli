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
	return func(string) (string, error) {
		line, _, _ := strings.Cut(in, "\n")
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

	cfg, err := ensureConfigWith(scriptedReader("sk-typed-key\n"), true)
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

// The prompt promised a skip it cannot deliver: an empty answer leaves no key
// and no config file, so the next run asks again. Pin the wording.
func TestFirstRunPromptOffersNoSkip(t *testing.T) {
	lower := strings.ToLower(apiKeyPrompt)
	for _, unwanted := range []string{"empty", "skip", "optional"} {
		if strings.Contains(lower, unwanted) {
			t.Errorf("prompt %q still offers %q; an empty answer saves nothing", apiKeyPrompt, unwanted)
		}
	}
	if !strings.Contains(lower, "api key") {
		t.Errorf("prompt %q must say what it wants", apiKeyPrompt)
	}
}
