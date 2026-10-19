package cline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
)

// cdkSandboxHome points HOME and USERPROFILE at a temp dir so no adapter ever
// reads or writes a real user configuration during tests.
func cdkSandboxHome(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	t.Setenv("HOME", d)
	t.Setenv("USERPROFILE", d)
	return d
}

// findPlaceholders reports whether any of the legacy placeholder values appear
// in the serialized output. The credential wiring must never emit them.
func findPlaceholders(blob string) []string {
	var hits []string
	for _, p := range []string{"ollama", "harness-launch", "ollama-local"} {
		if strings.Contains(blob, p) {
			hits = append(hits, p)
		}
	}
	return hits
}

// --- cline ---------------------------------------------------------------

// TestClineWritesProviderWithoutKey pins where the key goes: cline reads it
// from OPENAI_API_KEY on the child environment, so the providers.json entry
// the launcher writes carries no apiKey.
func TestClineWritesProviderWithoutKey(t *testing.T) {
	d := cdkSandboxHome(t)
	envconfig.SetAPIKey("sk-cline-real")

	cfg := map[string]any{"providers": map[string]any{}}
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
	providers := out["providers"].(map[string]any)
	prov := providers[clineApiProvider].(map[string]any)
	if prov["tokenSource"] != "manual" {
		t.Fatalf("tokenSource = %v, want manual", prov["tokenSource"])
	}
	settings := prov["settings"].(map[string]any)
	if settings["provider"] != "openai-compatible" {
		t.Fatalf("provider = %v, want openai-compatible", settings["provider"])
	}
	if v, ok := settings["apiKey"]; ok {
		t.Fatalf("apiKey = %v, want no apiKey: the key reaches cline on the environment", v)
	}
	if out["lastUsedProvider"] != "openai-compatible" {
		t.Fatalf("lastUsedProvider = %v, want openai-compatible", out["lastUsedProvider"])
	}
	if hits := findPlaceholders(string(data)); len(hits) > 0 {
		t.Fatalf("unexpected placeholders in cline output: %v", hits)
	}
}

func TestClineEmptyKeyNoPlaceholder(t *testing.T) {
	d := cdkSandboxHome(t)
	envconfig.SetAPIKey("")

	cfg := map[string]any{"providers": map[string]any{}}
	configPath := filepath.Join(d, "providers.json")
	if err := writeClineProvidersConfig(configPath, cfg, "some-model"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(configPath)
	var out map[string]any
	_ = json.Unmarshal(data, &out)
	prov := out["providers"].(map[string]any)[clineApiProvider].(map[string]any)
	settings := prov["settings"].(map[string]any)
	// "ollama" is the provider identifier, not a credential; only auth values matter.
	if v, ok := settings["apiKey"]; ok {
		t.Fatalf("empty-key produced apiKey %q, want none", v)
	}
}

func TestClineReadBackOpenAiCompatibleProvider(t *testing.T) {
	// clineProviderModel must resolve a model from the openai-compatible
	// provider we register (stored under the provider key).
	d := cdkSandboxHome(t)

	providersPath := clineProvidersPath(d)
	if err := os.MkdirAll(filepath.Dir(providersPath), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{
		"version":          1,
		"lastUsedProvider": "prizmal",
		"providers": map[string]any{
			"prizmal": map[string]any{
				"settings": map[string]any{"provider": "openai-compatible", "model": "legacy-model", "baseUrl": "x"},
			},
		},
	}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(providersPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	got := clineProviderModel(d)
	if got != "legacy-model" {
		t.Fatalf("clineProviderModel() = %q, want legacy-model", got)
	}
}

// TestClineLastUsedProviderHasEntry pins the invariant that makes cline pick up
// our credentials at all: cline resolves the active provider as
// providers[lastUsedProvider], so the key the entry is written under MUST equal
// lastUsedProvider. Writing the entry under any other key leaves cline to fall
// back to its own default account provider, which 401s.
func TestClineLastUsedProviderHasEntry(t *testing.T) {
	d := cdkSandboxHome(t)
	envconfig.SetAPIKey("sk-cline-invariant")

	cfg := map[string]any{"providers": map[string]any{}}
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

	lastUsed, _ := out["lastUsedProvider"].(string)
	if lastUsed == "" {
		t.Fatalf("lastUsedProvider is unset in %s", data)
	}
	providers, _ := out["providers"].(map[string]any)
	provider, ok := providers[lastUsed].(map[string]any)
	if !ok {
		keys := make([]string, 0, len(providers))
		for k := range providers {
			keys = append(keys, k)
		}
		t.Fatalf("providers[%q] is missing; cline resolves the active provider by that key and would fall back to its default account. present keys: %v", lastUsed, keys)
	}

	settings, _ := provider["settings"].(map[string]any)
	if got, _ := settings["baseUrl"].(string); got != clineProviderBaseURL() {
		t.Fatalf("providers[%q].settings.baseUrl = %q, want %q", lastUsed, got, clineProviderBaseURL())
	}
	if got, _ := settings["model"].(string); got != "some-model" {
		t.Fatalf("providers[%q].settings.model = %q, want some-model", lastUsed, got)
	}
}

// TestClineMigratesLegacyProviderKey covers the upgrade path: an install that
// still has the entry under the legacy key must end up with a single entry
// under the api-provider key, so cline stops falling back to its own account.
func TestClineMigratesLegacyProviderKey(t *testing.T) {
	d := cdkSandboxHome(t)
	envconfig.SetAPIKey("sk-migrated")

	cfg := map[string]any{
		"version":          float64(1),
		"lastUsedProvider": clineApiProvider,
		"providers": map[string]any{
			clineLegacyProvider: map[string]any{
				"settings":    map[string]any{"provider": clineApiProvider, "model": "old-model", "baseUrl": "http://old/v1", "apiKey": "sk-old"},
				"tokenSource": "manual",
			},
			// Cline manufactures a blank entry under the api-provider key when
			// lastUsedProvider names a key with no entry; it has no baseUrl and
			// defaults to OpenAI. Our write must overwrite it.
			clineApiProvider: map[string]any{
				"settings":    map[string]any{"provider": clineApiProvider, "model": "gpt-4o"},
				"tokenSource": "migration",
			},
		},
	}
	configPath := filepath.Join(d, "providers.json")
	if err := writeClineProvidersConfig(configPath, cfg, "new-model"); err != nil {
		t.Fatalf("writeClineProvidersConfig: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	providers, _ := out["providers"].(map[string]any)
	if _, stale := providers[clineLegacyProvider]; stale {
		t.Fatalf("legacy %q entry survived the write; cline cannot select that key and it would keep a stale copy of the api key", clineLegacyProvider)
	}
	provider, ok := providers[clineApiProvider].(map[string]any)
	if !ok {
		t.Fatalf("providers[%q] missing after migration", clineApiProvider)
	}
	settings, _ := provider["settings"].(map[string]any)
	if v, ok := settings["apiKey"]; ok {
		t.Fatalf("apiKey = %v, want none: the legacy entry's key must not carry over", v)
	}
	if got, _ := settings["baseUrl"].(string); got != clineProviderBaseURL() {
		t.Fatalf("baseUrl = %q, want %q", got, clineProviderBaseURL())
	}
	if got, _ := settings["model"].(string); got != "new-model" {
		t.Fatalf("model = %q, want new-model", got)
	}
	if got, _ := provider["tokenSource"].(string); got != "manual" {
		t.Fatalf("tokenSource = %q, want manual", got)
	}
}

// TestClineNeverPairsSwitchKeyWithForeignHost guards the credential-disclosure
// case that a clean CI runner cannot reach: a user who already has a genuine
// openai-compatible provider configured in cline, pointed at OpenAI with their
// own key. We reuse that entry, so if baseUrl were ever carried over from it
// instead of being overwritten, the entry would end up holding the Switch key
// next to an api.openai.com baseUrl and the next launch would hand a live
// Switch credential to OpenAI. The key and the baseUrl must always be written
// together.
func TestClineNeverPairsSwitchKeyWithForeignHost(t *testing.T) {
	const switchKey = "sk-switch-secret"
	const foreignHost = "https://api.openai.com/v1"

	d := cdkSandboxHome(t)
	envconfig.SetBaseURL("https://switch.example.test")
	envconfig.SetAPIKey(switchKey)
	t.Cleanup(func() { envconfig.SetBaseURL(""); envconfig.SetAPIKey("") })

	// A real, non-blank openai-compatible entry: the user's own OpenAI setup.
	cfg := map[string]any{
		"version":          float64(1),
		"lastUsedProvider": clineApiProvider,
		"providers": map[string]any{
			clineApiProvider: map[string]any{
				"settings": map[string]any{
					"provider": clineApiProvider,
					"model":    "gpt-4o",
					"baseUrl":  foreignHost,
					"apiKey":   "sk-decoy-user-openai-key",
				},
				"tokenSource": "manual",
				"updatedAt":   "2020-01-01T00:00:00Z",
			},
		},
	}
	configPath := filepath.Join(d, "providers.json")
	if err := writeClineProvidersConfig(configPath, cfg, "prizmal/default"); err != nil {
		t.Fatalf("writeClineProvidersConfig: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}

	lastUsed, _ := out["lastUsedProvider"].(string)
	providers, _ := out["providers"].(map[string]any)
	active, ok := providers[lastUsed].(map[string]any)
	if !ok {
		t.Fatalf("providers[%q] missing", lastUsed)
	}
	activeSettings, _ := active["settings"].(map[string]any)
	if got, _ := activeSettings["baseUrl"].(string); got != clineProviderBaseURL() {
		t.Fatalf("active entry baseUrl = %q, want the Switch URL %q; a carried-over baseUrl would send the Switch key to a foreign host", got, clineProviderBaseURL())
	}

	// No entry anywhere in the file may hold the Switch key next to a host that
	// is not the Switch.
	for name, raw := range providers {
		entry, _ := raw.(map[string]any)
		settings, _ := entry["settings"].(map[string]any)
		key, _ := settings["apiKey"].(string)
		if key != switchKey {
			continue
		}
		base, _ := settings["baseUrl"].(string)
		if base != clineProviderBaseURL() {
			t.Fatalf("providers[%q] holds the Switch key next to baseUrl %q; the Switch credential must never be paired with a non-Switch host", name, base)
		}
	}

	// The foreign host must not survive anywhere in the serialized config.
	if strings.Contains(string(data), "api.openai.com") {
		t.Fatalf("api.openai.com survived in the written config:\n%s", data)
	}
}
