package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// buildPrizmalForMock builds the prizmal binary the mock-harness tests launch.
func buildPrizmalForMock(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping subprocess test in short mode")
	}
	bin := filepath.Join(t.TempDir(), "prizmal")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build prizmal: %v\n%s", err, out)
	}
	return bin
}

// codexMockLaunch runs `prizmal codex <args>` against the mock Codex and returns
// the arguments the mock saw, or the failure.
func codexMockLaunch(t *testing.T, prizmalArgs, harnessArgs []string) (calledArgs string, exitCode int, stdout, stderr string) {
	t.Helper()
	prizmalBin := buildPrizmalForMock(t)
	mockBin := buildMockBinary(t)
	exp := harnessExpectation{name: "codex", binaryNames: []string{"codex"}, requiredEnv: []string{"OPENAI_API_KEY"}}
	home, expDir, _ := setupMockEnv(t, func() string { return mockBin }, exp)

	exitCode, stdout, stderr = runPrizmalLaunch(t, prizmalBin, home, expDir, "codex", append([]string{"--yes"}, prizmalArgs...), harnessArgs)
	data, _ := os.ReadFile(filepath.Join(expDir, "codex.called.args"))
	return string(data), exitCode, stdout, stderr
}

// `prizmal codex --model X` works the way `prizmal claude --model X` does: the
// CLI takes the flag as its own model choice and hands Codex one -m.
func TestMockHarnessCodexTakesTheModelFlagAfterTheIntegrationName(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"--model", []string{"--model", "prizmal-flash"}},
		{"--model=", []string{"--model=prizmal-flash"}},
		{"-m", []string{"-m", "prizmal-flash"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called, code, stdout, stderr := codexMockLaunch(t, nil, tc.args)
			if code != 0 {
				t.Fatalf("prizmal codex %v exited %d\nstdout: %s\nstderr: %s", tc.args, code, stdout, stderr)
			}
			if got := strings.Count(called, " -m "); got != 1 || !strings.Contains(called, "-m prizmal-flash") {
				t.Errorf("codex args = %q, want exactly one -m prizmal-flash", called)
			}
			if strings.Contains(called, "--model") {
				t.Errorf("codex args = %q still carry the --model the CLI took", called)
			}
		})
	}
}

// Text after a `--` separator belongs to Codex, such as a prompt that mentions
// the flag, and prizmal neither takes it as the model nor refuses it.
func TestMockHarnessCodexLeavesTextAfterTheSeparatorAlone(t *testing.T) {
	called, code, stdout, stderr := codexMockLaunch(t, nil, []string{"exec", "--", "--model is a word"})
	if code != 0 {
		t.Fatalf("prizmal codex exec -- \"--model is a word\" exited %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(called, "--model is a word") {
		t.Errorf("codex args = %q, want the text after the separator passed through", called)
	}
	if strings.Contains(called, "-m is") {
		t.Errorf("codex args = %q took the text as a model", called)
	}
}

// Two different models are one question answered twice, and the launch stops.
func TestMockHarnessCodexRefusesTwoDifferentModels(t *testing.T) {
	_, code, _, stderr := codexMockLaunch(t, []string{"--model", "prizmal-core"}, []string{"-m", "prizmal-flash"})
	if code == 0 || !strings.Contains(stderr, "conflicting --model") {
		t.Fatalf("exit %d, stderr %q, want a conflicting --model error", code, stderr)
	}
}

// An old Codex stops the launch before the first-run sign-in, not after it.
func TestMockHarnessOldCodexStopsBeforeSignIn(t *testing.T) {
	setMockLaunchEnv(t, "MOCKBIN_CODEX_VERSION=0.134.0")
	prizmalBin := buildPrizmalForMock(t)
	mockBin := buildMockBinary(t)
	exp := harnessExpectation{name: "codex", binaryNames: []string{"codex"}}
	home, expDir, _ := setupMockEnv(t, func() string { return mockBin }, exp)
	// A machine that has never signed in: no config file at all.
	_ = os.Remove(filepath.Join(home, ".prizmal", "config.json"))

	code, _, stderr := runPrizmalLaunch(t, prizmalBin, home, expDir, "codex", []string{"--yes"}, nil)
	if code == 0 || !strings.Contains(stderr, "too old") || !strings.Contains(stderr, "0.160.0") {
		t.Fatalf("exit %d, stderr %q, want a too-old error naming 0.160.0", code, stderr)
	}
	if strings.Contains(strings.ToLower(stderr), "sign in") || strings.Contains(strings.ToLower(stderr), "sign-in") {
		t.Errorf("the launch got to the sign-in before it checked Codex: %q", stderr)
	}
}
