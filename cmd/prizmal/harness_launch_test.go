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

func TestHarnessLaunchClaude(t *testing.T)   { runHarnessLaunch(t, "claude") }
func TestHarnessLaunchCodex(t *testing.T)    { runHarnessLaunch(t, "codex") }
func TestHarnessLaunchCline(t *testing.T)    { runHarnessLaunch(t, "cline") }
func TestHarnessLaunchOpencode(t *testing.T) { runHarnessLaunch(t, "opencode") }
func TestHarnessLaunchPi(t *testing.T)       { runHarnessLaunch(t, "pi") }

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

// runHarnessLaunch launches harness through a freshly built prizmal, passing
// its case's arguments after the integration name, and asserts that the
// stub's reply is on stdout.
func runHarnessLaunch(t *testing.T, harness string) {
	t.Helper()
	harnessArgs := harnessLaunchCases[harness].args
	if testing.Short() {
		t.Skip("skipping harness launch in short mode")
	}
	harnessPath, err := exec.LookPath(harness)
	if err != nil {
		if os.Getenv(harnessLaunchRequireEnv) == "require" {
			t.Fatalf("%s is not on PATH", harness)
		}
		t.Skipf("%s is not on PATH", harness)
	}

	// Codex keeps cloning plugins into HOME after it exits, so a t.TempDir
	// cleanup would fail the test on a file created mid-removal.
	root, err := os.MkdirTemp("", "prizmal-launch-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	prizmalBin := filepath.Join(root, "prizmal")
	if out, err := exec.Command("go", "build", "-o", prizmalBin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build prizmal: %v\n%s", err, out)
	}

	srv := stubserver.NewWithModels(harnessLaunchModel)
	t.Cleanup(srv.Close)

	// A fresh HOME gives the harness no config of its own, the state a
	// first launch on a new machine starts from.
	home := filepath.Join(root, "home")
	project := filepath.Join(home, "project")
	if err := os.MkdirAll(filepath.Join(home, ".prizmal"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSONFile(t, filepath.Join(home, ".prizmal", "config.json"), map[string]any{
		"version":  1,
		"base_url": srv.URL,
		"api_key":  stubserver.StubKey,
	})

	ctx, cancel := context.WithTimeout(context.Background(), harnessLaunchTimeout)
	defer cancel()
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
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Stdin = nil
	// A harness can leave child processes holding stdout after the timeout
	// kills it. WaitDelay stops Wait from blocking on them.
	cmd.WaitDelay = 10 * time.Second
	runErr := cmd.Run()

	if !strings.Contains(stdout.String(), stubserver.Reply) {
		t.Fatalf("%s did not print the stub's reply %q on stdout (run error: %v, timed out: %v)\n--- stdout ---\n%s\n--- stderr ---\n%s",
			harness, stubserver.Reply, runErr, ctx.Err() != nil, tail(stdout.String(), 50), tail(stderr.String(), 50))
	}
}

// tail returns the last n lines of s.
func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
