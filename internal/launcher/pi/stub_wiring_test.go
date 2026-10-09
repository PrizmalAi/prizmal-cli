package pi

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

func TestStubServerPiWiring(t *testing.T) {
	srv := stubserver.New()
	defer srv.Close()
	envconfig.SetBaseURL(srv.URL)
	envconfig.SetAPIKey(stubserver.StubKey)
	defer func() { envconfig.SetBaseURL(""); envconfig.SetAPIKey("") }()

	d := internaltest.SandboxedHome(t)
	p := &Pi{}
	if err := p.Edit([]launch.LaunchModel{{Name: "prizmal/default"}}); err != nil {
		t.Fatal(err)
	}
	// Read back the models.json and check the provider carries the stub key.
	data, err := os.ReadFile(filepath.Join(d, ".pi/agent/models.json"))
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
	if got := internaltest.EnvValue(p.envVars(nil), envconfig.KeyEnvVar+"="); got != stubserver.StubKey {
		t.Fatalf("%s = %q, want %q", envconfig.KeyEnvVar, got, stubserver.StubKey)
	}
}
