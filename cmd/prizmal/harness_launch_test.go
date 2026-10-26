package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PrizmalAi/prizmal-cli/internal/device"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/registry"
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
	specs := registry.ListAllIntegrationSpecs()
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
		// The launch and the launch with no model share a workflow: both are
		// the same harness running against the same stub, and the second is
		// the case a script hits first.
		withoutModel := tc.test + "WithoutAModel"
		for _, want := range []string{
			"-run '" + tc.test + "$|" + withoutModel + "$'",
			harnessLaunchRequireEnv + ": require",
		} {
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
	runHarnessLaunchWith(t, harness, mode, true, false)
}

func runHarnessLaunchThroughRelay(t *testing.T, harness string, mode harnessLaunchKeyMode) {
	runHarnessLaunchWith(t, harness, mode, true, true)
}

// runHarnessLaunchWithoutAModel runs the harness's one-shot case with no model
// named at all, on a pipe. The picker cannot open there, so the launch must
// stop with the names the stub's catalog routes, and the harness must not run.
func runHarnessLaunchWithoutAModel(t *testing.T, harness string) {
	runHarnessLaunchWith(t, harness, harnessLaunchConfigKey, false, false)
}

func runHarnessLaunchWith(t *testing.T, harness string, mode harnessLaunchKeyMode, withModel, relayMode bool) {
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
	// The prompt a launch must put on the wire is Codex's own: the test reads it
	// from the installed binary, with the command the Codex doc gives an
	// operator who wires the launch by hand.
	promptText := ""
	if harness == "codex" {
		promptText = codexBundledPrompt(t)
	}

	recorder := &stubserver.Recorder{}
	srvOpts := []stubserver.ServerOption{
		stubserver.WithModels(harnessLaunchModel),
		stubserver.WithRecorder(recorder),
	}
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

	prizmalArgs := []string{"-y", "-m", harnessLaunchModel, harness, "--"}
	if !withModel {
		prizmalArgs = []string{"-y", harness, "--"}
	}
	if relayMode {
		prizmalArgs = append([]string{"--relay"}, prizmalArgs...)
	}
	stdout, stderr, timedOut, runErr := runPrizmalInSandbox(t, prizmalBin, home, project, harnessPath, append(prizmalArgs, harnessArgs...)...)

	if !withModel {
		if runErr == nil {
			t.Fatalf("a launch with no model and no terminal must fail\n--- stdout ---\n%s\n--- stderr ---\n%s", tail(stdout, 50), tail(stderr, 50))
		}
		for _, want := range []string{"no model selected", harnessLaunchModel} {
			if !strings.Contains(stderr, want) {
				t.Errorf("the failure does not name %q:\n--- stderr ---\n%s", want, tail(stderr, 50))
			}
		}
		if served := modelsOf(recorder); len(served) != 0 {
			t.Errorf("the harness sent %q, want no request without a model", served)
		}
		// Nothing ran, so the success assertions below have nothing to read.
		return
	}

	switch mode {
	case harnessLaunchConfigKey:
		// Nothing to assert about the source: no device key was enrolled, so
		// the launch resolves the config file's key and nothing else.
		if harness != "cline" && !relayMode && strings.Contains(stderr, "relaying through ") {
			t.Errorf("a launch without --relay started the loopback relay")
		}
	case harnessLaunchDeviceLogin:
		// The announce line names the credential in use. Device login is the
		// source, which is the whole point of the harness running in device
		// mode.
		if !strings.Contains(stderr, "using api key from: device login") {
			t.Errorf("launch did not announce device login:\n--- stderr ---\n%s", tail(stderr, 50))
		}
		if harness == "cline" && !strings.Contains(stderr, "relaying through ") {
			t.Errorf("Cline device login did not start its automatic relay:\n--- stderr ---\n%s", tail(stderr, 50))
		}
		if harness != "cline" && !relayMode && strings.Contains(stderr, "relaying through ") {
			t.Errorf("a device launch without --relay started the loopback relay")
		}
	}
	wantRelay := relayMode || harness == "cline"
	if wantRelay != strings.Contains(stderr, "relaying through ") {
		t.Errorf("relay announcement presence = %v, want %v", strings.Contains(stderr, "relaying through "), wantRelay)
	}
	if harness == "codex" {
		// A launch that hands Codex a catalog with no system prompt sends a
		// session with nothing describing its tools, its sandbox or its editing
		// rules. The stub records the body of each responses request, so the
		// test can read the prompt off the wire.
		bodies := recorder.ResponsesBodies()
		if len(bodies) == 0 {
			t.Fatal("codex sent no responses request to the stub")
		}
		for i, body := range bodies {
			if err := codexWireCarriesASystemPrompt(body, promptText); err != nil {
				t.Errorf("responses request %d: %v", i, err)
			}
		}
		// The catalog entry sets apply_patch_tool_type and supports_search_tool,
		// and Codex answers with the freeform apply_patch tool and a
		// tool_search tool in the tools it offers. Only the wire shows that the
		// settings reached the client, so the first request must offer both.
		offered := responsesToolNames(bodies[0])
		for _, want := range []string{"apply_patch", "tool_search"} {
			if !slices.Contains(offered, want) {
				t.Errorf("the first responses request does not offer the %s tool (offers %v)", want, offered)
			}
		}
	}
	assertServed(t, recorder)
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
	prizmalBin = filepath.Join(root, "prizmal"+exeSuffix())
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
	return runPrizmalInSandbox(t, prizmalBin, home, project, harnessPath,
		append([]string{"-y", "-m", harnessLaunchModel, harness, "--"}, harnessArgs...)...)
}

// runPrizmalInSandbox runs prizmal with the given arguments in the launch
// sandbox, with stdin closed so nothing can prompt.
func runPrizmalInSandbox(t *testing.T, prizmalBin, home, project, harnessPath string, args ...string) (stdout, stderr string, timedOut bool, runErr error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), harnessLaunchTimeout)
	t.Cleanup(cancel)
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

// codexWireCarriesASystemPrompt reports whether a responses request carries
// Codex's system prompt. The two Codex generations put it in different places:
// 0.160 sends it as the request's instructions, and 0.162 sends the same text
// rendered as the first developer message of the input. An empty catalog
// produces neither, and that is the regression the caller guards against.
func codexWireCarriesASystemPrompt(body map[string]any, prompt string) error {
	if ins, _ := body["instructions"].(string); prompt != "" && strings.Contains(ins, prompt) {
		return nil
	}
	for _, item := range inputItems(body) {
		if item["type"] != "message" || item["role"] != "developer" {
			continue
		}
		content, _ := item["content"].([]any)
		for _, part := range content {
			p, ok := part.(map[string]any)
			if !ok {
				continue
			}
			if text, _ := p["text"].(string); prompt != "" && strings.Contains(text, prompt) {
				return nil
			}
		}
	}
	return errors.New("the request carries no system prompt")
}

// inputItems returns the request's input when it is a list of items, which is
// the shape a launch that writes a model catalog sends.
func inputItems(body map[string]any) []map[string]any {
	raw, _ := body["input"].([]any)
	items := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if m, ok := item.(map[string]any); ok {
			items = append(items, m)
		}
	}
	return items
}

// codexBundledPrompt returns the system prompt of the installed Codex's default
// model, the lowest-priority entry its bundled catalog lists. The launcher reads
// the same prompt from the same command, so a launch that put an empty one on
// the wire fails the assertion this feeds.
func codexBundledPrompt(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("codex", "debug", "models", "--bundled").Output()
	if err != nil {
		t.Fatalf("codex debug models --bundled: %v", err)
	}
	var bundle struct {
		Models []struct {
			Visibility       string `json:"visibility"`
			Priority         int    `json:"priority"`
			BaseInstructions string `json:"base_instructions"`
		} `json:"models"`
	}
	if err := json.Unmarshal(out, &bundle); err != nil {
		t.Fatalf("parse the bundled catalog: %v", err)
	}
	best := -1
	for i, m := range bundle.Models {
		if m.Visibility != "list" || m.BaseInstructions == "" {
			continue
		}
		if best < 0 || m.Priority < bundle.Models[best].Priority {
			best = i
		}
	}
	if best < 0 {
		t.Fatal("the bundled catalog lists no model with a system prompt")
	}
	return bundle.Models[best].BaseInstructions
}

// tail returns the last n lines of s.
func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// responsesToolNames lists the tools a /v1/responses request offers by name,
// falling back to the tool's type for the ones that carry no name, such as the
// hosted tool_search.
func responsesToolNames(body map[string]any) []string {
	tools, _ := body["tools"].([]any)
	var names []string
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		if name, _ := tool["name"].(string); name != "" {
			names = append(names, name)
			continue
		}
		if kind, _ := tool["type"].(string); kind != "" {
			names = append(names, kind)
		}
	}
	return names
}

// exeSuffix is the extension a built executable carries: go build -o writes
// exactly the name it is given, and Windows will not start a file without it.
func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// harnessBinary returns the file in dir that starts the named harness. A
// harness installed with npm leaves a .cmd shim on Windows, and the native
// installers leave a .exe, so the name alone does not find it there. A harness the file does not
// exist for gets the bare path, as before.
func harnessBinary(dir, name string) string {
	for _, ext := range []string{"", ".exe", ".cmd"} {
		path := filepath.Join(dir, name+ext)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return filepath.Join(dir, name)
}
