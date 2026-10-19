package cline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
)

// TestClineDropsPreExistingSettingsKeys pins the settings-replacement rule:
// the launcher must replace cline's settings map for the entry it writes the
// Switch key into, not mutate it.
//
// "headers" is the one that is demonstrably live. cline parses providers.json
// against a schema that names it (a record of string to string on the provider
// entry's settings) and puts every pair on the wire, so a header a user set for
// their own endpoint travels to the Switch once we repoint baseUrl at it. The
// other three keys here are the ones the settings-replacement ticket named;
// cline strips them as
// unknown, and they are kept as cheap cover in case a later cline release
// promotes them.
func TestClineDropsPreExistingSettingsKeys(t *testing.T) {
	d := cdkSandboxHome(t)
	envconfig.SetAPIKey("sk-cline-real")

	cfg := map[string]any{
		"version":          1,
		"lastUsedProvider": clineApiProvider,
		"providers": map[string]any{
			clineApiProvider: map[string]any{
				"settings": map[string]any{
					"provider": clineApiProvider,
					"model":    "user-model",
					"baseUrl":  "https://user-endpoint.example/v1",
					"apiKey":   "sk-user-own-key",
					"headers": map[string]any{
						"X-User-Secret": "leak-me",
					},
					"customHeaders": map[string]any{
						"X-User-Secret": "leak-me",
					},
					"openAiHeaders": map[string]any{
						"X-Org": "user-org",
					},
					"azureApiVersion": "2024-02-01",
					"openAiBaseUrl":   "https://user-endpoint.example",
				},
			},
		},
	}

	configPath := filepath.Join(d, "providers.json")
	if err := writeClineProvidersConfig(configPath, cfg, "some-model"); err != nil {
		t.Fatalf("writeClineProvidersConfig: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read written config: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("parse written config: %v", err)
	}
	settings := out["providers"].(map[string]any)[clineApiProvider].(map[string]any)["settings"].(map[string]any)

	for _, key := range []string{"headers", "customHeaders", "openAiHeaders", "azureApiVersion", "openAiBaseUrl"} {
		if v, ok := settings[key]; ok {
			t.Errorf("settings[%q] survived the launcher write as %v; the entry we put the Switch key on must carry only the fields we set", key, v)
		}
	}

	// The three fields the launcher owns must still be exactly what it set.
	if settings["provider"] != clineApiProvider {
		t.Errorf("provider = %v, want %v", settings["provider"], clineApiProvider)
	}
	if settings["model"] != "some-model" {
		t.Errorf("model = %v, want some-model", settings["model"])
	}
	if settings["baseUrl"] != clineProviderBaseURL() {
		t.Errorf("baseUrl = %v, want %v", settings["baseUrl"], clineProviderBaseURL())
	}
	if v, ok := settings["apiKey"]; ok {
		t.Errorf("apiKey = %v, want none: the user's key must not travel to the Switch and ours rides the environment", v)
	}
}
