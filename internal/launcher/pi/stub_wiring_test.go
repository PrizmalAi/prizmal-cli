package pi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/launchtest"
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
	envconfig.SetDeviceMode(false)
	defer func() { envconfig.SetBaseURL(""); envconfig.SetAPIKey("") }()

	home := internaltest.SandboxedHome(t)
	p := &Pi{}

	// The launch's sidecar is what the extension registers the provider from,
	// so it is the config under test: the stub's endpoint, the key reference,
	// and the models the launch resolved.
	l, err := piLaunchFor("prizmal/default", []launch.LaunchModel{{Name: "prizmal/default"}})
	if err != nil {
		t.Fatalf("piLaunchFor: %v", err)
	}
	t.Cleanup(l.clean)
	data, err := os.ReadFile(l.configPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg piDeviceConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	wantBase := launchtest.StubEndpoint(srv.URL) + "/v1"
	if cfg.BaseURL != wantBase {
		t.Fatalf("sidecar baseURL = %q, want %q", cfg.BaseURL, wantBase)
	}
	if cfg.APIKey != piAPIKeyReference {
		t.Fatalf("sidecar apiKey = %q, want %q", cfg.APIKey, piAPIKeyReference)
	}
	if got := internaltest.EnvValue(p.envVars(l), envconfig.KeyEnvVar+"="); got != stubserver.StubKey {
		t.Fatalf("%s = %q, want %q", envconfig.KeyEnvVar, got, stubserver.StubKey)
	}

	// --persist writes the provider to models.json, and that entry carries the
	// stub's endpoint and the same key reference.
	if err := p.Edit([]launch.LaunchModel{{Name: "prizmal/default"}}); err != nil {
		t.Fatal(err)
	}
	persisted, err := launch.ReadJSONFile(filepath.Join(home, piModelsPath))
	if err != nil || persisted == nil {
		t.Fatalf("read the persisted models.json: %v", err)
	}
	providers, _ := persisted["providers"].(map[string]any)
	prizmal, _ := providers["prizmal"].(map[string]any)
	if prizmal == nil {
		t.Fatal("no prizmal provider in models.json")
	}
	if got, _ := prizmal["baseUrl"].(string); got != launchtest.StubEndpoint(srv.URL)+"/v1" {
		t.Fatalf("models.json baseUrl = %q, want the stub's endpoint", got)
	}
	if apiKey, _ := prizmal["apiKey"].(string); apiKey != piAPIKeyReference {
		t.Fatalf("models.json apiKey = %q, want %q", apiKey, piAPIKeyReference)
	}
}
