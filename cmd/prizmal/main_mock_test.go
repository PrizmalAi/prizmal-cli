package main

import (
	"encoding/json"
	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/launcher/mockbin"
	"github.com/PrizmalAi/prizmal-cli/internal/stubserver"

	"github.com/PrizmalAi/prizmal-cli/internal/launcher/registry"
)

// mockHarnessTest runs `prizmal <harness>` as a subprocess with a temp HOME
// and a PATH where every harness binary is a Go-built mock that validates
// its env and args. The mock exits 0 when expectations pass, 1 with a report
// on stderr when they fail. No Docker, no shell, no symlinks: the mock is a
// Go binary compiled from internal/launcher/mockbin and copied (not
// symlinked) as each harness binary name, so it runs on every OS including
// Windows CI.

const (
	// mockBinPkg is the package the mock harness binary is built from.
	mockBinPkg = "github.com/PrizmalAi/prizmal-cli/internal/launcher/mockbin/cmd/mockbin"
)

// harnessExpectation describes what the mock for a given harness must see.
type harnessExpectation struct {
	// name is the prizmal integration name (e.g. "claude", "codex").
	name string
	// binaryNames are the names the mock binary is copied as on the temp PATH.
	binaryNames []string
	// requiredEnv are env vars that must be set and non-empty.
	requiredEnv []string
	// forbiddenEnv are env vars that must NOT be set.
	forbiddenEnv []string
	// requiredArgs are args that must appear in the harness command line.
	requiredArgs []string
	// skipReason, if non-empty, skips this harness. skipWindows, if true,
	// skips this harness only on Windows (product platform gates). Both
	// kinds of skip do not count toward the zero-ran
	// guard's executed count; the guard only fails when NOTHING ran.
	skipReason string
	// windowsOnlySkip marks a skip that applies on Windows only
	// (skipWindows). skipOn, if non-nil, is evaluated at test time and its
	// non-empty result skips this harness — for product gates that depend
	// on the host OS or environment rather than being unconditional.
	skipWindows bool
	skipOn      func() string
	// alsoMockNpm — if true, copy the mock as "npm" too (for harnesses
	// that check npm on PATH during install discovery).
	alsoMockNpm bool
	// alsoMock — extra binary names to mock beyond binaryNames.
	alsoMock []string
	// stubRequest, when non-nil, makes the mock send one authenticated
	// request to the stub server (Change 2: observed HTTP effect).
	stubRequest *mockbin.StubRequest
}

// mockLaunchModel is the model these launches name, and the one the stub
// server's own /v1/models serves. Naming a model the catalog lists keeps the
// launch's capability lookup on the normal path.
const mockLaunchModel = "prizmal/stub"

func allHarnessExpectations() []harnessExpectation {
	return []harnessExpectation{
		{
			name:         "claude",
			binaryNames:  []string{"claude"},
			requiredEnv:  []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN"},
			forbiddenEnv: []string{"ANTHROPIC_API_KEY"},
		},
		{
			name:        "codex",
			binaryNames: []string{"codex"},
			requiredEnv: []string{"OPENAI_API_KEY"},
		},
		{
			name:         "opencode",
			binaryNames:  []string{"opencode"},
			requiredEnv:  []string{"OPENCODE_CONFIG_CONTENT"},
			requiredArgs: []string{"--model", "prizmal/" + mockLaunchModel},
		},
		{
			name:        "pi",
			binaryNames: []string{"pi"},
			alsoMockNpm: true,
			// pi reads the key from $PRIZMAL_SWITCH_KEY, which the apiKey
			// in ~/.pi/agent/models.json names. The test env sets no key
			// variable, so only prizmal can have put it there. pi only
			// honors settings.json's defaultProvider when the provider
			// entry passes its schema validation, so the launch itself
			// must select the provider explicitly (pi_launch_args).
			requiredEnv:  []string{"PRIZMAL_SWITCH_KEY"},
			requiredArgs: []string{"--provider", "prizmal", "--model", mockLaunchModel},
		},
		{
			name:        "cline",
			binaryNames: []string{"cline"},
			alsoMockNpm: true,
			// cline's openai-compatible provider reads OPENAI_API_KEY when
			// ~/.cline/data/settings/providers.json carries no apiKey.
			requiredEnv: []string{"OPENAI_API_KEY"},
		},
	}
}

// buildMockBinary compiles the Go mock harness once per test binary run and
// returns its path. The mock is a real executable, so it works identically
// on darwin, linux, and windows (where GOOS adds .exe via GOEXE).
func buildMockBinary(t *testing.T) string {
	t.Helper()
	binName := "mockbin-harness"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	out := filepath.Join(t.TempDir(), binName)
	build := exec.Command("go", "build", "-o", out, mockBinPkg)
	build.Env = os.Environ()
	if outBytes, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build mock harness: %v\n%s", err, outBytes)
	}
	return out
}

// setupMockEnv creates a temp HOME, temp PATH with mock harness binaries, a
// live stub server, and the mock's expectation file. Returns the home dir,
// the expectations dir, and the stub server URL.
func setupMockEnv(t *testing.T, buildMock func() string, exp harnessExpectation) (home, expDir, stubURL string) {
	t.Helper()

	// A live stub server stands in for the Switch. The mock binary (Change
	// 2) makes one authenticated request to it using the env prizmal handed
	// it, so the test observes a real HTTP exchange — values prizmal emits
	// accepted by a real HTTP server that checks them — not just env
	// construction.
	srv := stubserver.New()
	t.Cleanup(srv.Close)
	stubURL = srv.URL

	home = t.TempDir()
	binDir := filepath.Join(home, "bin")
	expDir = filepath.Join(home, "expectations")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(expDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Copy (never symlink: os.Symlink on Windows needs developer mode or an
	// elevated shell) the mock binary as each harness binary name.
	mockPath := buildMock()
	allMocks := append([]string{}, exp.binaryNames...)
	if exp.alsoMockNpm {
		allMocks = append(allMocks, "npm")
	}
	allMocks = append(allMocks, exp.alsoMock...)
	for _, bin := range allMocks {
		name := bin
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		data, err := os.ReadFile(mockPath)
		if err != nil {
			t.Fatalf("read mock binary: %v", err)
		}
		if err := os.WriteFile(filepath.Join(binDir, name), data, 0o755); err != nil {
			t.Fatalf("copy mock as %s: %v", bin, err)
		}
	}

	// Write the prizmal config file with the test key + URL. This is the
	// config-file path a real user's machine relies on; no
	// PRIZMAL_SWITCH_URL/KEY is set in the child env, so this file is the
	// only source of the credentials prizmal will emit.
	prizmalDir := filepath.Join(home, ".prizmal")
	if err := os.MkdirAll(prizmalDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{
		"version":  1,
		"base_url": stubURL,
		"api_key":  stubserver.StubKey,
		// A launch with no --model reads this saved default, so the subprocess
		// launches below never need a terminal to pick one.
		"default_model": mockLaunchModel,
	}
	cfgData, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prizmalDir, "config.json"), cfgData, 0o600); err != nil {
		t.Fatal(err)
	}

	// Write the expectation file the mock binary reads.
	expData, err := json.Marshal(mockbin.Expectations{
		RequiredEnv:  exp.requiredEnv,
		ForbiddenEnv: exp.forbiddenEnv,
		RequiredArgs: exp.requiredArgs,
		StubRequest:  exp.stubRequest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(expDir, "mock.json"), expData, 0o644); err != nil {
		t.Fatal(err)
	}

	return home, expDir, stubURL
}

// runPrizmalSubprocess runs the prizmal binary as a subprocess with the
// given env and returns its exit code and output. The launch names its model
// explicitly, so it does not depend on the saved default the temp config holds.
func runPrizmalSubprocess(t *testing.T, prizmalBin, home, expDir, harness string) (int, string, string) {
	t.Helper()
	return runPrizmalSubprocessArgs(t, prizmalBin, home, expDir, harness, "--yes", "--model", mockLaunchModel)
}

// runPrizmalSubprocessArgs is runPrizmalSubprocess with the prizmal flags
// spelled by the caller, for a launch that must not name a model. The flags
// are prizmal's own and parse only before the integration name, per the
// positional grammar, so they come first.
func runPrizmalSubprocessArgs(t *testing.T, prizmalBin, home, expDir, harness string, flags ...string) (int, string, string) {
	t.Helper()
	return runPrizmalLaunch(t, prizmalBin, home, expDir, harness, flags, nil)
}

// mockLaunchExtraEnv is added to the environment of the next launches a test
// runs. A test sets it with setMockLaunchEnv, which clears it afterwards.
var mockLaunchExtraEnv []string

func setMockLaunchEnv(t *testing.T, env ...string) {
	t.Helper()
	mockLaunchExtraEnv = env
	t.Cleanup(func() { mockLaunchExtraEnv = nil })
}

// runPrizmalLaunch is runPrizmalSubprocessArgs with a separate harnessArgs
// list that is passed after the integration name, exercising the passthrough
// half of the positional grammar.
func runPrizmalLaunch(t *testing.T, prizmalBin, home, expDir, harness string, prizmalFlags, harnessArgs []string) (int, string, string) {
	t.Helper()

	binDir := filepath.Join(home, "bin")
	args := append([]string{}, prizmalFlags...)
	args = append(args, harness)
	args = append(args, harnessArgs...)
	cmd := exec.Command(prizmalBin, args...)
	cmd.Stdin = nil

	// Minimal clean env. PATH is joined with the OS list separator — the
	// PowerShell-is-not-bash trap: ":" is only correct on unix.
	env := []string{
		"HOME=" + home,
		"PATH=" + strings.Join([]string{binDir, os.Getenv("PATH")}, string(filepath.ListSeparator)),
		"MOCK_EXPECTATIONS_DIR=" + expDir,
		"PRIZMAL_ENV=testing",
	}
	if runtime.GOOS == "windows" {
		env = append(env, "USERPROFILE="+home)
	}
	env = append(env, mockLaunchExtraEnv...)
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
	return exitCode, stdout.String(), stderr.String()
}

// TestMockHarnessLaunch runs each harness through prizmal with mock binaries
// on PATH and validates the mock's env/args. The zero-ran guard at the end
// is the primary defence against a silently-skipped Windows leg: nothing at
// the platform level can back it up (this private repo cannot have branch
// protection / required status checks), so if every harness subtest is
// skipped — exactly the pre-fix Windows state — the parent test itself must
// fail rather than pass green with zero executed harness tests.
//
// NOTE for whoever edits the CI test command: this test is deliberately
// excluded from `-short`. If `go test -short` is ever added to ci.yml, the
// subprocess tests below skip and this guard goes quiet in exactly the way
// the pre-PR-2540 Windows leg was quiet — do not add `-short` to the CI
// test step without replacing this guard.
func TestMockHarnessLaunch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess test in short mode")
	}

	// Build the prizmal binary once for the whole suite.
	prizmalBin := filepath.Join(t.TempDir(), "prizmal")
	if runtime.GOOS == "windows" {
		prizmalBin += ".exe"
	}
	build := exec.Command("go", "build", "-o", prizmalBin, ".")
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build prizmal: %v\n%s", err, out)
	}

	// Build the Go mock harness binary once (it is copied under each
	// harness name by setupMockEnv).
	mockBin := buildMockBinary(t)
	buildMock := func() string { return mockBin }

	ranCount := 0
	for _, exp := range allHarnessExpectations() {
		exp := exp
		if exp.skipReason != "" || (exp.skipWindows && runtime.GOOS == "windows") {
			reason := exp.skipReason
			if reason == "" {
				reason = "not supported on this platform (product gate)"
			}
			t.Run(exp.name, func(t *testing.T) {
				t.Skip(reason)
			})
			continue
		}
		if exp.skipOn != nil {
			if reason := exp.skipOn(); reason != "" {
				t.Run(exp.name, func(t *testing.T) {
					t.Skip(reason)
				})
				continue
			}
		}
		t.Run(exp.name, func(t *testing.T) {
			ranCount++

			home, expDir, _ := setupMockEnv(t, buildMock, exp)

			exitCode, stdout, stderr := runPrizmalSubprocess(t, prizmalBin, home, expDir, exp.name)

			if exitCode != 0 {
				t.Fatalf("prizmal %s exited %d\nstdout: %s\nstderr: %s", exp.name, exitCode, stdout, stderr)
			}

			// Verify the mock was actually called (marker file exists) and
			// saw the expected env (observed effect, not the command prizmal
			// constructed).
			if len(exp.binaryNames) > 0 {
				marker := filepath.Join(expDir, exp.binaryNames[0]+".called.args")
				if _, err := os.Stat(marker); err != nil {
					t.Errorf("mock binary %q was not invoked\nstdout: %s\nstderr: %s",
						exp.binaryNames[0], stdout, stderr)
				}
				envFile := filepath.Join(expDir, exp.binaryNames[0]+".called.env")
				if data, err := os.ReadFile(envFile); err == nil {
					envDump := string(data)
					for _, envVar := range exp.requiredEnv {
						if !envDumpHasNonEmpty(envDump, envVar) {
							t.Errorf("mock did not see non-empty env %s\nenv dump:\n%s", envVar, internaltest.RedactEnvDump(envDump))
						}
					}
				} else {
					t.Errorf("mock env dump missing: %v", err)
				}
			}
		})
	}

	// Zero-ran guard: at least one harness subtest must have actually
	// executed. If every harness test is skipped — exactly the pre-fix
	// Windows state — the parent fails here rather than passing green with
	// zero executed harness tests. t.Skip inside a subtest cannot veto this:
	// the guard reads ranCount after the loop, not subtest pass/fail state.
	if ranCount == 0 {
		t.Fatalf("zero-ran guard: 0 of %d harness subtests executed; "+
			"a green run here proves nothing about harness launching", len(allHarnessExpectations()))
	}
}

// envDumpValue returns the value var is set to in the mock's env dump, or
// "" when unset or empty.
func envDumpValue(envDump, envVar string) string {
	for _, line := range strings.Split(envDump, "\n") {
		if after, ok := strings.CutPrefix(line, envVar+"="); ok {
			return after
		}
	}
	return ""
}

// envDumpHasNonEmpty reports whether the mock's env dump sets var to a
// non-empty value.
func envDumpHasNonEmpty(envDump, envVar string) bool {
	for _, line := range strings.Split(envDump, "\n") {
		if after, ok := strings.CutPrefix(line, envVar+"="); ok && after != "" {
			return true
		}
	}
	return false
}

// TestClaudeCodeE2EWithStubRequest launches Claude Code via the prizmal
// subprocess with the mock standing in for the real Claude Code binary, and
// verifies the full chain end to end: the values prizmal emits (base URL
// from the config file, key from the config file) are accepted by a real
// HTTP server that checks them. The mock binary makes one authenticated
// request to the stub server (Anthropic /v1/messages dialect) using exactly
// the env prizmal put in its environment, so a non-200 from the stub means
// prizmal emitted wrong values. Observed effect, not the command prizmal
// constructed: a test that checks a command string would have passed right
// through the Windows pwsh-vs-powershell env-injection bug.
func TestClaudeCodeE2EWithStubRequest(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess test in short mode")
	}

	prizmalBin := filepath.Join(t.TempDir(), "prizmal")
	if runtime.GOOS == "windows" {
		prizmalBin += ".exe"
	}
	build := exec.Command("go", "build", "-o", prizmalBin, ".")
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build prizmal: %v\n%s", err, out)
	}

	mockBin := buildMockBinary(t)
	buildMock := func() string { return mockBin }

	exp := harnessExpectation{
		name:         "claude",
		binaryNames:  []string{"claude"},
		requiredEnv:  []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN"},
		forbiddenEnv: []string{"ANTHROPIC_API_KEY"},
		stubRequest: &mockbin.StubRequest{
			BaseURLEnv: "ANTHROPIC_BASE_URL",
			KeyEnv:     "ANTHROPIC_AUTH_TOKEN",
			Path:       "/v1/messages",
			// The model rides the inline --settings JSON rather than an env
			// var, so the mock reads it from the command line. This is what
			// makes the observed wire model the launch's actual model.
			ModelFromSettings: true,
		},
	}

	home, expDir, stubURL := setupMockEnv(t, buildMock, exp)

	exitCode, stdout, stderr := runPrizmalSubprocess(t, prizmalBin, home, expDir, exp.name)

	if exitCode != 0 {
		t.Fatalf("prizmal claude exited %d\nstdout: %s\nstderr: %s", exitCode, stdout, stderr)
	}

	// Observed effect 1: the mock ran.
	argsMarker := filepath.Join(expDir, "claude.called.args")
	argsData, err := os.ReadFile(argsMarker)
	if err != nil {
		t.Fatalf("claude mock not invoked: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}
	if strings.TrimSpace(string(argsData)) == "" {
		t.Fatalf("claude mock invoked with empty args\nstdout: %s\nstderr: %s", stdout, stderr)
	}

	// Observed effect 2: the model is named in an inline --settings JSON
	// argument. The env dump below checks the environment: the Opus tier
	// variable carries the launch model, and the other tier variables must be
	// absent, not merely empty.
	argsLine := string(argsData)
	if !strings.Contains(argsLine, "--settings") {
		t.Errorf("claude args carry no --settings; the model and picker rows are defined there:\n%s", argsLine)
	}
	if !strings.Contains(argsLine, mockLaunchModel) {
		t.Errorf("claude args do not name the launch model %s:\n%s", mockLaunchModel, argsLine)
	}

	// Observed effect 3: the env prizmal emitted carries the stub server's URL
	// and key from the config file, and the launch model in the Opus tier
	// variable only. The key rides
	// on the gateway token alone; the mock's forbiddenEnv check above already
	// failed the launch if ANTHROPIC_API_KEY carried a value.
	envFile := filepath.Join(expDir, "claude.called.env")
	envData, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("claude env dump missing: %v", err)
	}
	envDump := string(envData)
	for _, check := range []struct{ name, want string }{
		{"ANTHROPIC_BASE_URL", stubURL},
		{"ANTHROPIC_AUTH_TOKEN", stubserver.StubKey},
	} {
		if !envDumpHasNonEmpty(envDump, check.name) {
			t.Errorf("claude env missing %s\nenv dump:\n%s", check.name, internaltest.RedactEnvDump(envDump))
			continue
		}
		if got := envDumpValue(envDump, check.name); got != check.want {
			t.Errorf("claude env %s = %q, want %q", check.name, got, check.want)
		}
	}
	if got := envDumpValue(envDump, "ANTHROPIC_DEFAULT_OPUS_MODEL"); got != mockLaunchModel+"[1m]" {
		t.Errorf("claude env ANTHROPIC_DEFAULT_OPUS_MODEL = %q, want %q; the /model Default row reads it\nenv dump:\n%s", got, mockLaunchModel+"[1m]", internaltest.RedactEnvDump(envDump))
	}
	// The stub tenant holds no Claude tier, so every tier word follows the
	// launch model: a vendor id for it would be a name the Switch refuses.
	for _, name := range []string{
		"ANTHROPIC_DEFAULT_SONNET_MODEL",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL",
		"ANTHROPIC_DEFAULT_FABLE_MODEL",
	} {
		if got := envDumpValue(envDump, name); got != mockLaunchModel+"[1m]" {
			t.Errorf("claude env %s = %q, want the launch model %q, because the tenant holds no such tier\nenv dump:\n%s", name, got, mockLaunchModel+"[1m]", internaltest.RedactEnvDump(envDump))
		}
	}
	if strings.Contains(envDump, "CLAUDE_CODE_SUBAGENT_MODEL=") {
		t.Errorf("claude env carries CLAUDE_CODE_SUBAGENT_MODEL, and the launch picked no subagent model\nenv dump:\n%s", internaltest.RedactEnvDump(envDump))
	}

	// Observed effect 3: the stub server accepted the mock's authenticated
	// request. If the mock had received a wrong URL or key, the request
	// would have 401'd (or failed outright) and the mock would have
	// reported "stub request:" on stderr with a non-zero exit — caught
	// above by the exitCode check.
	// Observed effect 3 is implicit: if the mock had received a wrong URL
	// or key from prizmal, its stub request would have 401'd or failed
	// outright, and the mock's non-zero exit would have failed the
	// exitCode check above.
}
func TestMockHarnessLaunchFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess test in short mode")
	}

	prizmalBin := filepath.Join(t.TempDir(), "prizmal")
	if runtime.GOOS == "windows" {
		prizmalBin += ".exe"
	}
	build := exec.Command("go", "build", "-o", prizmalBin, ".")
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build prizmal: %v\n%s", err, out)
	}

	// Claude requires ANTHROPIC_API_KEY; we set a bogus expectation for
	// an env var that prizmal doesn't set, so the mock must fail.
	exp := harnessExpectation{
		name:        "claude",
		binaryNames: []string{"claude"},
		requiredEnv: []string{"NONEXISTENT_VAR_THAT_PRIZMAL_NEVER_SETS"},
	}

	buildMock := func() string { return buildMockBinary(t) }

	home, expDir, _ := setupMockEnv(t, buildMock, exp)

	exitCode, _, stderr := runPrizmalSubprocess(t, prizmalBin, home, expDir, exp.name)

	// The mock should have been called and should have failed.
	if exitCode == 0 {
		t.Fatalf("expected prizmal to exit non-zero (mock validation failure), but exited 0")
	}
	if !strings.Contains(stderr, "FAILED") && !strings.Contains(stderr, "missing env") {
		t.Fatalf("expected mock failure in stderr, got: %s", stderr)
	}
}

// TestMockHarnessUnknownFlagsPassStraightThrough runs every harness with a
// harness-only flag that prizmal does not define, positioned after the
// integration name. Before the positional grammar every harness leg would
// die on "unknown flag", which is the regression this test pins.
func TestMockHarnessUnknownFlagsPassStraightThrough(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess test in short mode")
	}

	prizmalBin := filepath.Join(t.TempDir(), "prizmal")
	if runtime.GOOS == "windows" {
		prizmalBin += ".exe"
	}
	build := exec.Command("go", "build", "-o", prizmalBin, ".")
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build prizmal: %v\n%s", err, out)
	}

	mockBin := buildMockBinary(t)
	buildMock := func() string { return mockBin }

	ran := 0
	for _, exp := range allHarnessExpectations() {
		if exp.skipReason != "" || (exp.skipWindows && runtime.GOOS == "windows") {
			continue
		}
		t.Run(exp.name, func(t *testing.T) {
			ran++
			home, expDir, _ := setupMockEnv(t, buildMock, exp)
			// A harness-only flag, unknown to prizmal, after the integration
			// name. Claude Code's --resume takes a session id, which is the
			// shape of the report (the passthrough must carry the value too).
			exitCode, stdout, stderr := runPrizmalLaunch(t, prizmalBin, home, expDir, exp.name, []string{"--yes"}, []string{"--resume", "session-gh4"})
			if exitCode != 0 {
				t.Fatalf("prizmal %s with passthrough --resume exited %d\nstdout: %s\nstderr: %s", exp.name, exitCode, stdout, stderr)
			}
			marker := filepath.Join(expDir, exp.binaryNames[0]+".called.args")
			data, err := os.ReadFile(marker)
			if err != nil {
				t.Fatalf("mock %q not invoked: %v\nstdout: %s\nstderr: %s", exp.name, err, stdout, stderr)
			}
			if !strings.Contains(string(data), "--resume session-gh4") {
				t.Errorf("mock args = %q, want them to carry --resume session-gh4", string(data))
			}
		})
	}
	if ran == 0 {
		t.Fatal("zero-ran guard: no harness subtest executed")
	}
}

// TestMockHarnessRegistryCompleteness enumerates every integration in the
// registry (the source of truth) and fails when one has neither a mock-harness
// case nor a documented skip, so a new harness cannot ship untested.
func TestMockHarnessRegistryCompleteness(t *testing.T) {
	covered := make(map[string]bool)
	for _, exp := range allHarnessExpectations() {
		covered[exp.name] = true
	}

	specs := registry.ListAllIntegrationSpecs()
	if len(specs) == 0 {
		t.Fatal("registry returned no integrations")
	}

	for _, spec := range specs {
		if covered[spec.Name] {
			continue
		}
		t.Errorf("integration %q has no mock-harness case in allHarnessExpectations()", spec.Name)
	}

	// Also flag table entries that no longer match any registry integration.
	registry := make(map[string]bool, len(specs))
	for _, spec := range specs {
		registry[spec.Name] = true
	}
	for _, exp := range allHarnessExpectations() {
		if !registry[exp.name] {
			t.Errorf("mock-harness case %q has no matching registry integration (stale entry?)", exp.name)
		}
	}
}

// TestMockHarnessBareCodexLaunchUsesTheSavedDefault pins the precedence on a
// launch that names no model. Codex has no model of its own to fall back on,
// and the reserved prizmal/default name is sunset, so a bare `prizmal codex`
// must resolve the model the config saved and hand the child that model on the
// command line.
func TestMockHarnessBareCodexLaunchUsesTheSavedDefault(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess test in short mode")
	}

	prizmalBin := filepath.Join(t.TempDir(), "prizmal")
	if runtime.GOOS == "windows" {
		prizmalBin += ".exe"
	}
	build := exec.Command("go", "build", "-o", prizmalBin, ".")
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build prizmal: %v\n%s", err, out)
	}

	mockBin := buildMockBinary(t)
	buildMock := func() string { return mockBin }

	exp := harnessExpectation{
		name:         "codex",
		binaryNames:  []string{"codex"},
		requiredEnv:  []string{"OPENAI_API_KEY"},
		requiredArgs: []string{"-m", mockLaunchModel},
	}
	home, expDir, _ := setupMockEnv(t, buildMock, exp)

	exitCode, stdout, stderr := runPrizmalSubprocessArgs(t, prizmalBin, home, expDir, exp.name, "--yes")
	if exitCode != 0 {
		t.Fatalf("bare prizmal codex exited %d\nstdout: %s\nstderr: %s", exitCode, stdout, stderr)
	}

	argsFile := filepath.Join(expDir, "codex.called.args")
	argsData, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("codex mock not invoked: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}
	if !strings.Contains(string(argsData), "-m "+mockLaunchModel) {
		t.Errorf("codex args = %q, want them to carry -m %s from the saved default", string(argsData), mockLaunchModel)
	}

	if _, err := os.Stat(filepath.Join(home, ".codex", "prizmal.config.toml")); !os.IsNotExist(err) {
		t.Errorf("the launch left a profile under ~/.codex (stat err = %v)", err)
	}
}

// TestMockHarnessBareCodexLaunchFailsWithoutAModel is the other half of the
// precedence: with no --model and no saved default, a non-interactive launch
// stops and names the ways out. It must never reach the switch with the sunset
// prizmal/default name.
func TestMockHarnessBareCodexLaunchFailsWithoutAModel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess test in short mode")
	}

	prizmalBin := filepath.Join(t.TempDir(), "prizmal")
	if runtime.GOOS == "windows" {
		prizmalBin += ".exe"
	}
	build := exec.Command("go", "build", "-o", prizmalBin, ".")
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build prizmal: %v\n%s", err, out)
	}

	mockBin := buildMockBinary(t)
	buildMock := func() string { return mockBin }

	exp := harnessExpectation{
		name:        "codex",
		binaryNames: []string{"codex"},
	}
	home, expDir, _ := setupMockEnv(t, buildMock, exp)

	// Drop the saved default the shared setup writes, so nothing names a model
	// while the subprocess has no terminal to pick one on.
	cfgPath := filepath.Join(home, ".prizmal", "config.json")
	cfgData, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(cfgData, &cfg); err != nil {
		t.Fatal(err)
	}
	delete(cfg, "default_model")
	cfgData, err = json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, cfgData, 0o600); err != nil {
		t.Fatal(err)
	}

	exitCode, stdout, stderr := runPrizmalSubprocessArgs(t, prizmalBin, home, expDir, exp.name, "--yes")
	if exitCode == 0 {
		t.Fatalf("a launch with no model and no terminal must fail\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	if strings.Contains(stdout+stderr, "prizmal/default") {
		t.Errorf("the refusal names the sunset prizmal/default model:\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	for _, want := range []string{"--model", "--pick", "default_model"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("refusal does not name %q: %s", want, stderr)
		}
	}
	// The launch asks Codex for its version before anything else, so the mock
	// has been called once. A launch would be the last call, with more than
	// the version flag.
	if called, err := os.ReadFile(filepath.Join(expDir, "codex.called.args")); err == nil && strings.TrimSpace(string(called)) != "--version" {
		t.Errorf("codex was launched despite there being no model to launch it with (args %q)", called)
	}
}

// TestMockHarnessCodexLaunchPassesTheSubagentModel pins that --subagent-model
// reaches Codex. Codex has no environment variable for it, so the launch hands
// the child a -c override of agents.default_subagent_model, and lists the model
// in the catalog Codex checks it against.
func TestMockHarnessCodexLaunchPassesTheSubagentModel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess test in short mode")
	}

	prizmalBin := filepath.Join(t.TempDir(), "prizmal")
	if runtime.GOOS == "windows" {
		prizmalBin += ".exe"
	}
	build := exec.Command("go", "build", "-o", prizmalBin, ".")
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build prizmal: %v\n%s", err, out)
	}

	mockBin := buildMockBinary(t)
	exp := harnessExpectation{
		name:         "codex",
		binaryNames:  []string{"codex"},
		requiredEnv:  []string{"OPENAI_API_KEY"},
		requiredArgs: []string{"-m", mockLaunchModel},
	}
	home, expDir, _ := setupMockEnv(t, func() string { return mockBin }, exp)

	const subagent = "prizmal/stub-vision"
	exitCode, stdout, stderr := runPrizmalSubprocessArgs(t, prizmalBin, home, expDir, exp.name, "--yes", "--subagent-model", subagent)
	if exitCode != 0 {
		t.Fatalf("prizmal codex --subagent-model exited %d\nstdout: %s\nstderr: %s", exitCode, stdout, stderr)
	}

	argsData, err := os.ReadFile(filepath.Join(expDir, "codex.called.args"))
	if err != nil {
		t.Fatalf("codex mock not invoked: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}
	want := `agents.default_subagent_model="` + subagent + `"`
	if !strings.Contains(string(argsData), want) {
		t.Errorf("codex args = %q, want them to carry -c %s", string(argsData), want)
	}
}
