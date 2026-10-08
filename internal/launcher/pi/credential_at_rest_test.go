package pi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

// piModelsPath is the models.json the credential-at-rest tests inspect,
// relative to the sandboxed home.
var piModelsPath = filepath.Join(".pi", "agent", "models.json")

// seedLegacyKeyedConfig writes the provider config an older build of the pi
// launcher left behind, with the key itself in it, and returns its path. The
// current launcher writes no key, so this is the only way one is still on
// disk for Edit to overwrite or Restore to remove.
func seedLegacyKeyedConfig(t *testing.T, home string) string {
	t.Helper()
	internaltest.SandboxedSeed(t, home, filepath.Join(".pi", "agent", "settings.json"),
		`{"defaultProvider":"prizmal","defaultModel":"probe-model"}`)
	internaltest.SandboxedSeed(t, home, piModelsPath, `{"providers":{"prizmal":{`+
		`"baseUrl":"`+envconfig.Host().String()+`/v1","api":"openai-completions",`+
		`"apiKey":"`+internaltest.FakeKeyAtRest+`","models":[{"id":"probe-model","_launch":true}]}}}`)
	return filepath.Join(home, piModelsPath)
}

// A plain (non---persist) run must leave nothing key-shaped in the home
// directory. pi reads a "$VAR" reference in models.json's apiKey from its own
// environment, so the file names the variable and the key rides the child
// environment.
func TestPiLeavesNoKeyOnDisk(t *testing.T) {
	home := internaltest.SandboxedHome(t)
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)

	(&Pi{}).envVars(nil)
	if err := (&Pi{}).Edit([]launch.LaunchModel{{Name: "probe-model"}}); err != nil {
		t.Fatalf("configure pi: %v", err)
	}
	internaltest.AssertNoKeyUnder(t, home, "pi")
}

// The key reaches pi through the variable models.json's apiKey names.
func TestPiEnvVarsCarryAPIKey(t *testing.T) {
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)

	if got := internaltest.EnvValue((&Pi{}).envVars(nil), "PRIZMAL_SWITCH_KEY="); got != internaltest.FakeKeyAtRest {
		t.Fatalf("PRIZMAL_SWITCH_KEY = %q, want the configured key", got)
	}
}

// The upgrade path: an older build wrote the key itself into models.json, and
// the first launch of this one overwrites it.
func TestPiOverwriteLeavesNoKeyInBackupDir(t *testing.T) {
	home := internaltest.SandboxedHome(t)
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)

	target := seedLegacyKeyedConfig(t, home)
	internaltest.AssertOverwriteLeavesNoKeyInBackup(t, home, target, func() error {
		return (&Pi{}).Edit([]launch.LaunchModel{{Name: "other-probe-model"}})
	})
}

// Restore takes the legacy key off the disk and, because both restore paths
// bypass the backup helper on purpose, copies it nowhere.
func TestPiRestoreRemovesCredential(t *testing.T) {
	home := internaltest.SandboxedHome(t)
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)

	seedLegacyKeyedConfig(t, home)
	internaltest.AssertRestoreRemovesKey(t, home, func() error {
		_, err := (&Pi{}).Restore()
		return err
	})
}

// TestPiPersistStillWritesWorkingConfig is the --persist guard.
// `prizmal --persist pi` must keep writing a complete, working provider entry.
// Its apiKey is a reference to $PRIZMAL_SWITCH_KEY, which pi resolves from its
// environment, rather than the key itself. A credential-hygiene change that
// silently emptied it would be a worse bug than the one being fixed.
func TestPiPersistStillWritesWorkingConfig(t *testing.T) {
	home := internaltest.SandboxedHome(t)
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)

	if err := (&Pi{}).Edit([]launch.LaunchModel{{Name: "probe-model"}}); err != nil {
		t.Fatalf("Pi.Edit: %v", err)
	}

	providers, _ := internaltest.SandboxedJSON(t, home, piModelsPath)["providers"].(map[string]any)
	prizmal, _ := providers["prizmal"].(map[string]any)
	if prizmal == nil {
		t.Fatal("Pi.Edit wrote no prizmal provider")
	}
	if got, _ := prizmal["apiKey"].(string); got != "$PRIZMAL_SWITCH_KEY" {
		t.Fatalf("prizmal provider apiKey = %q, want a reference to $PRIZMAL_SWITCH_KEY", got)
	}
	if got, _ := prizmal["baseUrl"].(string); got == "" {
		t.Fatal("prizmal provider has no baseUrl")
	}

	settings, err := os.ReadFile(filepath.Join(home, ".pi", "agent", "settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var s map[string]any
	if err := json.Unmarshal(settings, &s); err != nil {
		t.Fatalf("unmarshal settings.json: %v", err)
	}
	if got, _ := s["defaultProvider"].(string); got != "prizmal" {
		t.Fatalf("defaultProvider = %q, want prizmal", got)
	}
}

// TestPiRestoreRemovesCredential covers the remedy that pi never had: until
// now `prizmal --restore pi` failed with "pi does not support restore", so the
// key pi must write had no documented way off the disk.
func TestPiRestorePreservesForeignProviders(t *testing.T) {
	home := internaltest.SandboxedHome(t)
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)

	agentDir := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	existing := `{"providers":{"someone-else":{"api":"openai-completions","apiKey":"their-own-fake-key","models":[{"id":"their-model"}]}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(existing), 0o600); err != nil {
		t.Fatalf("seed models.json: %v", err)
	}
	settings := `{"defaultProvider":"someone-else","defaultModel":"their-model","theme":"dark"}`
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatalf("seed settings.json: %v", err)
	}

	if _, err := (&Pi{}).Restore(); err != nil {
		t.Fatalf("Pi.Restore: %v", err)
	}

	providers, _ := internaltest.SandboxedJSON(t, home, piModelsPath)["providers"].(map[string]any)
	other, _ := providers["someone-else"].(map[string]any)
	if other == nil {
		t.Fatal("Pi.Restore removed a provider it does not manage")
	}
	if got, _ := other["apiKey"].(string); got != "their-own-fake-key" {
		t.Fatalf("foreign provider apiKey = %q, want it untouched", got)
	}

	data, err := os.ReadFile(filepath.Join(agentDir, "settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var s map[string]any
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("unmarshal settings.json: %v", err)
	}
	if got, _ := s["defaultProvider"].(string); got != "someone-else" {
		t.Fatalf("defaultProvider = %q, want someone-else left alone", got)
	}
	if got, _ := s["theme"].(string); got != "dark" {
		t.Fatalf("unrelated setting theme = %q, want dark", got)
	}
}

// TestPiRestoreResetsPrizmalDefaults covers the other half of pi's footprint
// on a machine that had no defaults before the first launch: an ordinary
// launch repoints defaultProvider/defaultModel at prizmal, and with nothing
// to put back, restore must not leave the machine pointed at the provider
// entry it just deleted. The case with defaults to put back is
// TestPiRestoreReinstatesPreviousDefaults.
func TestPiRestoreResetsPrizmalDefaults(t *testing.T) {
	home := internaltest.SandboxedHome(t)
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)

	p := &Pi{}
	if err := p.Edit([]launch.LaunchModel{{Name: "probe-model"}}); err != nil {
		t.Fatalf("Pi.Edit: %v", err)
	}
	if _, err := p.Restore(); err != nil {
		t.Fatalf("Pi.Restore: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(home, ".pi", "agent", "settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var s map[string]any
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("unmarshal settings.json: %v", err)
	}
	if got, _ := s["defaultProvider"].(string); got == piProviderID {
		t.Fatalf("defaultProvider = %q after restore: it names the provider entry restore just deleted", got)
	}
	if got, _ := s["defaultModel"].(string); got == "probe-model" {
		t.Fatalf("defaultModel = %q after restore: it names a model only the deleted entry served", got)
	}
}

// A PRIZMAL_SWITCH_KEY already in the operator's shell must not reach pi beside
// the launch's own: a name defined twice leaves the child's value to whichever
// copy the OS reads first.
func TestPiChildEnvCarriesTheLaunchKeyOnce(t *testing.T) {
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)
	// WithAPIKey clears the variable, so the inherited copy is set after it.
	t.Setenv("PRIZMAL_SWITCH_KEY", "inherited-from-the-shell")

	count := 0
	for _, kv := range (&Pi{}).childEnv(nil) {
		if strings.HasPrefix(kv, "PRIZMAL_SWITCH_KEY=") {
			count++
			if kv != "PRIZMAL_SWITCH_KEY="+internaltest.FakeKeyAtRest {
				t.Errorf("child env carries %q, want the launch key", kv)
			}
		}
	}
	if count != 1 {
		t.Fatalf("PRIZMAL_SWITCH_KEY appears %d times, want 1", count)
	}
}
