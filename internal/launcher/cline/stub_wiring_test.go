package cline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/PrizmalAi/prizmal-cli/internal/stubserver"
)

// These tests prove each harness's config builder carries the stub server's
// URL and key when pointed at it through envconfig. They do not launch the
// real harness binary (that is CI-only); they prove the wiring is correct
// against a real HTTP endpoint shape.

func TestStubServerClineWiring(t *testing.T) {
	srv := stubserver.New()
	defer srv.Close()
	envconfig.SetBaseURL(srv.URL)
	envconfig.SetAPIKey(stubserver.StubKey)
	defer func() { envconfig.SetBaseURL(""); envconfig.SetAPIKey("") }()

	d := internaltest.SandboxedHome(t)
	c := &Cline{}
	if err := c.Edit([]launch.LaunchModel{{Name: "prizmal/default"}}); err != nil {
		t.Fatal(err)
	}
	// Read providers.json: the key rides the environment, not the file.
	data, err := os.ReadFile(filepath.Join(d, ".cline/data/settings/providers.json"))
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
	if got := internaltest.EnvValue(c.envVars(), "OPENAI_API_KEY="); got != stubserver.StubKey {
		t.Fatalf("OPENAI_API_KEY = %q, want %q", got, stubserver.StubKey)
	}
	// The Prizmal endpoint must register as Cline's OpenAI Compatible provider
	// type, never the Ollama provider type.
	if ptype, _ := settings["provider"].(string); ptype != clineApiProvider {
		t.Fatalf("settings.provider = %q, want %q (openai-compatible, not ollama)", ptype, clineApiProvider)
	}
	// The global state must use the OpenAI Compatible act/plan mode keys, not
	// the legacy Ollama ones.
	gsData, err := os.ReadFile(filepath.Join(d, ".cline/data/globalState.json"))
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
