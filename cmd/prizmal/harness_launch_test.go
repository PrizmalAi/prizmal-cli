//go:build !windows

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PrizmalAi/prizmal-cli/internal/device"
	launcher "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/PrizmalAi/prizmal-cli/internal/stubserver"
)

// Harness launch tests run each real coding-agent harness through prizmal
// with a one-shot prompt, against a stub switch in place of the Prizmal
// Switch. The stub answers every turn with stubserver.Reply, and the test
// passes when that reply reaches the harness's stdout. It proves that prizmal
// pointed the harness at the switch with a key it accepted, and that the
// harness parsed the switch's answer, without a live key or a billed token.
//
// Exit codes are not the pass signal: opencode exits 0 on its own internal
// errors, so every test asserts on stdout.
//
// A test skips when its harness is not on PATH. Each harness's workflow
// installs it and sets PRIZMAL_HARNESS_LAUNCH=require, which turns the skip
// into a failure.
//
// Each harness keeps its own launch tests in its own file, named after it
// (claude_launch_test.go, codex_launch_test.go, and the rest). This file holds
// what they share: the launch cases, the sandbox, and the runner.

const (
	harnessLaunchRequireEnv = "PRIZMAL_HARNESS_LAUNCH"
	harnessLaunchTimeout    = 3 * time.Minute
	harnessLaunchModel      = "smart"
	harnessLaunchPrompt     = "Reply with a short greeting."
)

// harnessLaunchCase is one harness's launch: the arguments passed after the
// integration name, and the workflow that runs its test in CI.
type harnessLaunchCase struct {
	test     string
	workflow string
	args     []string
}

// harnessLaunchCases has one entry per integration in the launcher registry.
// TestHarnessLaunchRegistryCompleteness fails when one is missing, so a new
// harness cannot be added without a launch in CI.
var harnessLaunchCases = map[string]harnessLaunchCase{
	"claude": {
		test: "TestHarnessLaunchClaude", workflow: "claude-code.yml",
		args: []string{"--print", harnessLaunchPrompt},
	},
	"codex": {
		test: "TestHarnessLaunchCodex", workflow: "codex.yml",
		args: []string{"exec", "--skip-git-repo-check", "-s", "read-only", harnessLaunchPrompt},
	},
	"cline": {
		test: "TestHarnessLaunchCline", workflow: "cline.yml",
		args: []string{harnessLaunchPrompt},
	},
	// The caller includes opencode's `run` subcommand, which the launcher
	// does not insert.
	"opencode": {
		test: "TestHarnessLaunchOpencode", workflow: "opencode.yml",
		args: []string{"run", harnessLaunchPrompt},
	},
	"pi": {
		test: "TestHarnessLaunchPi", workflow: "pi.yml",
		args: []string{"--print", harnessLaunchPrompt},
	},
}

// TestHarnessLaunchRegistryCompleteness checks that every integration in the
// registry has a launch case, and that the case's workflow runs its test with
// the harness required, so CI cannot pass by skipping it.
func TestHarnessLaunchRegistryCompleteness(t *testing.T) {
	specs := launcher.ListAllIntegrationSpecs()
	if len(specs) == 0 {
		t.Fatal("registry returned no integrations")
	}
	registry := make(map[string]bool, len(specs))
	for _, spec := range specs {
		registry[spec.Name] = true
		tc, ok := harnessLaunchCases[spec.Name]
		if !ok {
			t.Errorf("integration %q has no entry in harnessLaunchCases", spec.Name)
			continue
		}
		path := filepath.Join("..", "..", ".github", "workflows", tc.workflow)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("integration %q: %v", spec.Name, err)
			continue
		}
		for _, want := range []string{"-run '" + tc.test + "$'", harnessLaunchRequireEnv + ": require"} {
			if !strings.Contains(string(raw), want) {
				t.Errorf("%s does not contain %q", path, want)
			}
		}
	}
	for name := range harnessLaunchCases {
		if !registry[name] {
			t.Errorf("harnessLaunchCases entry %q has no matching registry integration", name)
		}
	}
}

// harnessLaunchKeyMode says what credential state a launch case sets up.
type harnessLaunchKeyMode int

const (
	// harnessLaunchConfigKey enrolls no device: the launch runs on the switch
	// key in the config file.
	harnessLaunchConfigKey harnessLaunchKeyMode = iota
	// harnessLaunchDeviceLogin enrols a device key and approves the stub's
	// device refresh, so a harness that can refresh runs in device mode.
	harnessLaunchDeviceLogin
)

// harnessLaunchOnPath returns the harness binary, or skips the test when it is
// not installed. Under PRIZMAL_HARNESS_LAUNCH=require — which every harness
// workflow sets — the absence is a failure, so CI cannot pass by skipping.
func harnessLaunchOnPath(t *testing.T, harness string) string {
	t.Helper()
	path, err := exec.LookPath(harness)
	if err != nil {
		if os.Getenv(harnessLaunchRequireEnv) == "require" {
			t.Fatalf("%s is not on PATH", harness)
		}
		t.Skipf("%s is not on PATH", harness)
	}
	return path
}

// skipHarnessLaunchInShortMode skips a launch test under `go test -short`,
// where a real harness would only slow the suite down.
func skipHarnessLaunchInShortMode(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping harness launch in short mode")
	}
}

// runHarnessLaunch launches harness through a freshly built prizmal, passing
// its case's arguments after the integration name, and asserts that the
// stub's reply is on stdout. The key mode decides what credential the machine
// starts with and which key source the launch must announce.
func runHarnessLaunch(t *testing.T, harness string, mode harnessLaunchKeyMode) {
	t.Helper()
	// A harness with no launch case is a bug in the test, not a launch with no
	// arguments, so fail on it before anything else runs.
	launchCase, ok := harnessLaunchCases[harness]
	if !ok {
		t.Fatalf("harnessLaunchCases has no entry for %q", harness)
	}
	harnessArgs := launchCase.args
	skipHarnessLaunchInShortMode(t)
	harnessPath := harnessLaunchOnPath(t, harness)

	recorder := &stubserver.Recorder{}
	srvOpts := []stubserver.ServerOption{stubserver.WithModels(harnessLaunchModel), stubserver.WithRecorder(recorder)}
	switch mode {
	case harnessLaunchDeviceLogin:
		// The stub accepts one credential: the device token its refresh just
		// handed out. Every other key is refused, so the reply on stdout proves
		// the harness authenticated with a token the helper minted, not with
		// the switch key the config file already held.
		srvOpts = append(srvOpts, stubserver.WithDeviceTokenOnly())
	}
	srv := stubserver.NewServer(srvOpts...)
	t.Cleanup(srv.Close)

	// A fresh HOME gives the harness no config of its own, the state a
	// first launch on a new machine starts from.
	prizmalBin, home, project := harnessLaunchSandbox(t, srv.URL, mode != harnessLaunchConfigKey)

	stdout, stderr, timedOut, runErr := runHarnessCommand(t, prizmalBin, home, project, harnessPath, harness, harnessArgs)

	switch mode {
	case harnessLaunchConfigKey:
		// Nothing to assert about the source: no device key was enrolled, so
		// the launch resolves the config file's key and nothing else.
	case harnessLaunchDeviceLogin:
		// The announce line names the credential in use. Device login is the
		// source, which is the whole point of the harness running in device
		// mode.
		if !strings.Contains(stderr, "using api key from: device login") {
			t.Errorf("launch did not announce device login:\n--- stderr ---\n%s", tail(stderr, 50))
		}
	}
	if harness == "codex" {
		// Codex sends its system prompt as the request's instructions. A launch
		// that hands Codex a catalog without one sends them empty, which the
		// stub can see because it records the body of each responses request.
		bodies := recorder.ResponsesBodies()
		if len(bodies) == 0 {
			t.Fatal("codex sent no responses request to the stub")
		}
		for i, body := range bodies {
			if ins, _ := body["instructions"].(string); strings.TrimSpace(ins) == "" {
				t.Errorf("responses request %d carries empty instructions", i)
			}
		}
	}
	if !strings.Contains(stdout, stubserver.Reply) {
		t.Fatalf("%s did not print the stub's reply %q on stdout (run error: %v, timed out: %v)\n--- stdout ---\n%s\n--- stderr ---\n%s",
			harness, stubserver.Reply, runErr, timedOut, tail(stdout, 50), tail(stderr, 50))
	}
}

// harnessLaunchSandbox builds prizmal and lays out the launch's HOME: a config
// file pointing at switchURL, and, when enrollDevice is set, an enrolled device
// key. It returns the built prizmal binary, the HOME, and the project directory
// to launch in.
func harnessLaunchSandbox(t *testing.T, switchURL string, enrollDevice bool) (prizmalBin, home, project string) {
	t.Helper()
	// Codex keeps cloning plugins into HOME after it exits, so a t.TempDir
	// cleanup would fail the test on a file created mid-removal.
	root, err := os.MkdirTemp("", "prizmal-launch-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	prizmalBin = filepath.Join(root, "prizmal")
	if out, err := exec.Command("go", "build", "-o", prizmalBin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build prizmal: %v\n%s", err, out)
	}

	home = filepath.Join(root, "home")
	project = filepath.Join(home, "project")
	if err := os.MkdirAll(filepath.Join(home, ".prizmal"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSONFile(t, filepath.Join(home, ".prizmal", "config.json"), map[string]any{
		"version":  1,
		"base_url": switchURL,
		"api_key":  stubserver.StubKey,
	})
	if enrollDevice {
		// A real key, so a device-mode launch can sign a refresh the approving
		// stub accepts.
		writeDeviceKey(t, home)
	}
	return prizmalBin, home, project
}

// runHarnessCommand runs prizmal's launch of harness and returns what it wrote
// to stdout and stderr, the run error, and whether the context deadline was
// reached (so a caller can tell a timeout from a plain failure).
func runHarnessCommand(t *testing.T, prizmalBin, home, project, harnessPath, harness string, harnessArgs []string) (stdout, stderr string, timedOut bool, runErr error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), harnessLaunchTimeout)
	t.Cleanup(cancel)
	args := append([]string{"-y", "-m", harnessLaunchModel, harness, "--"}, harnessArgs...)
	cmd := exec.CommandContext(ctx, prizmalBin, args...)
	cmd.Dir = project
	// The harness's own directory leads PATH, so an npm-installed harness
	// finds the node it was installed with.
	cmd.Env = []string{
		"HOME=" + home,
		"PATH=" + filepath.Dir(harnessPath) + string(filepath.ListSeparator) + os.Getenv("PATH"),
		"TMPDIR=" + os.TempDir(),
		"LANG=C.UTF-8",
		"TERM=dumb",
		"NO_COLOR=1",
		"DISABLE_AUTOUPDATER=1",
	}
	cmd.Env = append(cmd.Env, device.TestingEnv()...)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	cmd.Stdin = nil
	// A harness can leave child processes holding stdout after the timeout
	// kills it. WaitDelay stops Wait from blocking on them.
	cmd.WaitDelay = 10 * time.Second
	runErr = cmd.Run()
	return outBuf.String(), errBuf.String(), ctx.Err() != nil, runErr
}

// tail returns the last n lines of s.
func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
