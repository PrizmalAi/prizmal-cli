package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

// A plain (non---persist) run must leave nothing key-shaped in the home
// directory. Anything the launcher writes is config codex needs; the
// credential itself rides the child environment and dies with the process.
func TestCodexLeavesNoKeyOnDisk(t *testing.T) {
	home := internaltest.SandboxedHome(t)
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)
	fakeCodexBundle(t, fakeCodexCatalog)

	if err := ensureCodexConfig("probe-model", []launch.LaunchModel{{Name: "probe-model"}}); err != nil {
		t.Fatalf("configure codex: %v", err)
	}
	internaltest.AssertNoKeyUnder(t, home, "codex")
}

// Codex names OPENAI_API_KEY with env_key in its generated profile, and
// prizmal sets it on the child.
func TestCodexEnvVarsCarryAPIKey(t *testing.T) {
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)

	if got := internaltest.EnvValue((&Codex{}).envVars(), "OPENAI_API_KEY="); got != internaltest.FakeKeyAtRest {
		t.Fatalf("OPENAI_API_KEY = %q, want the configured key", got)
	}
}

// The upgrade path: a prizmal old enough to write api_key into the generated
// profile left one there, and the first run of the fixed build overwrites it.
// The key must not be preserved on the way out.
func TestCodexProfileUpgradeLeavesNoKeyInBackupDir(t *testing.T) {
	home := internaltest.SandboxedHome(t)
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)
	fakeCodexBundle(t, fakeCodexCatalog)

	legacy := "model = \"stale-model\"\nmodel_provider = \"prizmal\"\n\n" +
		"[model_providers.prizmal]\nname = \"Prizmal\"\n" +
		"api_key = \"" + internaltest.FakeKeyAtRest + "\"\n"
	rel := filepath.Join(".codex", "prizmal.config.toml")
	internaltest.SandboxedSeed(t, home, rel, legacy)
	internaltest.AssertOverwriteLeavesNoKeyInBackup(t, home, filepath.Join(home, rel), func() error {
		return ensureCodexConfig("probe-model", []launch.LaunchModel{{Name: "probe-model"}})
	})
}

// ~/.codex/config.toml is the user's own file. Cleanup rewrites it to drop a
// legacy prizmal profile, and the copy it takes on the way past holds
// whatever keys were in it.
func TestCodexRootConfigCleanupLeavesNoKeyInBackupDir(t *testing.T) {
	home := internaltest.SandboxedHome(t)
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)
	fakeCodexBundle(t, fakeCodexCatalog)

	legacy := "profile = \"prizmal\"\n\n[profiles.prizmal]\n" +
		"model = \"stale-model\"\napi_key = \"" + internaltest.FakeKeyAtRest + "\"\n"
	rel := filepath.Join(".codex", "config.toml")
	internaltest.SandboxedSeed(t, home, rel, legacy)
	internaltest.AssertOverwriteLeavesNoKeyInBackup(t, home, filepath.Join(home, rel), func() error {
		return ensureCodexConfig("probe-model", []launch.LaunchModel{{Name: "probe-model"}})
	})
}

// TestCodexProfileConfigRemainsUsableWithoutKey guards the other half of the
// change: dropping api_key must not strip anything codex needs to route to the
// Switch. This is what a persisted profile still has to contain.
func TestCodexProfileConfigRemainsUsableWithoutKey(t *testing.T) {
	sandboxCodexHome(t)
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)

	profilePath := filepath.Join(t.TempDir(), "prizmal.config.toml")
	catalogPath := filepath.Join(t.TempDir(), "model.json")
	if err := writeCodexNamedProfileConfig(profilePath, codexProfileName, "probe-model", catalogPath, ""); err != nil {
		t.Fatalf("writeCodexNamedProfileConfig: %v", err)
	}

	data, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	parsed, err := codexParseConfig(string(data))
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	for _, want := range []struct {
		path  []string
		value string
	}{
		{[]string{"model_providers", codexProfileName, "name"}, codexProviderName},
		{[]string{"model_providers", codexProfileName, "wire_api"}, "responses"},
		{[]string{"model_providers", codexProfileName, "env_key"}, "OPENAI_API_KEY"},
	} {
		if got, ok := parsed.String(want.path...); !ok || got != want.value {
			t.Fatalf("%s = %q (ok=%v), want %q", strings.Join(want.path, "."), got, ok, want.value)
		}
	}
	if got, ok := parsed.String("model_providers", codexProfileName, "base_url"); !ok || got == "" {
		t.Fatalf("base_url = %q (ok=%v), want a non-empty URL", got, ok)
	}
	if got := parsed.RootString(codexRootModelKey); got != "probe-model" {
		t.Fatalf("model = %q, want probe-model", got)
	}
	if got := parsed.RootString(codexRootModelProviderKey); got != codexProfileName {
		t.Fatalf("model_provider = %q, want %q", got, codexProfileName)
	}
	if got := parsed.RootString(codexRootModelCatalogJSONKey); got != catalogPath {
		t.Fatalf("model_catalog_json = %q, want %q", got, catalogPath)
	}
}
