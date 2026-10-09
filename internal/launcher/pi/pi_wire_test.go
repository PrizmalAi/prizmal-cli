package pi

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

func piModelsAPIKey(t *testing.T) (string, map[string]any) {
	t.Helper()
	home := internaltest.SandboxedHome(t)
	p := &Pi{}
	if err := p.Edit(launchtest.WireKeyTestModel()); err != nil {
		t.Fatalf("Pi.Edit: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".pi", "agent", "models.json"))
	if err != nil {
		t.Fatalf("read pi models.json: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal pi models.json: %v", err)
	}
	providers, _ := cfg["providers"].(map[string]any)
	ollama, _ := providers["prizmal"].(map[string]any)
	key, _ := ollama["apiKey"].(string)
	return key, ollama
}

// TestPiReferencesAPIKeyEnv pins where the key goes: models.json names
// $PRIZMAL_SWITCH_KEY and pi resolves it from the child environment.
func TestPiReferencesAPIKeyEnv(t *testing.T) {
	envconfig.SetAPIKey("sk-pi")
	key, _ := piModelsAPIKey(t)
	if key != "$PRIZMAL_SWITCH_KEY" {
		t.Fatalf("pi provider.prizmal.apiKey = %q, want $PRIZMAL_SWITCH_KEY", key)
	}
	envconfig.SetAPIKey("")
}

func TestPiEmptyKeyIsEmptyNotPlaceholder(t *testing.T) {
	internaltest.ClearCredentialEnv(t)
	envconfig.SetAPIKey("")
	key, ollama := piModelsAPIKey(t)
	if key != piAPIKeyReference {
		t.Fatalf("empty key: pi provider.prizmal.apiKey is not the reference %q", piAPIKeyReference)
	}
	for field, v := range ollama {
		if s, ok := v.(string); ok && (s == "ollama" || s == "harness-launch" || s == "ollama-local") {
			t.Fatalf("pi ollama %q holds placeholder %q with empty key: %+v", field, s, ollama)
		}
	}
	// The provider block itself must not embed any placeholder credential value.
	raw, err := json.Marshal(ollama)
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"\"apiKey\":\"ollama\"", "\"apiKey\":\"harness-launch\"", "\"apiKey\":\"ollama-local\""} {
		if strings.Contains(string(raw), banned) {
			t.Fatalf("pi config embeds placeholder %s: %s", banned, raw)
		}
	}
}

func TestPiProviderKeyIsPrizmalNotOllama(t *testing.T) {
	envconfig.SetAPIKey("sk-pi")
	key, provider := piModelsAPIKey(t)
	if key != piAPIKeyReference {
		t.Fatalf("pi provider.apiKey = %q, want %q", key, piAPIKeyReference)
	}
	// The provider block is stored under the 'prizmal' key, not 'ollama'.
	raw, _ := json.Marshal(provider)
	if strings.Contains(string(raw), "\"apiKey\":\"ollama\"") || strings.Contains(string(raw), "\"ollama\"") {
		t.Fatalf("pi provider embeds ollama id: %s", raw)
	}
	envconfig.SetAPIKey("")
}

func TestPiWritesCurrentProviderOnly(t *testing.T) {
	envconfig.SetAPIKey("sk-migrate")
	home := internaltest.SandboxedHome(t)
	dir := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := &Pi{}
	if err := p.Edit(launchtest.WireKeyTestModel()); err != nil {
		t.Fatalf("Pi.Edit: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(got, &cfg); err != nil {
		t.Fatal(err)
	}
	providers, _ := cfg["providers"].(map[string]any)
	prizmal, ok := providers["prizmal"].(map[string]any)
	if !ok {
		t.Fatalf("expected a 'prizmal' provider, got %v", providers)
	}
	if k, _ := prizmal["apiKey"].(string); k != piAPIKeyReference {
		t.Fatalf("provider apiKey = %q, want %q", k, piAPIKeyReference)
	}
	envconfig.SetAPIKey("")
}
