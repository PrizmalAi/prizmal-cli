package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/stubserver"
)

// What the compaction and baseline tests share about the pinned harness
// releases. It builds on every OS, because the compaction tests run on Windows,
// where the tmux-driven baselines cannot.
const (
	// baselineRequireEnv set to "require" turns a missing tmux or harness
	// into a failure. CI sets it, so a runner without them cannot pass by
	// skipping every case.
	baselineRequireEnv = "PRIZMAL_TERMINAL_BASELINES"

	// codexBaselineDir and opencodeBaselineDir hold the screens of the real
	// harnesses, recorded with the version in the file beside them.
	codexBaselineDir    = "testdata/terminal/codex"
	opencodeBaselineDir = "testdata/terminal/opencode"
	claudeBaselineDir   = "testdata/terminal/claude"
)

// pinnedHarnessDir returns the directory of the binary on PATH when the
// version it prints is the one recorded in versionFile. Otherwise it returns
// "" and the reason. field is the index of the version in the output of
// `<binary> --version`, and a negative index counts from the end.
func pinnedHarnessDir(t *testing.T, binary, label, versionFile string, field int) (string, string) {
	t.Helper()
	pinned, err := os.ReadFile(versionFile)
	if err != nil {
		t.Fatalf("read pinned %s version: %v", label, err)
	}
	want := strings.TrimSpace(string(pinned))

	path, err := exec.LookPath(binary)
	if err != nil {
		return "", label + " is not on PATH"
	}
	out, err := exec.Command(path, "--version").Output()
	if err != nil {
		return "", fmt.Sprintf("%s --version: %v", binary, err)
	}
	fields := strings.Fields(string(out))
	if field < 0 {
		field += len(fields)
	}
	if field < 0 || field >= len(fields) || fields[field] != want {
		return "", fmt.Sprintf("%s %s is installed, and the baselines were recorded with %s", label, strings.TrimSpace(string(out)), want)
	}
	return filepath.Dir(path), ""
}

// modelsOf lists the model of every request the recorder kept, in order.
func modelsOf(rec *stubserver.Recorder) []string {
	var models []string
	for _, call := range rec.ModelCalls() {
		models = append(models, call.Model)
	}
	return models
}

// assertServed fails when the stub refused any name, or saw none.
func assertServed(t *testing.T, rec *stubserver.Recorder) {
	t.Helper()
	if len(rec.ModelCalls()) == 0 {
		t.Fatal("the harness sent the stub no inference request")
	}
	if refused := rec.RefusedModels(); len(refused) > 0 {
		t.Errorf("the harness sent model names the Switch would refuse: %q (all names sent: %q)", refused, modelsOf(rec))
	}
}

// pinnedClaudeDir returns the directory of the claude binary on PATH when its
// version is the one the baselines were recorded with. Otherwise it returns ""
// and the reason.
func pinnedClaudeDir(t *testing.T) (string, string) {
	t.Helper()
	return pinnedHarnessDir(t, "claude", "Claude Code", filepath.Join(claudeBaselineDir, "claude-code-version"), 0)
}

// seedClaudeState writes the Claude Code state a returning user has, so the
// launch opens at the prompt: onboarding done, release notes read, a theme
// chosen, and the project folder trusted. Without it the first screen is the
// theme picker.
func seedClaudeState(t *testing.T, home, project string) {
	t.Helper()
	pinned, err := os.ReadFile(filepath.Join(claudeBaselineDir, "claude-code-version"))
	if err != nil {
		t.Fatal(err)
	}
	version := strings.TrimSpace(string(pinned))
	writeJSONFile(t, filepath.Join(home, ".claude.json"), map[string]any{
		"hasCompletedOnboarding": true,
		"lastOnboardingVersion":  version,
		"lastReleaseNotesSeen":   version,
		"theme":                  "dark",
		"numStartups":            1,
		"autoUpdates":            false,
		"projects": map[string]any{
			project: map[string]any{"hasTrustDialogAccepted": true},
		},
	})
	// The logo animates, and a capture could land on any frame.
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSONFile(t, filepath.Join(home, ".claude", "settings.json"), map[string]any{
		"prefersReducedMotion": true,
	})
}

func writeJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// A sandboxed child must keep its temporary files under the sandbox home, so a
// run cannot read what an earlier run left in the host's temp directory.
func TestHomeEnvKeepsTempFilesInsideTheHome(t *testing.T) {
	home := t.TempDir()
	names := []string{"TMPDIR"}
	if runtime.GOOS == "windows" {
		names = []string{"TEMP", "TMP"}
	}
	env := homeEnv(home)
	for _, name := range names {
		var got string
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, name+"="); ok {
				got = v
			}
		}
		if rel, err := filepath.Rel(home, got); got == "" || err != nil || strings.HasPrefix(rel, "..") {
			t.Errorf("%s = %q, want a directory under the sandbox home %q", name, got, home)
			continue
		}
		if info, err := os.Stat(got); err != nil || !info.IsDir() {
			t.Errorf("%s = %q does not exist as a directory", name, got)
		}
	}
}
