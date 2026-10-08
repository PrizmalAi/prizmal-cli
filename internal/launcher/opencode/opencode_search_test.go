package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/launchtest"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
)

// openCodeInlineConfig renders buildInlineConfig for the shared test model and
// returns it decoded.
func openCodeInlineConfig(t *testing.T) map[string]any {
	t.Helper()
	content, err := buildInlineConfig(launchtest.WireKeyTestModel()[0], launchtest.WireKeyTestModel())
	if err != nil {
		t.Fatalf("buildInlineConfig: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(content), &cfg); err != nil {
		t.Fatalf("unmarshal opencode config: %v", err)
	}
	return cfg
}

// envLookup returns the value of name in a KEY=VALUE slice, and whether it was set.
func envLookup(env []string, name string) (string, bool) {
	for _, entry := range env {
		if after, ok := strings.CutPrefix(entry, name+"="); ok {
			return after, true
		}
	}
	return "", false
}

// TestOpenCodeConfigDeniesBuiltinWebSearch covers the built-in websearch tool,
// which reaches https://search.parallel.ai/mcp and https://mcp.exa.ai/mcp from
// opencode's own runtime. Both the `tools` block and the `permission` block
// remove it from the model's toolset.
func TestOpenCodeConfigDeniesBuiltinWebSearch(t *testing.T) {
	cfg := openCodeInlineConfig(t)

	tools, ok := cfg["tools"].(map[string]any)
	if !ok {
		t.Fatalf("opencode config has no 'tools' block; got keys %v", keysOf(cfg))
	}
	if enabled, ok := tools["websearch"].(bool); !ok || enabled {
		t.Fatalf("tools.websearch = %v, want false", tools["websearch"])
	}

	perm, ok := cfg["permission"].(map[string]any)
	if !ok {
		t.Fatalf("opencode config has no 'permission' block; got keys %v", keysOf(cfg))
	}
	if action, _ := perm["websearch"].(string); action != "deny" {
		t.Fatalf("permission.websearch = %q, want deny", action)
	}
}

// TestOpenCodeLaunchEnablesNoHostedSearch guards the operator ruling: prizmal
// must not switch on opencode's hosted search providers.
func TestOpenCodeLaunchEnablesNoHostedSearch(t *testing.T) {
	internaltest.SandboxedHome(t)
	o := &OpenCode{}
	env := o.childEnv("wire-test-model", launchtest.WireKeyTestModel())

	for _, name := range []string{
		"OPENCODE_ENABLE_PARALLEL",
		"OPENCODE_ENABLE_EXA",
		"OPENCODE_EXPERIMENTAL_PARALLEL",
		"OPENCODE_EXPERIMENTAL_EXA",
		"OPENCODE_WEBSEARCH_PROVIDER",
		"PARALLEL_API_KEY",
		"EXA_API_KEY",
	} {
		if value, ok := envLookup(env, name); ok {
			t.Fatalf("launch sets %s=%q; prizmal must not enable opencode's hosted search", name, value)
		}
	}
}

// TestOpenCodeEnvVarsWireSearchToolToSwitch checks the launch environment
// points the generated tool at the Switch, with the key passed out of band.
func TestOpenCodeEnvVarsWireSearchToolToSwitch(t *testing.T) {
	internaltest.SandboxedHome(t)
	envconfig.SetBaseURL("https://switch.example/")
	envconfig.SetAPIKey("sk-search")
	t.Cleanup(func() {
		envconfig.SetBaseURL("")
		envconfig.SetAPIKey("")
	})

	o := &OpenCode{}
	env := o.envVars("wire-test-model", launchtest.WireKeyTestModel())

	dir, ok := envLookup(env, "OPENCODE_CONFIG_DIR")
	if !ok || dir == "" {
		t.Fatalf("launch does not set OPENCODE_CONFIG_DIR; the custom tool would never load")
	}
	if _, err := os.Stat(filepath.Join(dir, "tool", openCodeSearchToolID+".ts")); err != nil {
		t.Fatalf("search tool missing from OPENCODE_CONFIG_DIR: %v", err)
	}

	if url, _ := envLookup(env, openCodeSearchURLEnv); url != "https://switch.example/v1" {
		t.Fatalf("%s = %q, want https://switch.example/v1", openCodeSearchURLEnv, url)
	}
	if key, _ := envLookup(env, openCodeSearchKeyEnv); key != "sk-search" {
		t.Fatalf("%s = %q, want sk-search", openCodeSearchKeyEnv, key)
	}
	if model, _ := envLookup(env, openCodeSearchModelEnv); model != "wire-test-model" {
		t.Fatalf("%s = %q, want wire-test-model", openCodeSearchModelEnv, model)
	}
}

// TestOpenCodeSearchToolIDIsNotWebsearch pins the tool id away from
// "websearch". opencode's registry applies its hosted-search gate to every tool
// carrying that id, custom ones included, so a tool named websearch is dropped
// from the toolset whenever the provider is not opencode's own.
func TestOpenCodeSearchToolIDIsNotWebsearch(t *testing.T) {
	if openCodeSearchToolID == "websearch" {
		t.Fatal("tool id 'websearch' is filtered out by opencode's registry for non-opencode providers")
	}
	for _, r := range openCodeSearchToolID {
		valid := r == '_' || r == '-' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !valid {
			t.Fatalf("tool id %q contains %q, which is not a valid tool-name character", openCodeSearchToolID, r)
		}
	}
}

// TestOpenCodeSearchToolWritesNoCredentials keeps the Switch key out of the
// file on disk: the tool reads it from the process environment at call time.
func TestOpenCodeSearchToolWritesNoCredentials(t *testing.T) {
	envconfig.SetAPIKey("sk-must-not-land-on-disk")
	envconfig.SetBaseURL("https://switch.example")
	t.Cleanup(func() {
		envconfig.SetAPIKey("")
		envconfig.SetBaseURL("")
	})

	dir := t.TempDir()
	if err := writeOpenCodeSearchTool(dir); err != nil {
		t.Fatalf("writeOpenCodeSearchTool: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "tool", openCodeSearchToolID+".ts"))
	if err != nil {
		t.Fatalf("read generated tool: %v", err)
	}
	source := string(data)

	if strings.Contains(source, "sk-must-not-land-on-disk") {
		t.Fatal("generated tool embeds the Switch key")
	}
	if strings.Contains(source, "switch.example") {
		t.Fatal("generated tool hardcodes the Switch host instead of reading it from the environment")
	}
	for _, name := range []string{openCodeSearchURLEnv, openCodeSearchKeyEnv, openCodeSearchModelEnv} {
		if !strings.Contains(source, name) {
			t.Fatalf("generated tool never reads %s", name)
		}
	}
}

// TestOpenCodeSearchToolCallsNoHostedBackend checks the generated tool talks to
// nothing but the Switch.
func TestOpenCodeSearchToolCallsNoHostedBackend(t *testing.T) {
	dir := t.TempDir()
	if err := writeOpenCodeSearchTool(dir); err != nil {
		t.Fatalf("writeOpenCodeSearchTool: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "tool", openCodeSearchToolID+".ts"))
	if err != nil {
		t.Fatalf("read generated tool: %v", err)
	}
	source := string(data)

	for _, host := range []string{"parallel.ai", "exa.ai", "opencode.ai"} {
		if strings.Contains(source, host) {
			t.Fatalf("generated tool reaches %s; search must route through the Switch", host)
		}
	}
	// A plain object literal with description/args/execute is what opencode's
	// registry recognises, and it needs no dependency to be installed first.
	for _, marker := range []string{"export default", "description", "args", "execute"} {
		if !strings.Contains(source, marker) {
			t.Fatalf("generated tool lacks %q; opencode's registry will not recognise it", marker)
		}
	}
	if strings.Contains(source, "import ") || strings.Contains(source, "require(") {
		t.Fatal("generated tool has an import; it must run before any dependency install completes")
	}
}

// TestOpenCodeSearchToolRewriteIsIdempotent covers relaunches: the file is
// regenerated in place, without accumulating copies or stale content.
func TestOpenCodeSearchToolRewriteIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool", openCodeSearchToolID+".ts")

	if err := writeOpenCodeSearchTool(dir); err != nil {
		t.Fatalf("first write: %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after first write: %v", err)
	}

	if err := os.WriteFile(path, []byte("stale"), 0o600); err != nil {
		t.Fatalf("seed stale content: %v", err)
	}
	if err := writeOpenCodeSearchTool(dir); err != nil {
		t.Fatalf("second write: %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after second write: %v", err)
	}

	if string(first) != string(second) {
		t.Fatal("regenerating the search tool did not restore the expected content")
	}
	entries, err := os.ReadDir(filepath.Join(dir, "tool"))
	if err != nil {
		t.Fatalf("read tool dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("tool dir holds %d entries, want 1", len(entries))
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestOpenCodeEnvReplacesInheritedConfigDir pins the unconditional replacement
// of an inherited OPENCODE_CONFIG_DIR. Exec's last-occurrence convention would
// resolve a duplicate in the appended value's favour anyway, but dropping the
// inherited copy makes the replacement hold regardless of runtime convention.
func TestOpenCodeEnvReplacesInheritedConfigDir(t *testing.T) {
	t.Setenv("OPENCODE_CONFIG_DIR", "/home/user/.config/opencode")

	env := (&OpenCode{}).childEnv("", nil)

	count := 0
	for _, kv := range env {
		if strings.HasPrefix(kv, "OPENCODE_CONFIG_DIR=") {
			count++
			if !strings.Contains(kv, "prizmal") {
				t.Fatalf("OPENCODE_CONFIG_DIR = %q, want the prizmal-owned directory", kv)
			}
		}
	}
	if count != 1 {
		t.Fatalf("OPENCODE_CONFIG_DIR appears %d times, want exactly 1", count)
	}
}
