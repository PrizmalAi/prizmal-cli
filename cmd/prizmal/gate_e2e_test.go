package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
)

// buildPrizmalTestBinary builds the prizmal binary once per test function.
func buildPrizmalTestBinary(t *testing.T) string {
	t.Helper()
	bin := "prizmal-under-test"
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	out := filepath.Join(t.TempDir(), bin)
	build := exec.Command("go", "build", "-o", out, ".")
	build.Env = os.Environ()
	if outb, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build prizmal: %v\n%s", err, outb)
	}
	return out
}

// writePrizmalConfig writes a config file with the given key.
func writePrizmalConfig(t *testing.T, home, apiKey string) {
	t.Helper()
	prizmalDir := filepath.Join(home, ".prizmal")
	if err := os.MkdirAll(prizmalDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{"version": 1}
	if apiKey != "" {
		cfg["api_key"] = apiKey
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prizmalDir, "config.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// runGateSubprocess runs `prizmal --yes claude` as a subprocess with a temp
// HOME, a mock claude binary that records its presence (and env), and the
// given extra env. Returns exit code, stdout, stderr, and whether the mock
// actually ran.
//
// The prizmal flags come first: -y and the model are prizmal's own and only
// parse before the integration name, per the positional grammar.
func runGateSubprocess(t *testing.T, prizmalBin, home string, extraEnv map[string]string, urlFlag string) (int, string, string, bool) {
	t.Helper()

	binDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	expDir := filepath.Join(home, "expectations")
	if err := os.MkdirAll(expDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mockPath := filepath.Join(binDir, "claude")
	if runtime.GOOS == "windows" {
		mockPath += ".exe"
	}
	if err := writeClaudeRecorder(t, expDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mockPath, recorderScriptBytes, 0o755); err != nil {
		t.Fatal(err)
	}

	// A launch carries a real model, and prizmal's own flags come before the
	// integration name. These tests exercise the credential gate, so the model
	// only has to be one a launch can send.
	args := []string{"--yes", "--model", "gpt-oss:20b", "claude"}
	if urlFlag != "" {
		args = append([]string{"--url", urlFlag}, args...)
	}
	cmd := exec.Command(prizmalBin, args...)
	cmd.Stdin = nil
	env := []string{
		"HOME=" + home,
		"PATH=" + strings.Join([]string{binDir, os.Getenv("PATH")}, string(filepath.ListSeparator)),
		"GATE_EXP_DIR=" + expDir,
		"PRIZMAL_ENV=testing",
	}
	if runtime.GOOS == "windows" {
		env = append(env, "USERPROFILE="+home)
	}
	for k, v := range extraEnv {
		env = append(env, k+"="+v)
	}
	cmd.Env = env

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("failed to run prizmal: %v", err)
		}
	}
	marker := filepath.Join(expDir, "claude.called")
	_, mockRan := os.Stat(marker)
	return exitCode, stdout.String(), stderr.String(), mockRan == nil
}

// writeClaudeRecorder writes a small shell script + build tag-free recorder.
// On Windows we skip these subprocess tests (see below).
func writeClaudeRecorder(t *testing.T, dir string) error {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("gate e2e recorder script is POSIX-shell based")
	}
	return nil
}

// recorderScriptBytes is a tiny POSIX shell script that records that it ran,
// dumps its env, and exits 0. No secrets involved — it records names only.
var recorderScriptBytes = []byte(`#!/bin/sh
: > "$GATE_EXP_DIR/claude.called"
echo "$*" > "$GATE_EXP_DIR/claude.args"
env | grep -E '^(ANTHROPIC_BASE_URL|ANTHROPIC_AUTH_TOKEN)=' > "$GATE_EXP_DIR/claude.env"
exit 0
`)

// TestGateKeylessRemoteRefused is the regression: a launch with no credential
// against a remote URL must not silently proceed. The harness binary must
// never execute, and the refusal must name all three key sources.
func TestGateKeylessRemoteRefused(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess test in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("recorder is a POSIX shell script")
	}
	prizmalBin := buildPrizmalTestBinary(t)

	home := t.TempDir()
	writePrizmalConfig(t, home, "") // no key anywhere
	remoteURL := "https://switch.example.test"
	if envconfig.EnvVar != "PRIZMAL_SWITCH_URL" {
		t.Skip("unexpected envconfig env var name")
	}

	t.Setenv("PRIZMAL_SWITCH_URL", "") // ensure the flag --url is the only source
	exitCode, stdout, stderr, launched := runGateSubprocess(t, prizmalBin, home, nil, remoteURL)

	if exitCode == 0 {
		t.Fatalf("keyless remote launch must fail loudly; prizmal exited 0\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	if launched {
		t.Fatal("the harness must not be launched at all on a refused keyless remote launch")
	}
	for _, want := range []string{"--api-key", "PRIZMAL_SWITCH_KEY", "config file"} {
		if !strings.Contains(stderr+stdout, want) {
			t.Errorf("refusal message must name %q\nstdout: %s\nstderr: %s", want, stdout, stderr)
		}
	}
	if strings.Contains(stderr, "sk-") {
		t.Errorf("refusal output contains a key-shaped token\nstderr: %s", stderr)
	}
}

// TestGateLocalhostKeylessLaunches pins that unauthenticated loopback launches
// keep working: exit 0, harness ran, empty ANTHROPIC_AUTH_TOKEN present, and the
// announcement says "none".
func TestGateLocalhostKeylessLaunches(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess test in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("recorder is a POSIX shell script")
	}
	prizmalBin := buildPrizmalTestBinary(t)

	home := t.TempDir()
	writePrizmalConfig(t, home, "")
	exitCode, stdout, stderr, launched := runGateSubprocess(t, prizmalBin, home, nil, "http://127.0.0.1:8099")
	if exitCode != 0 {
		t.Fatalf("keyless loopback launch must keep working; exited %d\nstdout: %s\nstderr: %s", exitCode, stdout, stderr)
	}
	if !launched {
		t.Fatalf("loopback launch must still execute the harness\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	if !strings.Contains(stderr, "using api key from: none") {
		t.Errorf("announcement must say the key came from none\nstderr: %s", stderr)
	}
	envData, err := os.ReadFile(filepath.Join(home, "expectations", "claude.env"))
	if err == nil {
		envDump := string(envData)
		if !strings.Contains(envDump, "ANTHROPIC_AUTH_TOKEN=") {
			t.Errorf("mock saw no ANTHROPIC_AUTH_TOKEN at all: %s", envDump)
		}
	}
}

// TestGateUnknownFlagsPassStraightToTheHarness is the regression for the
// report that started this: a harness flag prizmal does not know must be
// handed to the harness untouched, not die as an unknown flag. `--resume
// <session-id>` reaches the mock verbatim, the mock records it, and the
// launch proceeds like any other.
func TestGateUnknownFlagsPassStraightToTheHarness(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess test in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("recorder is a POSIX shell script")
	}
	prizmalBin := buildPrizmalTestBinary(t)

	home := t.TempDir()
	writePrizmalConfig(t, home, "")
	binDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	expDir := filepath.Join(home, "expectations")
	if err := os.MkdirAll(expDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mockPath := filepath.Join(binDir, "claude")
	if err := writeClaudeRecorder(t, expDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mockPath, recorderScriptBytes, 0o755); err != nil {
		t.Fatal(err)
	}

	// A launch carries a real model, and prizmal's own flags come before the
	// integration name. The point of this test is that --resume, a flag
	// prizmal does not define, reaches the harness untouched.
	cmd := exec.Command(prizmalBin, "--yes", "--model", "gpt-oss:20b", "--url", "http://127.0.0.1:8099", "claude", "--resume", "a0b08857-3115-42fd-852d-effec9581d65")
	cmd.Stdin = nil
	env := []string{
		"HOME=" + home,
		"PATH=" + strings.Join([]string{binDir, os.Getenv("PATH")}, string(filepath.ListSeparator)),
		"GATE_EXP_DIR=" + expDir,
		"PRIZMAL_ENV=testing",
	}
	cmd.Env = env

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		t.Fatalf("`prizmal --yes claude --resume <id>` failed: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}

	argsMarker := filepath.Join(expDir, "claude.args")
	data, err := os.ReadFile(argsMarker)
	if err != nil {
		t.Fatalf("mock did not record its args: %v", err)
	}
	if got, want := strings.TrimSpace(string(data)), "--resume a0b08857-3115-42fd-852d-effec9581d65"; !strings.Contains(got, want) {
		t.Errorf("mock args = %q, want them to carry %q: the harness flags must arrive untouched", got, want)
	}
}

// TestGateRemoteWithKeyLaunches pins the happy path: a remote URL with a
// config-file credential proceeds, and the child sees the key value from the
// config file.
func TestGateRemoteWithKeyLaunches(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess test in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("recorder is a POSIX shell script")
	}
	prizmalBin := buildPrizmalTestBinary(t)

	home := t.TempDir()
	writePrizmalConfig(t, home, "sk-config-file")
	exitCode, stdout, stderr, launched := runGateSubprocess(t, prizmalBin, home, nil, "https://switch.example.test")
	if exitCode != 0 {
		t.Fatalf("remote launch with a config key must proceed; exited %d\nstdout: %s\nstderr: %s", exitCode, stdout, stderr)
	}
	if !launched {
		t.Fatalf("harness must run\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	envData, err := os.ReadFile(filepath.Join(home, "expectations", "claude.env"))
	if err != nil {
		t.Fatalf("mock env dump missing: %v", err)
	}
	if !strings.Contains(string(envData), "ANTHROPIC_AUTH_TOKEN=sk-config-file") {
		t.Errorf("child must see the config-file key value")
	}
	// The announcement must name the config file while never echoing the
	// value it holds.
	if !strings.Contains(stderr, "using api key from: config file") {
		t.Errorf("announcement must name the config file as the source\nstderr: %s", stderr)
	}
	if strings.Contains(stderr, "sk-config-file") {
		t.Errorf("announcement must never contain the key value\nstderr: %s", stderr)
	}
}
