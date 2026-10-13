// Package mockbin provides the Go implementation of the test mock harness.
// The tests build it with `go build` and copy it onto a temp PATH under each
// harness name (claude, codex, ...), replacing the previous POSIX shell mock
// (`mock-bin.sh`) so the mock-harness subprocess tests run on every OS,
// including Windows CI.
//
// Behavior (mirrors the shell mock it replaced):
//   - writes <name>.called.args and <name>.called.env markers under
//     $MOCK_EXPECTATIONS_DIR so the parent test can verify the harness binary
//     was actually invoked and what it saw;
//   - on a bare `--version` first arg, prints a version string and exits 0
//     (codex install-discovery path);
//   - validates expectations from mock.json under $MOCK_EXPECTATIONS_DIR and
//     exits non-zero with a per-check report on stderr if any fail.
package mockbin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/PrizmalAi/prizmal-cli/internal/claudecode"
)

// Expectations is the JSON contract between the test and the mock binary,
// written by the test as mock.json under $MOCK_EXPECTATIONS_DIR.
type Expectations struct {
	// RequiredEnv is a list of env vars that must be set and non-empty in
	// the mock's environment.
	RequiredEnv []string `json:"required_env,omitempty"`
	// ForbiddenEnv is a list of env vars that must NOT be set.
	ForbiddenEnv []string `json:"forbidden_env,omitempty"`
	// RequiredArgs is a list of args that must appear in the command line.
	RequiredArgs []string `json:"required_args,omitempty"`
	// StubRequest, when non-nil, makes the mock send one authenticated HTTP
	// request to the stub server, using the base URL and key that prizmal
	// put in its environment (env var names given below). This lets the
	// parent test verify that the values prizmal emits are accepted by a
	// real HTTP server that checks them — observed effect, not constructed
	// command — without any real harness binary or real credentials.
	StubRequest *StubRequest `json:"stub_request,omitempty"`
}

// StubRequest names the env vars carrying the endpoint and credential, and
// the wire shape to use. Default path is /v1/messages (Anthropic dialect).
type StubRequest struct {
	// BaseURLEnv is the env var holding the provider base URL.
	BaseURLEnv string `json:"base_url_env"`
	// KeyEnv is the env var holding the provider API key.
	KeyEnv string `json:"key_env"`
	// Path is the endpoint path; default "/v1/messages".
	Path string `json:"path,omitempty"`
	// Model is the model field to put in the request body when non-empty.
	Model string `json:"model,omitempty"`
	// ModelFromSettings, when true, takes the model from the --settings JSON
	// argument instead of the Model field. Claude Code is launched with its
	// model in that argument rather than in an env var, so this is how a test
	// observes the model a launch actually sends.
	ModelFromSettings bool `json:"model_from_settings,omitempty"`
}

// Run executes the mock harness process: os.Args[0]'s basename is the
// harness name, os.Args[1:] are the harness args. It returns the process
// exit code after writing marker files and validating expectations.
func Run() int {
	binName := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
	expDir := os.Getenv("MOCK_EXPECTATIONS_DIR")

	if expDir == "" {
		fmt.Fprintf(os.Stderr, "mock %s: MOCK_EXPECTATIONS_DIR is not set\n", binName)
		return 1
	}
	if err := os.MkdirAll(expDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mock %s: mkdir: %v\n", binName, err)
		return 1
	}

	args := os.Args[1:]
	writeMarker(expDir, binName+".called.args", strings.Join(args, " "))

	env := os.Environ()
	sort.Strings(env)
	writeMarker(expDir, binName+".called.env", strings.Join(env, "\n"))

	// codex install discovery calls `codex --version`.
	if len(args) == 1 && args[0] == "--version" {
		fmt.Println("codex-cli 0.134.0")
		return 0
	}

	// npm-based harnesses call `npm --version` or `npm install` during
	// install discovery; succeed silently.
	switch binName {
	case "npm":
		if len(args) > 0 && (args[0] == "--version" || args[0] == "-v") {
			fmt.Println("0.0.0")
			return 0
		}
		if len(args) > 0 && (args[0] == "install" || args[0] == "update") {
			return 0
		}
	}

	exp, err := loadExpectations(expDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mock %s: %v\n", binName, err)
		return 1
	}

	var failures []string
	for _, v := range exp.RequiredEnv {
		if os.Getenv(v) == "" {
			failures = append(failures, fmt.Sprintf("missing env: %s", v))
		}
	}
	for _, v := range exp.ForbiddenEnv {
		if val := os.Getenv(v); val != "" {
			failures = append(failures, fmt.Sprintf("unexpected env: %s=%s", v, val))
		}
	}
	for _, a := range exp.RequiredArgs {
		if !contains(args, a) {
			failures = append(failures, fmt.Sprintf("missing arg: %s", a))
		}
	}

	if exp.StubRequest != nil {
		if err := doStubRequest(exp.StubRequest, args); err != nil {
			failures = append(failures, fmt.Sprintf("stub request: %v", err))
		}
	}

	if len(failures) > 0 {
		fmt.Fprintf(os.Stderr, "mock %s FAILED:\n", binName)
		for _, f := range failures {
			fmt.Fprintf(os.Stderr, "  %s\n", f)
		}
		return 1
	}
	return 0
}

// loadExpectations reads mock.json from the expectations dir. A missing file
// means "no expectations", which preserves the shell mock's default of
// marker-writing only.
func loadExpectations(expDir string) (*Expectations, error) {
	p := filepath.Join(expDir, "mock.json")
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return &Expectations{}, nil
	}
	if err != nil {
		return nil, err
	}
	var exp Expectations
	if err := json.Unmarshal(data, &exp); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	return &exp, nil
}

func writeMarker(expDir, name, content string) {
	_ = os.WriteFile(filepath.Join(expDir, name), []byte(content), 0o644)
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// doStubRequest sends one request to the stub server using the base URL and
// key from the mock's environment, authenticated the way the harnesses
// authenticate (Authorization: Bearer + x-api-key). A non-200 response is a
// validation failure, reported on stderr.
func doStubRequest(req *StubRequest, args []string) error {
	if req.BaseURLEnv == "" || req.KeyEnv == "" {
		return fmt.Errorf("stub_request needs base_url_env and key_env")
	}
	base := strings.TrimRight(os.Getenv(req.BaseURLEnv), "/")
	key := os.Getenv(req.KeyEnv)
	if base == "" {
		return fmt.Errorf("env %s is empty", req.BaseURLEnv)
	}
	if key == "" {
		return fmt.Errorf("env %s is empty", req.KeyEnv)
	}

	path := req.Path
	if path == "" {
		path = "/v1/messages"
	}
	model := req.Model
	if req.ModelFromSettings {
		// Claude Code receives its model in an inline --settings JSON argument
		// rather than in an environment variable, so the model the launch
		// sends is only observable from the command line.
		fromSettings, err := modelFromSettingsArg(args)
		if err != nil {
			return err
		}
		model = fromSettings
	}
	if model == "" {
		return fmt.Errorf("stub_request has no model to send")
	}
	body := fmt.Sprintf(`{"model":%q,"max_tokens":1,"messages":[{"role":"user","content":"ping"}]}`, model)

	httpReq, err := http.NewRequest(http.MethodPost, base+path, strings.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Authorization", "Bearer "+key)
	httpReq.Header.Set("x-api-key", key)
	httpReq.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("stub returned %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}
	_, _ = fmt.Fprintln(os.Stdout, string(respBody))
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// modelFromSettingsArg reads the "model" field out of the inline --settings JSON
// a Claude Code launch passes as an argument. It returns the value with Claude
// Code's [1m] context-budget suffix stripped, because that suffix is the
// client's own instruction and never part of the model id on the wire.
//
// The suffix is not spelled here. It used to be, as a constant mirroring the
// one the CLI appends, and the mirror was free to drift: a changed suffix
// would have left the mock asserting against a decoration the launch no longer
// sends, and the expectation would fail on a model id rather than on anything
// about the mock.
func modelFromSettingsArg(args []string) (string, error) {
	for i := 0; i < len(args); i++ {
		value, ok := strings.CutPrefix(args[i], "--settings=")
		if !ok {
			if args[i] != "--settings" {
				continue
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("--settings has no value")
			}
			value = args[i+1]
		}
		var settings struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal([]byte(value), &settings); err != nil {
			return "", fmt.Errorf("parse --settings JSON: %w", err)
		}
		if settings.Model == "" {
			return "", fmt.Errorf("--settings JSON carries no model")
		}
		return claudecode.RoutableName(settings.Model), nil
	}
	return "", fmt.Errorf("no --settings argument on the command line")
}
