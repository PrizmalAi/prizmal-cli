//go:build !windows

package main

import (
	"context"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/codex"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/device"
	"github.com/PrizmalAi/prizmal-cli/internal/stubserver"
)

// These tests run the real Codex against a stub switch to prove how Codex
// treats a command-backed provider auth, which prizmal uses in device mode.
// Like the harness launch tests they skip when Codex is not on PATH, and the
// Codex workflow sets PRIZMAL_HARNESS_LAUNCH=require.

// recordedRequests collects the stub's request log for assertions.
type recordedRequests struct {
	mu   sync.Mutex
	list []stubserver.Request
}

func (r *recordedRequests) add(req stubserver.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.list = append(r.list, req)
}

// matching returns the requests for method and path.
func (r *recordedRequests) matching(method, path string) []stubserver.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []stubserver.Request
	for _, req := range r.list {
		if req.Method == method && req.Path == path {
			out = append(out, req)
		}
	}
	return out
}

func (r *recordedRequests) all() []stubserver.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]stubserver.Request(nil), r.list...)
}

func requireCodex(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping harness launch in short mode")
	}
	path, err := exec.LookPath("codex")
	if err != nil {
		if os.Getenv(harnessLaunchRequireEnv) == "require" {
			t.Fatal("codex is not on PATH")
		}
		t.Skip("codex is not on PATH")
	}
	return path
}

// codexSandbox is a fresh HOME with a project directory, the state a first
// launch on a new machine starts from.
type codexSandbox struct {
	root, home, project string
}

func newCodexSandbox(t *testing.T) codexSandbox {
	t.Helper()
	// Codex keeps writing into HOME after it exits, so a t.TempDir cleanup
	// would fail the test on a file created mid-removal.
	root, err := os.MkdirTemp("", "prizmal-codex-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	sb := codexSandbox{root: root, home: filepath.Join(root, "home"), project: filepath.Join(root, "home", "project")}
	if err := os.MkdirAll(filepath.Join(sb.home, ".prizmal"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sb.home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sb.project, 0o755); err != nil {
		t.Fatal(err)
	}
	return sb
}

// env is the minimal environment for a Codex process in the sandbox.
func (sb codexSandbox) env(codexPath string) []string {
	env := []string{
		"HOME=" + sb.home,
		"PATH=" + filepath.Dir(codexPath) + string(filepath.ListSeparator) + os.Getenv("PATH"),
		"TMPDIR=" + os.TempDir(),
		"LANG=C.UTF-8",
		"TERM=dumb",
		"NO_COLOR=1",
	}
	return append(env, device.TestingEnv()...)
}

// runCodexDirect runs `codex exec` against srv with a provider that gets its
// bearer token from command, bypassing prizmal, so the test sees Codex's own
// handling of the command and of /models. withCatalog adds a model catalog
// file, as prizmal always does.
func runCodexDirect(t *testing.T, srv *httptest.Server, command string, withCatalog bool) (stdout string) {
	t.Helper()
	codexPath := requireCodex(t)
	sb := newCodexSandbox(t)

	args := []string{
		"exec", "--skip-git-repo-check", "-s", "read-only",
		"-c", `model_provider="p"`,
		"-c", `model_providers.p.name="p"`,
		"-c", `model_providers.p.base_url="` + srv.URL + `/v1/"`,
		"-c", `model_providers.p.wire_api="responses"`,
		"-c", `model_providers.p.auth.command="` + command + `"`,
	}
	if withCatalog {
		catalog := filepath.Join(sb.root, "model.json")
		// The production builder writes it, so the entry cannot drift from what a
		// launch hands Codex.
		if err := codex.WriteModelCatalog(catalog, harnessLaunchModel, nil); err != nil {
			t.Fatalf("write the model catalog: %v", err)
		}
		args = append(args, "-c", `model_catalog_json="`+catalog+`"`)
	}
	args = append(args, "-m", harnessLaunchModel, harnessLaunchPrompt)

	ctx, cancel := context.WithTimeout(context.Background(), harnessLaunchTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, codexPath, args...)
	cmd.Dir = sb.project
	cmd.Env = sb.env(codexPath)
	var out, errOut strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	cmd.WaitDelay = harnessLaunchTimeout / 6
	_ = cmd.Run()
	if !strings.Contains(out.String(), stubserver.Reply) {
		t.Fatalf("codex did not print the stub's reply %q (timed out: %v)\n--- stdout ---\n%s\n--- stderr ---\n%s",
			stubserver.Reply, ctx.Err() != nil, tail(out.String(), 50), tail(errOut.String(), 50))
	}
	return out.String()
}

// countingTokenCommand writes an executable script that appends a line to a
// counter file on every run and prints a token. It returns the script path and
// a function that reads how many times Codex ran it.
func countingTokenCommand(t *testing.T, token string) (string, func() int) {
	t.Helper()
	dir, err := os.MkdirTemp("", "prizmal-token-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	counter := filepath.Join(dir, "runs")
	script := filepath.Join(dir, "token.sh")
	// Both values go through shellQuote, which single-quotes and escapes any
	// embedded quote. strconv.Quote is not enough here: it emits a
	// double-quoted string, in which the shell would still expand $, backticks
	// and \\, so a token or temp path carrying one would reach the test
	// mangled rather than verbatim.
	body := "#!/bin/sh\necho run >> " + shellQuote(counter) + "\necho " + shellQuote(token) + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script, func() int {
		data, err := os.ReadFile(counter)
		if err != nil {
			return 0
		}
		return strings.Count(string(data), "\n")
	}
}

// TestCodexRerunsTheAuthCommandAfterA401 proves the refresh contract device
// mode relies on: when the Switch refuses a turn as an expired credential,
// Codex runs the provider's auth command again and retries the turn. The
// first run happens before the first turn, so a second run is the rerun.
func TestCodexRerunsTheAuthCommandAfterA401(t *testing.T) {
	requireCodex(t)
	command, runs := countingTokenCommand(t, "pz-d-dt-counted")
	var log recordedRequests
	srv := stubserver.NewServer(
		stubserver.WithModels(harnessLaunchModel),
		stubserver.WithRejectFirstResponse(),
		stubserver.WithRequestLog(log.add),
	)
	t.Cleanup(srv.Close)

	runCodexDirect(t, srv, command, true)

	turns := log.matching("POST", "/v1/responses")
	if len(turns) != 2 {
		t.Fatalf("the switch saw %d turns, want 2 (the refused one and the retry)", len(turns))
	}
	if got := runs(); got < 2 {
		t.Errorf("the auth command ran %d times, want at least 2: a 401 must make Codex run it again", got)
	}
	for _, turn := range turns {
		if turn.Authorization != "Bearer pz-d-dt-counted" {
			t.Errorf("a turn carried %q, want the command's token", turn.Authorization)
		}
	}
}

// TestCodexDiscoveryShapes pins what Codex does with each answer to its
// GET /models call, which a provider with command auth makes:
//   - with prizmal's model catalog file, Codex never makes the call, so the
//     answer cannot matter;
//   - without one, Codex asks, and finishes the turn whatever the answer is:
//     the Codex shape, the OpenAI list, a 404 or a 500.
func TestCodexDiscoveryShapes(t *testing.T) {
	requireCodex(t)
	t.Run("with a catalog file, Codex never asks", func(t *testing.T) {
		command, _ := countingTokenCommand(t, "pz-d-dt-counted")
		var log recordedRequests
		srv := stubserver.NewServer(stubserver.WithModels(harnessLaunchModel), stubserver.WithCodexModels(), stubserver.WithRequestLog(log.add))
		t.Cleanup(srv.Close)

		runCodexDirect(t, srv, command, true)

		if got := log.matching("GET", "/v1/models"); len(got) != 0 {
			t.Errorf("codex called GET /v1/models %d times despite a catalog file", len(got))
		}
	})
	for _, tc := range []struct {
		name string
		opts []stubserver.ServerOption
	}{
		{"the Codex shape", []stubserver.ServerOption{stubserver.WithCodexModels()}},
		{"the OpenAI list", nil},
		{"a 404", []stubserver.ServerOption{stubserver.WithoutModels()}},
		{"a 500", []stubserver.ServerOption{stubserver.WithFailingModels()}},
	} {
		t.Run("without a catalog file, "+tc.name+" does not stop the turn", func(t *testing.T) {
			command, _ := countingTokenCommand(t, "pz-d-dt-counted")
			var log recordedRequests
			opts := append([]stubserver.ServerOption{stubserver.WithModels(harnessLaunchModel), stubserver.WithRequestLog(log.add)}, tc.opts...)
			srv := stubserver.NewServer(opts...)
			t.Cleanup(srv.Close)

			runCodexDirect(t, srv, command, false)

			if got := log.matching("GET", "/v1/models"); len(got) == 0 {
				t.Errorf("codex never called GET /v1/models without a catalog file")
			}
		})
	}
}

// TestCodexDeviceLaunchRunsOnTheDeviceToken launches Codex through prizmal on
// a machine signed in with a device key. The stub holds a switch key in the
// config and approves the device, so the test sees which credential reached
// the Switch: the device token must carry every turn, and the switch key must
// never leave the machine.
func TestCodexDeviceLaunchRunsOnTheDeviceToken(t *testing.T) {
	codexPath := requireCodex(t)
	sb := newCodexSandbox(t)

	prizmalBin := filepath.Join(sb.root, "prizmal")
	if out, err := exec.Command("go", "build", "-o", prizmalBin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build prizmal: %v\n%s", err, out)
	}

	var log recordedRequests
	srv := stubserver.NewServer(
		stubserver.WithModels(harnessLaunchModel),
		stubserver.WithCodexModels(),
		stubserver.WithDeviceApproval(),
		stubserver.WithRequestLog(log.add),
	)
	t.Cleanup(srv.Close)

	writeJSONFile(t, filepath.Join(sb.home, ".prizmal", "config.json"), map[string]any{
		"version": 1, "base_url": srv.URL, "api_key": stubserver.StubKey,
	})
	key, err := device.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := key.WriteKeyFile(filepath.Join(sb.home, ".prizmal")); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), harnessLaunchTimeout)
	defer cancel()
	args := append([]string{"-y", "-m", harnessLaunchModel, "codex", "--"}, harnessLaunchCases["codex"].args...)
	cmd := exec.CommandContext(ctx, prizmalBin, args...)
	cmd.Dir = sb.project
	cmd.Env = sb.env(codexPath)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = harnessLaunchTimeout / 6
	runErr := cmd.Run()

	if !strings.Contains(stdout.String(), stubserver.Reply) {
		t.Fatalf("codex did not print the stub's reply (run error: %v, timed out: %v)\n--- stdout ---\n%s\n--- stderr ---\n%s",
			runErr, ctx.Err() != nil, tail(stdout.String(), 50), tail(stderr.String(), 50))
	}

	turns := log.matching("POST", "/v1/responses")
	if len(turns) == 0 {
		t.Fatal("the switch saw no turn")
	}
	for _, turn := range turns {
		if want := "Bearer " + stubserver.DeviceToken; turn.Authorization != want {
			t.Errorf("a turn carried %q, want the device token", turn.Authorization)
		}
	}
	for _, req := range log.all() {
		if strings.Contains(req.Authorization, stubserver.StubKey) {
			t.Errorf("%s %s carried the switch key", req.Method, req.Path)
		}
	}
	// The launch writes no profile file: the command auth rides -c overrides.
	if _, err := os.Stat(filepath.Join(sb.home, ".codex", "prizmal.config.toml")); !os.IsNotExist(err) {
		t.Errorf("the launch left a profile under ~/.codex (stat err = %v)", err)
	}
}
