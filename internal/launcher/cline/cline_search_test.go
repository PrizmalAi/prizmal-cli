package cline

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

var clineMCPPath = filepath.Join(".cline", "data", "settings", "cline_mcp_settings.json")

const userMCPServer = `"their-server":{"transport":{"type":"stdio","command":"their-cmd","args":["--flag"]}}`

func mcpServers(t *testing.T, root, rel string) map[string]any {
	t.Helper()
	servers, _ := internaltest.SandboxedJSON(t, root, rel)["mcpServers"].(map[string]any)
	return servers
}

func TestClineEditMergesSearchServerAndKeepsOthers(t *testing.T) {
	home := internaltest.SandboxedHome(t)
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)
	internaltest.SandboxedSeed(t, home, clineMCPPath, `{"mcpServers":{`+userMCPServer+`},"other":true}`)

	if err := (&Cline{}).Edit([]launch.LaunchModel{{Name: "probe-model"}}); err != nil {
		t.Fatal(err)
	}
	config := internaltest.SandboxedJSON(t, home, clineMCPPath)
	if config["other"] != true {
		t.Error("a top-level key the user had was dropped")
	}
	servers, _ := config["mcpServers"].(map[string]any)
	theirs, _ := servers["their-server"].(map[string]any)
	if transport, _ := theirs["transport"].(map[string]any); transport["command"] != "their-cmd" {
		t.Errorf("their-server = %v, want it kept as it was", servers["their-server"])
	}
	entry, _ := servers[clineSearchServerName].(map[string]any)
	transport, _ := entry["transport"].(map[string]any)
	env, _ := transport["env"].(map[string]any)
	if transport["type"] != "stdio" || transport["command"] != "node" {
		t.Errorf("prizmal_search transport = %v, want stdio via node", transport)
	}
	wantPath := filepath.Join(home, ".cache", "prizmal", "cline", "prizmal_search.mjs")
	if args, _ := transport["args"].([]any); len(args) != 1 || args[0] != wantPath {
		t.Errorf("args = %v, want [%s]", transport["args"], wantPath)
	}
	if env[clineSearchURLEnv] != envconfig.OpenAIBaseURL(false) || env[clineSearchModelEnv] != "probe-model" {
		t.Errorf("env = %v, want the Switch's /v1 base and probe-model", env)
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Errorf("generated server: %v", err)
	}
	internaltest.AssertNoKeyUnder(t, home, "a cline launch with web search")
}

func TestClineRestoreRemovesOnlySearchServer(t *testing.T) {
	home := internaltest.SandboxedHome(t)
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)
	internaltest.SandboxedSeed(t, home, clineMCPPath, `{"mcpServers":{`+userMCPServer+`}}`)
	c := &Cline{}
	for _, model := range []string{"probe-model-a", "probe-model-b"} {
		if err := c.Edit([]launch.LaunchModel{{Name: model}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.Restore(); err != nil {
		t.Fatal(err)
	}
	servers := mcpServers(t, home, clineMCPPath)
	if _, ok := servers[clineSearchServerName]; ok {
		t.Error("prizmal_search survived restore")
	}
	if _, ok := servers["their-server"]; !ok {
		t.Error("restore removed the user's own server")
	}
}

func TestClineRestoreReinstatesPriorSearchServer(t *testing.T) {
	home := internaltest.SandboxedHome(t)
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)
	internaltest.SandboxedSeed(t, home, clineMCPPath, `{"mcpServers":{"prizmal_search":{"transport":{"type":"stdio","command":"their-search"}}}}`)
	c := &Cline{}
	if err := c.Edit([]launch.LaunchModel{{Name: "probe-model"}}); err != nil {
		t.Fatal(err)
	}
	outcome, err := c.Restore()
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := mcpServers(t, home, clineMCPPath)[clineSearchServerName].(map[string]any)
	if transport, _ := entry["transport"].(map[string]any); transport["command"] != "their-search" {
		t.Fatalf("prizmal_search = %v, want the user's own entry back", entry)
	}
	if !strings.Contains(strings.Join(outcome.Reinstated, " "), "mcpServers.prizmal_search") {
		t.Errorf("outcome.Reinstated = %v, want the server named", outcome.Reinstated)
	}
}

func TestClineRestoreDeletesSearchSettingsItCreated(t *testing.T) {
	home := internaltest.SandboxedHome(t)
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)
	c := &Cline{}
	if err := c.Edit([]launch.LaunchModel{{Name: "probe-model"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Restore(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, clineMCPPath)); !os.IsNotExist(err) {
		t.Fatalf("cline_mcp_settings.json still there after restore: %v", err)
	}
}

func TestClineRunWritesSearchServerIntoDataDirFlagTree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake cline is a shell script")
	}
	home := internaltest.SandboxedHome(t)
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)
	t.Setenv("CLINE_DATA_DIR", "")
	t.Setenv("CLINE_DIR", "")
	bin := internaltest.WithEmptyPATH(t)
	if err := os.WriteFile(filepath.Join(bin, "cline"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "data")
	if err := (&Cline{}).Run("probe-model", nil, []string{"--data-dir", dir, "task"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := mcpServers(t, dir, filepath.Join("settings", "cline_mcp_settings.json"))[clineSearchServerName]; !ok {
		t.Fatal("the --data-dir tree has no prizmal_search server")
	}
	if _, err := os.Stat(filepath.Join(home, clineMCPPath)); !os.IsNotExist(err) {
		t.Fatalf("default cline_mcp_settings.json exists or cannot be checked: %v", err)
	}
}

func TestClineSearchServerSourceIsValidAndHoldsNoKey(t *testing.T) {
	internaltest.SandboxedHome(t)
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)
	source := clineSearchServerSource()
	for _, bad := range []string{internaltest.FakeKeyAtRest, "__URL_ENV__", "__MODEL_ENV__", "__TOOL__", "__SERVER__"} {
		if strings.Contains(source, bad) {
			t.Fatalf("generated server carries %q", bad)
		}
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on PATH")
	}
	path, err := writeClineSearchServer()
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(node, "--check", path).CombinedOutput(); err != nil {
		t.Fatalf("node --check: %v\n%s", err, out)
	}
}

// TestClineSearchServerAnswersOverStdio speaks to the generated server the
// way Cline does and checks the request it sends the Switch.
func TestClineSearchServerAnswersOverStdio(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on PATH")
	}
	internaltest.SandboxedHome(t)
	var got struct {
		path, auth string
		body       map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path, got.auth = r.URL.Path, r.Header.Get("Authorization")
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &got.body)
		if strings.Contains(string(data), "fail me") {
			http.Error(w, "nope", http.StatusForbidden)
			return
		}
		_, _ = io.WriteString(w, `{"output":[{"type":"message","content":[{"type":"output_text","text":"Go 9.9 https://go.example/dl"}]}]}`)
	}))
	defer srv.Close()

	path, err := writeClineSearchServer()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, path)
	cmd.Env = append(os.Environ(), clineSearchURLEnv+"="+srv.URL+"/v1/", clineSearchModelEnv+"=probe-model", "OPENAI_API_KEY=probe-key")
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close(); _ = cmd.Wait() }()
	lines := bufio.NewScanner(stdout)
	call := func(msg string) map[string]any {
		t.Helper()
		if _, err := io.WriteString(stdin, msg+"\n"); err != nil {
			t.Fatal(err)
		}
		done := make(chan bool, 1)
		go func() { done <- lines.Scan() }()
		select {
		case ok := <-done:
			if !ok {
				t.Fatalf("server closed stdout: %v", lines.Err())
			}
		case <-time.After(10 * time.Second):
			t.Fatal("no answer from the server")
		}
		var reply map[string]any
		if err := json.Unmarshal(lines.Bytes(), &reply); err != nil {
			t.Fatalf("reply %q: %v", lines.Text(), err)
		}
		return reply
	}

	if r := call(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`); r["result"] == nil {
		t.Fatalf("initialize = %v", r)
	}
	// A notification gets no answer, so the next line read is tools/list's.
	_, _ = io.WriteString(stdin, `{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n")
	list := call(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	tools, _ := list["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "search" {
		t.Fatalf("tools/list = %v, want one tool named search", list)
	}

	result, _ := call(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search","arguments":{"query":"latest go"}}}`)["result"].(map[string]any)
	if result["isError"] == true || !strings.Contains(toolText(result), "go.example") {
		t.Fatalf("tools/call result = %v", result)
	}
	if got.path != "/v1/responses" || got.auth != "Bearer probe-key" || got.body["model"] != "probe-model" {
		t.Errorf("request = %s auth %q model %v", got.path, got.auth, got.body["model"])
	}
	if tools, _ := got.body["tools"].([]any); len(tools) != 1 || tools[0].(map[string]any)["type"] != "web_search" {
		t.Errorf("request tools = %v, want web_search", got.body["tools"])
	}

	failed, _ := call(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"search","arguments":{"query":"fail me"}}}`)["result"].(map[string]any)
	if failed["isError"] != true || !strings.Contains(toolText(failed), "403") {
		t.Fatalf("failed call = %v, want isError with the status", failed)
	}
}

func toolText(result map[string]any) string {
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		return ""
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	return text
}
