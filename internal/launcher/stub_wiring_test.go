package launch

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/model"
	"github.com/PrizmalAi/prizmal-cli/internal/stubserver"
)

// readFile is a test helper that reads a file at an absolute path.
func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// stubEndpoint is an httptest server's URL as an endpoint shape spells it.
// httptest binds 127.0.0.1 and the Switch endpoint is always handed out under
// the name a client dials, so the wiring assertions below compare against the
// rewritten form. It is spelled out here instead of read from envconfig so
// that asserting the shape does not restate the function under test.
func stubEndpoint(raw string) string {
	return strings.Replace(raw, "127.0.0.1", "localhost", 1)
}

// stubServerWiring tests that each harness's config builder carries the stub
// server's URL + key when pointed at it via envconfig. These do NOT launch
// the real harness binary (that's CI-only); they prove the wiring is correct
// against a real HTTP endpoint shape.

func TestStubServerClaudeWiring(t *testing.T) {
	srv := stubserver.New()
	defer srv.Close()
	envconfig.SetBaseURL(srv.URL)
	envconfig.SetAPIKey(stubserver.StubKey)
	defer func() { envconfig.SetBaseURL(""); envconfig.SetAPIKey("") }()

	c := &Claude{}
	env := c.envVars()
	val := envValue(env, "ANTHROPIC_BASE_URL=")
	if want := stubEndpoint(srv.URL); val != want {
		t.Fatalf("ANTHROPIC_BASE_URL = %q, want %q", val, want)
	}
	keyVal := envValue(env, "ANTHROPIC_AUTH_TOKEN=")
	if keyVal != stubserver.StubKey {
		t.Fatalf("ANTHROPIC_AUTH_TOKEN = %q, want %q", keyVal, stubserver.StubKey)
	}
}

func TestStubServerOpenCodeWiring(t *testing.T) {
	srv := stubserver.New()
	defer srv.Close()
	envconfig.SetBaseURL(srv.URL)
	envconfig.SetAPIKey(stubserver.StubKey)
	defer func() { envconfig.SetBaseURL(""); envconfig.SetAPIKey("") }()

	models := []LaunchModel{{Name: "prizmal/default"}}
	content, err := buildInlineConfig(models[0], models)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(content), &cfg); err != nil {
		t.Fatal(err)
	}
	provider, _ := cfg["provider"].(map[string]any)
	prizmal, _ := provider["prizmal"].(map[string]any)
	options, _ := prizmal["options"].(map[string]any)
	baseURL, _ := options["baseURL"].(string)
	if want := stubEndpoint(srv.URL) + "/v1"; baseURL != want {
		t.Fatalf("baseURL = %q, want %q", baseURL, want)
	}
	apiKey, _ := options["apiKey"].(string)
	if apiKey != stubserver.StubKey {
		t.Fatalf("apiKey = %q, want %q", apiKey, stubserver.StubKey)
	}
}

func TestStubServerPiWiring(t *testing.T) {
	srv := stubserver.New()
	defer srv.Close()
	envconfig.SetBaseURL(srv.URL)
	envconfig.SetAPIKey(stubserver.StubKey)
	defer func() { envconfig.SetBaseURL(""); envconfig.SetAPIKey("") }()

	d := cdkSandboxHome(t)
	p := &Pi{}
	if err := p.Edit([]LaunchModel{{Name: "prizmal/default"}}); err != nil {
		t.Fatal(err)
	}
	// Read back the models.json and check the provider carries the stub key.
	data, err := readFile(filepath.Join(d, ".pi/agent/models.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	providers, _ := cfg["providers"].(map[string]any)
	prizmal, _ := providers["prizmal"].(map[string]any)
	if prizmal == nil {
		// legacy key might still be used
		prizmal, _ = providers["ollama"].(map[string]any)
	}
	if prizmal == nil {
		t.Fatal("no prizmal or ollama provider in models.json")
	}
	apiKey, _ := prizmal["apiKey"].(string)
	if apiKey != piAPIKeyReference {
		t.Fatalf("apiKey = %q, want %q", apiKey, piAPIKeyReference)
	}
	if got := envValue(p.envVars(nil), envconfig.KeyEnvVar+"="); got != stubserver.StubKey {
		t.Fatalf("%s = %q, want %q", envconfig.KeyEnvVar, got, stubserver.StubKey)
	}
}

func TestStubServerClineWiring(t *testing.T) {
	srv := stubserver.New()
	defer srv.Close()
	envconfig.SetBaseURL(srv.URL)
	envconfig.SetAPIKey(stubserver.StubKey)
	defer func() { envconfig.SetBaseURL(""); envconfig.SetAPIKey("") }()

	d := cdkSandboxHome(t)
	c := &Cline{}
	if err := c.Edit([]LaunchModel{{Name: "prizmal/default"}}); err != nil {
		t.Fatal(err)
	}
	// Read providers.json: the key rides the environment, not the file.
	data, err := readFile(filepath.Join(d, ".cline/data/settings/providers.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	providers, _ := cfg["providers"].(map[string]any)
	provider, _ := providers[clineApiProvider].(map[string]any)
	settings, _ := provider["settings"].(map[string]any)
	if v, ok := settings["apiKey"]; ok {
		t.Fatalf("apiKey = %v, want none", v)
	}
	if got := envValue(c.envVars(), "OPENAI_API_KEY="); got != stubserver.StubKey {
		t.Fatalf("OPENAI_API_KEY = %q, want %q", got, stubserver.StubKey)
	}
	// The Prizmal endpoint must register as Cline's OpenAI Compatible provider
	// type, never the Ollama provider type.
	if ptype, _ := settings["provider"].(string); ptype != clineApiProvider {
		t.Fatalf("settings.provider = %q, want %q (openai-compatible, not ollama)", ptype, clineApiProvider)
	}
	// The global state must use the OpenAI Compatible act/plan mode keys, not
	// the legacy Ollama ones.
	gsData, err := readFile(filepath.Join(d, ".cline/data/globalState.json"))
	if err != nil {
		t.Fatal(err)
	}
	var gs map[string]any
	if err := json.Unmarshal(gsData, &gs); err != nil {
		t.Fatal(err)
	}
	if _, ok := gs["actModeOpenAiCompatibleModelId"]; !ok {
		t.Fatal("globalState missing actModeOpenAiCompatibleModelId")
	}
	if _, ok := gs["actModeOpenAiCompatibleBaseUrl"]; !ok {
		t.Fatal("globalState missing actModeOpenAiCompatibleBaseUrl")
	}
	if _, ok := gs["actModeOllamaModelId"]; ok {
		t.Fatal("globalState still uses legacy actModeOllamaModelId")
	}
	if act, _ := gs["actModeApiProvider"].(string); act != clineApiProvider {
		t.Fatalf("actModeApiProvider = %q, want %q", act, clineApiProvider)
	}
}

// TestStubServerCapabilityWiring walks the whole capability path against the
// stub server's real /v1/models response: fetch, parse, and the opencode entry
// each of the three fixture shapes produces.
func TestStubServerCapabilityWiring(t *testing.T) {
	srv := stubserver.New()
	defer srv.Close()
	envconfig.SetBaseURL(srv.URL)
	envconfig.SetAPIKey(stubserver.StubKey)
	defer func() { envconfig.SetBaseURL(""); envconfig.SetAPIKey("") }()

	models := WithSwitchCapabilities(context.Background(), []LaunchModel{
		{Name: "prizmal/stub"},
		{Name: "prizmal/stub-vision"},
		{Name: "prizmal/stub-file"},
		{Name: "prizmal/stub-unknown"},
	}, io.Discard)

	if models[0].HasCapability(model.CapabilityVision) {
		t.Fatalf("prizmal/stub capabilities = %v, want no vision", models[0].Capabilities)
	}
	if len(models[0].Capabilities) == 0 {
		t.Fatal("prizmal/stub capabilities are empty, so a known text-only entry is indistinguishable from an unknown one")
	}
	if !models[1].HasCapability(model.CapabilityVision) {
		t.Fatalf("prizmal/stub-vision capabilities = %v, want vision", models[1].Capabilities)
	}
	if !models[2].HasCapability(model.CapabilityDocument) {
		t.Fatalf("prizmal/stub-file capabilities = %v, want document", models[2].Capabilities)
	}
	if models[2].HasCapability(model.CapabilityVision) {
		t.Fatalf("prizmal/stub-file capabilities = %v, want no vision", models[2].Capabilities)
	}
	if len(models[3].Capabilities) != 0 {
		t.Fatalf("prizmal/stub-unknown capabilities = %v, want unknown", models[3].Capabilities)
	}

	// The four shapes reach opencode as text-only, image, pdf, and permissive.
	for _, tc := range []struct {
		m    LaunchModel
		want []string
	}{
		{models[0], []string{"text"}},
		{models[1], []string{"text", "image"}},
		{models[2], []string{"text", "pdf"}},
		{models[3], []string{"text", "image", "pdf"}},
	} {
		if got := openCodeInputModalities(tc.m); !slices.Equal(got, tc.want) {
			t.Fatalf("opencode input modalities for %q = %v, want %v", tc.m.Name, got, tc.want)
		}
	}
}
