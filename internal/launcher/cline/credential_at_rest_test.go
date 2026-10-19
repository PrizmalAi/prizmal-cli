package cline

import (
	"path/filepath"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

// clineProvPath is the providers.json the credential-at-rest tests inspect,
// relative to the sandboxed home.
var clineProvPath = filepath.Join(".cline", "data", "settings", "providers.json")

// seedLegacyKeyedConfig writes the provider config an older build of the
// cline launcher left behind, with the key itself in it, and returns its path.
// The current launcher writes no key, so this is the only way one is still on
// disk for Edit to overwrite or Restore to remove.
func seedLegacyKeyedConfig(t *testing.T, home string) string {
	t.Helper()
	internaltest.SandboxedSeed(t, home, clineProvPath, `{"version":1,"lastUsedProvider":"openai-compatible","providers":{`+
		`"openai-compatible":{"settings":{"provider":"openai-compatible","model":"probe-model",`+
		`"baseUrl":"`+clineProviderBaseURL()+`","apiKey":"`+internaltest.FakeKeyAtRest+`"},"tokenSource":"manual"}}}`)
	return filepath.Join(home, clineProvPath)
}

// A plain (non---persist) run must leave nothing key-shaped in the home
// directory. cline's openai-compatible provider falls back to OPENAI_API_KEY
// when its settings carry no apiKey.
func TestClineLeavesNoKeyOnDisk(t *testing.T) {
	home := internaltest.SandboxedHome(t)
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)

	(&Cline{}).envVars()
	if err := (&Cline{}).Edit([]launch.LaunchModel{{Name: "probe-model"}}); err != nil {
		t.Fatalf("configure cline: %v", err)
	}
	internaltest.AssertNoKeyUnder(t, home, "cline")
}

// The key reaches cline through OPENAI_API_KEY.
func TestClineEnvVarsCarryAPIKey(t *testing.T) {
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)

	if got := internaltest.EnvValue((&Cline{}).envVars(), "OPENAI_API_KEY="); got != internaltest.FakeKeyAtRest {
		t.Fatalf("OPENAI_API_KEY = %q, want the configured key", got)
	}
}

// The upgrade path: an older build wrote the key itself into providers.json,
// and the first launch of this one overwrites it.
func TestClineOverwriteLeavesNoKeyInBackupDir(t *testing.T) {
	home := internaltest.SandboxedHome(t)
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)

	target := seedLegacyKeyedConfig(t, home)
	internaltest.AssertOverwriteLeavesNoKeyInBackup(t, home, target, func() error {
		return (&Cline{}).Edit([]launch.LaunchModel{{Name: "other-probe-model"}})
	})
}

// Restore takes the legacy key off the disk. Cline needs the undo because it
// has no environment channel for a custom provider, so `prizmal --restore
// cline` is the only way back off the disk.
func TestClineRestoreRemovesCredential(t *testing.T) {
	home := internaltest.SandboxedHome(t)
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)

	seedLegacyKeyedConfig(t, home)
	internaltest.AssertRestoreRemovesKey(t, home, func() error {
		_, err := (&Cline{}).Restore()
		return err
	})
}

func TestClineRestorePreservesForeignProviders(t *testing.T) {
	home := internaltest.SandboxedHome(t)
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)

	internaltest.SandboxedSeed(t, home, clineProvPath, `{"version":1,"lastUsedProvider":"openrouter","providers":{`+
		`"openrouter":{"settings":{"provider":"openrouter","model":"their-model","apiKey":"their-own-fake-key"},"tokenSource":"manual"}}}`)

	c := &Cline{}
	if err := c.Edit([]launch.LaunchModel{{Name: "probe-model"}}); err != nil {
		t.Fatalf("Cline.Edit: %v", err)
	}
	if _, err := c.Restore(); err != nil {
		t.Fatalf("Cline.Restore: %v", err)
	}

	if hits := internaltest.FilesContainingKey(t, home); len(hits) > 0 {
		t.Fatalf("Cline.Restore left the provider key on disk in: %v", hits)
	}

	providers, _ := internaltest.SandboxedJSON(t, home, clineProvPath)["providers"].(map[string]any)
	other, _ := providers["openrouter"].(map[string]any)
	if other == nil {
		t.Fatal("Cline.Restore removed a provider it does not manage")
	}
	settings, _ := other["settings"].(map[string]any)
	if got, _ := settings["apiKey"].(string); got != "their-own-fake-key" {
		t.Fatalf("foreign provider apiKey = %q, want it untouched", got)
	}
}

// TestClineRestoreKeepsUserOwnOpenAICompatibleEntry is the conservative half.
// "openai-compatible" is cline's own provider id, not a prizmal-private one,
// so an entry under that key pointing somewhere other than the Switch belongs
// to the user and restore must not touch it.
func TestClineRestoreKeepsUserOwnOpenAICompatibleEntry(t *testing.T) {
	home := internaltest.SandboxedHome(t)
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)

	internaltest.SandboxedSeed(t, home, clineProvPath, `{"version":1,"lastUsedProvider":"openai-compatible","providers":{`+
		`"openai-compatible":{"settings":{"provider":"openai-compatible","model":"their-model",`+
		`"baseUrl":"https://their-endpoint.example/v1","apiKey":"their-own-fake-key",`+
		`"headers":{"x-their-header":"value"}},"tokenSource":"manual"}}}`)

	if _, err := (&Cline{}).Restore(); err != nil {
		t.Fatalf("Cline.Restore: %v", err)
	}

	cfg := internaltest.SandboxedJSON(t, home, clineProvPath)
	providers, _ := cfg["providers"].(map[string]any)
	entry, _ := providers[clineApiProvider].(map[string]any)
	if entry == nil {
		t.Fatal("Cline.Restore deleted a provider entry prizmal never wrote")
	}
	settings, _ := entry["settings"].(map[string]any)
	if got, _ := settings["apiKey"].(string); got != "their-own-fake-key" {
		t.Fatalf("user apiKey = %q, want it untouched", got)
	}
	if got, _ := settings["baseUrl"].(string); got != "https://their-endpoint.example/v1" {
		t.Fatalf("user baseUrl = %q, want it untouched", got)
	}
	if _, ok := settings["headers"].(map[string]any); !ok {
		t.Fatal("Cline.Restore dropped the user's own headers")
	}
	if got, _ := cfg["lastUsedProvider"].(string); got != clineApiProvider {
		t.Fatalf("lastUsedProvider = %q, want the user's own selection left alone", got)
	}
}
