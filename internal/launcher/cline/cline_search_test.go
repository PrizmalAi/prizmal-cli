package cline

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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
	legacy := filepath.Join(".cache", "prizmal", "cline", "prizmal_search.mjs")
	internaltest.SandboxedSeed(t, home, legacy, "// an older prizmal's Node server\n")

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
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if transport["type"] != "stdio" || transport["command"] != exe {
		t.Errorf("prizmal_search transport = %v, want stdio via %s", transport, exe)
	}
	if args, _ := transport["args"].([]any); len(args) != 2 || args[0] != "mcp" || args[1] != "search" {
		t.Errorf("args = %v, want [mcp search]", transport["args"])
	}
	raw, _ := json.Marshal(entry)
	if strings.Contains(string(raw), "node") || strings.Contains(string(raw), ".mjs") {
		t.Errorf("entry %s still names node or a .mjs file", raw)
	}
	if env[clineSearchURLEnv] != envconfig.OpenAIBaseURL(false) || env[clineSearchModelEnv] != "probe-model" {
		t.Errorf("env = %v, want the Switch's /v1 base and probe-model", env)
	}
	if _, err := os.Stat(filepath.Join(home, legacy)); !os.IsNotExist(err) {
		t.Errorf("the old Node server is still there or cannot be checked: %v", err)
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

// TestClineSearchServerAnswersOverStdio speaks to the server the way Cline
// does, over a pipe, and checks the request it sends the Switch.
func TestClineSearchServerAnswersOverStdio(t *testing.T) {
	var got struct {
		path, auth string
		body       map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path, got.auth = r.URL.Path, r.Header.Get("Authorization")
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &got.body)
		if strings.Contains(string(data), "fail me") {
			http.Error(w, strings.Repeat("x", 600), http.StatusForbidden)
			return
		}
		_, _ = io.WriteString(w, `{"output":[{"type":"message","content":[{"type":"output_text","text":"Go 9.9 https://go.example/dl"}]}]}`)
	}))
	defer srv.Close()

	env := map[string]string{
		clineSearchURLEnv:   srv.URL + "/v1/",
		clineSearchModelEnv: "probe-model",
		"OPENAI_API_KEY":    "probe-key",
	}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- ServeSearch(context.Background(), inR, outW, func(k string) string { return env[k] }, srv.Client())
		_ = outW.Close()
	}()
	lines := bufio.NewScanner(outR)
	call := func(msg string) map[string]any {
		t.Helper()
		if _, err := io.WriteString(inW, msg+"\n"); err != nil {
			t.Fatal(err)
		}
		read := make(chan bool, 1)
		go func() { read <- lines.Scan() }()
		select {
		case ok := <-read:
			if !ok {
				t.Fatalf("server closed its output: %v", lines.Err())
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
	// A notification gets no answer, so the next line read is ping's.
	_, _ = io.WriteString(inW, `{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n")
	if r := call(`{"jsonrpc":"2.0","id":"p","method":"ping"}`); r["id"] != "p" || r["result"] == nil {
		t.Fatalf("ping = %v", r)
	}
	list := call(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	tools, _ := list["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "search" {
		t.Fatalf("tools/list = %v, want one tool named search", list)
	}

	result, _ := call(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search","arguments":{"query":"latest go"}}}`)["result"].(map[string]any)
	if result["isError"] == true || !strings.Contains(resultText(result), "go.example") {
		t.Fatalf("tools/call result = %v", result)
	}
	if got.path != "/v1/responses" || got.auth != "Bearer probe-key" || got.body["model"] != "probe-model" {
		t.Errorf("request = %s auth %q model %v", got.path, got.auth, got.body["model"])
	}
	if tools, _ := got.body["tools"].([]any); len(tools) != 1 || tools[0].(map[string]any)["type"] != "web_search" {
		t.Errorf("request tools = %v, want web_search", got.body["tools"])
	}

	failed, _ := call(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"search","arguments":{"query":"fail me"}}}`)["result"].(map[string]any)
	text := resultText(failed)
	if failed["isError"] != true || !strings.Contains(text, "403") || strings.Count(text, "x") > 500 {
		t.Fatalf("failed call = %v, want isError with the status and at most 500 bytes of body", failed)
	}

	if r := call(`{"jsonrpc":"2.0","id":5,"method":"resources/list"}`); r["error"] == nil {
		t.Fatalf("unknown method = %v, want an error", r)
	}
	_ = inW.Close()
	if err := <-done; err != nil {
		t.Fatalf("ServeSearch: %v", err)
	}
}

func resultText(result map[string]any) string {
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		return ""
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	return text
}
