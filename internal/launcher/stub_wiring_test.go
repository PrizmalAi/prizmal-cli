package launch

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/model"
	"github.com/PrizmalAi/prizmal-cli/internal/stubserver"
)

// readFile is a test helper that reads a file at an absolute path.
func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
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
	if val != srv.URL {
		t.Fatalf("ANTHROPIC_BASE_URL = %q, want %q", val, srv.URL)
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
	if baseURL != srv.URL+"/v1" {
		t.Fatalf("baseURL = %q, want %q", baseURL, srv.URL+"/v1")
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
	if got := envValue(p.envVars(), envconfig.KeyEnvVar+"="); got != stubserver.StubKey {
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

	ResetModelCatalog()
	t.Cleanup(ResetModelCatalog)

	// The launch reader, which is the posture a launch has: degrade, never
	// fail. Its rows are looked up the way every launch looks one up.
	catalog := BestEffortCatalog(context.Background(), io.Discard)
	models := LaunchModels("prizmal/stub", catalog, true)
	vision := catalogEntryForTest(t, models, "prizmal/stub-vision")
	file := catalogEntryForTest(t, models, "prizmal/stub-file")
	unknown := catalogEntryForTest(t, models, "prizmal/stub-unknown")

	if models[0].HasCapability(model.CapabilityVision) {
		t.Fatalf("prizmal/stub capabilities = %v, want no vision", models[0].Capabilities)
	}
	if len(models[0].Capabilities) == 0 {
		t.Fatal("prizmal/stub capabilities are empty, so a known text-only entry is indistinguishable from an unknown one")
	}
	if !vision.HasCapability(model.CapabilityVision) {
		t.Fatalf("prizmal/stub-vision capabilities = %v, want vision", vision.Capabilities)
	}
	if !file.HasCapability(model.CapabilityDocument) {
		t.Fatalf("prizmal/stub-file capabilities = %v, want document", file.Capabilities)
	}
	if file.HasCapability(model.CapabilityVision) {
		t.Fatalf("prizmal/stub-file capabilities = %v, want no vision", file.Capabilities)
	}
	if len(unknown.Capabilities) != 0 {
		t.Fatalf("prizmal/stub-unknown capabilities = %v, want unknown", unknown.Capabilities)
	}

	// The four shapes reach opencode as text-only, image, pdf, and permissive.
	for _, tc := range []struct {
		m    LaunchModel
		want []string
	}{
		{models[0], []string{"text"}},
		{vision, []string{"text", "image"}},
		{file, []string{"text", "pdf"}},
		{unknown, []string{"text", "image", "pdf"}},
	} {
		if got := openCodeInputModalities(tc.m); !slices.Equal(got, tc.want) {
			t.Fatalf("opencode input modalities for %q = %v, want %v", tc.m.Name, got, tc.want)
		}
	}
}

// catalogEntryForTest finds a row in a launch's model list by name, failing the
// test when the name is not there.
func catalogEntryForTest(t *testing.T, models []LaunchModel, name string) LaunchModel {
	t.Helper()
	entry, ok := findCatalogModel(models, name)
	if !ok {
		t.Fatalf("no model %q in %v", name, launchModelNames(models))
	}
	return entry
}
