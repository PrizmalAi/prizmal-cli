package launch

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
)

// fakeKeyAtRest is an obvious non-credential used to prove where a key would
// land. It must never look like a real provider key.
const fakeKeyAtRest = "not-a-real-key-credential-at-rest-probe"

// filesContainingKey walks root and returns every file whose bytes contain the
// probe key. Directories that do not exist are simply empty.
func filesContainingKey(t *testing.T, root string) []string {
	t.Helper()
	var hits []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries cannot hold a key we care about
		}
		if d.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		if strings.Contains(string(data), fakeKeyAtRest) {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
			hits = append(hits, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return hits
}

// TestEnvChannelLaunchersLeaveNoKeyOnDisk is the core credential-at-rest
// guarantee: for every harness that accepts the key over an environment
// variable, a plain (non---persist) run must leave nothing key-shaped in the
// home directory. Anything these launchers write is config the harness needs;
// the credential itself rides the child environment and dies with the process.
func TestEnvChannelLaunchersLeaveNoKeyOnDisk(t *testing.T) {
	models := []LaunchModel{{Name: "probe-model"}}

	tests := []struct {
		name string
		// configure runs the launcher's disk-writing path, the same one a
		// plain `prizmal <harness>` run takes before exec.
		configure func(t *testing.T) error
	}{
		{
			name: "claude",
			configure: func(t *testing.T) error {
				// Claude writes nothing at all; the key exists only in the
				// child environment assembled here.
				(&Claude{}).envVars()
				return nil
			},
		},
		{
			name: "codex",
			configure: func(t *testing.T) error {
				return ensureCodexConfig("probe-model", models)
			},
		},
		{
			name: "opencode",
			configure: func(t *testing.T) error {
				return (&OpenCode{}).Edit(models)
			},
		},
		{
			// pi reads a "$VAR" reference in models.json's apiKey from its
			// own environment, so the file names the variable and the key
			// rides the child environment.
			name: "pi",
			configure: func(t *testing.T) error {
				(&Pi{}).envVars(nil)
				return (&Pi{}).Edit(models)
			},
		},
		{
			// cline's openai-compatible provider falls back to
			// OPENAI_API_KEY when its settings carry no apiKey.
			name: "cline",
			configure: func(t *testing.T) error {
				(&Cline{}).envVars()
				return (&Cline{}).Edit(models)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := cdkSandboxHome(t)
			internaltest.WithAPIKey(t, fakeKeyAtRest)

			if err := tc.configure(t); err != nil {
				t.Fatalf("configure %s: %v", tc.name, err)
			}

			if hits := filesContainingKey(t, home); len(hits) > 0 {
				t.Fatalf("%s left the provider key on disk in: %v", tc.name, hits)
			}
		})
	}
}

// TestEnvVarsCarryAPIKey proves the channel that replaces a key written to
// disk: each harness reads the key from the variable named here, and prizmal
// sets it on the child. Codex names OPENAI_API_KEY with env_key in its
// generated profile, pi reads the "$PRIZMAL_SWITCH_KEY" reference in
// models.json, and cline's openai-compatible provider falls back to
// OPENAI_API_KEY.
func TestEnvVarsCarryAPIKey(t *testing.T) {
	tests := []struct {
		name    string
		envVars func() []string
		varName string
	}{
		{"codex", (&Codex{}).envVars, "OPENAI_API_KEY"},
		{"pi", func() []string { return (&Pi{}).envVars(nil) }, "PRIZMAL_SWITCH_KEY"},
		{"cline", (&Cline{}).envVars, "OPENAI_API_KEY"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			internaltest.WithAPIKey(t, fakeKeyAtRest)

			if got := envValue(tc.envVars(), tc.varName+"="); got != fakeKeyAtRest {
				t.Fatalf("%s = %q, want the configured key", tc.varName, got)
			}
		})
	}
}

// TestCodexProfileConfigRemainsUsableWithoutKey guards the other half of the
// change: dropping api_key must not strip anything codex needs to route to the
// Switch. This is what a persisted profile still has to contain.
func TestCodexProfileConfigRemainsUsableWithoutKey(t *testing.T) {
	sandboxCodexHome(t)
	internaltest.WithAPIKey(t, fakeKeyAtRest)

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

// The config paths the credential-at-rest tests inspect, relative to the
// sandboxed home.
var (
	piModelsPath  = filepath.Join(".pi", "agent", "models.json")
	clineProvPath = filepath.Join(".cline", "data", "settings", "providers.json")
)

// sandboxedJSON reads a config file relative to a sandboxed home and parses
// it, the one reader every credential-at-rest test uses; the pi and cline
// callers differ only in the path.
func sandboxedJSON(t *testing.T, home, relPath string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, relPath))
	if err != nil {
		t.Fatalf("read %s: %v", relPath, err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal %s: %v", relPath, err)
	}
	return cfg
}

// TestPiPersistStillWritesWorkingConfig is the --persist guard.
// `prizmal --persist pi` must keep writing a complete, working provider entry.
// Its apiKey is a reference to $PRIZMAL_SWITCH_KEY, which pi resolves from its
// environment, rather than the key itself. A credential-hygiene change that
// silently emptied it would be a worse bug than the one being fixed.
func TestPiPersistStillWritesWorkingConfig(t *testing.T) {
	home := cdkSandboxHome(t)
	internaltest.WithAPIKey(t, fakeKeyAtRest)

	if err := (&Pi{}).Edit([]LaunchModel{{Name: "probe-model"}}); err != nil {
		t.Fatalf("Pi.Edit: %v", err)
	}

	providers, _ := sandboxedJSON(t, home, piModelsPath)["providers"].(map[string]any)
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
	home := cdkSandboxHome(t)
	internaltest.WithAPIKey(t, fakeKeyAtRest)

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

	providers, _ := sandboxedJSON(t, home, piModelsPath)["providers"].(map[string]any)
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
	home := cdkSandboxHome(t)
	internaltest.WithAPIKey(t, fakeKeyAtRest)

	p := &Pi{}
	if err := p.Edit([]LaunchModel{{Name: "probe-model"}}); err != nil {
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

// backupProbeCase drives one launcher over a key-bearing predecessor file and
// then inspects ~/.prizmal/backup.
type backupProbeCase struct {
	name string
	// seed puts a key-bearing file where the launcher is about to write, and
	// returns its path. For launchers that write the key themselves this is
	// just an earlier ordinary run.
	seed func(t *testing.T, home string) string
	// configure runs the launcher path that overwrites the seeded file.
	configure func(t *testing.T) error
}

// TestOverwritesLeaveNoKeyInBackupDir is the trap in this fix: the shared
// WriteWithBackup helper copies the file it is about to overwrite into
// ~/.prizmal/backup. Using it inside Restore would scrub the config and
// deposit the very key being removed one directory over.
func TestOverwritesLeaveNoKeyInBackupDir(t *testing.T) {
	cases := []backupProbeCase{
		{
			// The upgrade path: an older build wrote the key itself into
			// models.json, and the first launch of this one overwrites it.
			name: "pi",
			seed: func(t *testing.T, home string) string {
				return seedLegacyKeyedConfig(t, home, "pi")
			},
			configure: func(t *testing.T) error {
				return (&Pi{}).Edit([]LaunchModel{{Name: "other-probe-model"}})
			},
		},
		{
			// The same upgrade path for cline's providers.json.
			name: "cline",
			seed: func(t *testing.T, home string) string {
				return seedLegacyKeyedConfig(t, home, "cline")
			},
			configure: func(t *testing.T) error {
				return (&Cline{}).Edit([]LaunchModel{{Name: "other-probe-model"}})
			},
		},
		{
			// The upgrade path: a prizmal old enough to write api_key into
			// the generated profile left one there, and the first run of the
			// fixed build overwrites it. The key must not be preserved on the
			// way out.
			name: "codex-profile-upgrade",
			seed: func(t *testing.T, home string) string {
				profilePath := filepath.Join(home, ".codex", "prizmal.config.toml")
				if err := os.MkdirAll(filepath.Dir(profilePath), 0o755); err != nil {
					t.Fatalf("mkdir .codex: %v", err)
				}
				legacy := "model = \"stale-model\"\nmodel_provider = \"prizmal\"\n\n" +
					"[model_providers.prizmal]\nname = \"Prizmal\"\n" +
					"api_key = \"" + fakeKeyAtRest + "\"\n"
				if err := os.WriteFile(profilePath, []byte(legacy), 0o600); err != nil {
					t.Fatalf("seed prizmal.config.toml: %v", err)
				}
				return profilePath
			},
			configure: func(t *testing.T) error {
				return ensureCodexConfig("probe-model", []LaunchModel{{Name: "probe-model"}})
			},
		},
		{
			// ~/.codex/config.toml is the user's own file. Cleanup rewrites
			// it to drop a legacy prizmal profile, and the copy it takes on
			// the way past holds whatever keys were in it.
			name: "codex-root-config-cleanup",
			seed: func(t *testing.T, home string) string {
				configPath := filepath.Join(home, ".codex", "config.toml")
				if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
					t.Fatalf("mkdir .codex: %v", err)
				}
				legacy := "profile = \"prizmal\"\n\n[profiles.prizmal]\n" +
					"model = \"stale-model\"\napi_key = \"" + fakeKeyAtRest + "\"\n"
				if err := os.WriteFile(configPath, []byte(legacy), 0o600); err != nil {
					t.Fatalf("seed config.toml: %v", err)
				}
				return configPath
			},
			configure: func(t *testing.T) error {
				return ensureCodexConfig("probe-model", []LaunchModel{{Name: "probe-model"}})
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := cdkSandboxHome(t)
			internaltest.WithAPIKey(t, fakeKeyAtRest)

			target := tc.seed(t, home)
			before, err := os.ReadFile(target)
			if err != nil {
				t.Fatalf("read seeded %s: %v", target, err)
			}
			if !strings.Contains(string(before), fakeKeyAtRest) {
				t.Fatalf("precondition failed: seeded %s carries no key", target)
			}

			if err := tc.configure(t); err != nil {
				t.Fatalf("configure %s: %v", tc.name, err)
			}

			after, err := os.ReadFile(target)
			if err != nil {
				t.Fatalf("read overwritten %s: %v", target, err)
			}
			if string(after) == string(before) {
				t.Fatalf("precondition failed: %s was not overwritten, so no backup was taken", target)
			}

			backupDir := filepath.Join(home, ".prizmal", "backup")
			if hits := filesContainingKey(t, backupDir); len(hits) > 0 {
				t.Fatalf("%s retained the provider key under ~/.prizmal/backup: %v", tc.name, hits)
			}
		})
	}
}

// sandboxedSeed writes body to a file relative to a sandboxed home, creating
// the parent directories, the way a user's own harness install would look.
func sandboxedSeed(t *testing.T, home, relPath, body string) {
	t.Helper()
	path := filepath.Join(home, relPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("seed %s: %v", relPath, err)
	}
}

// TestRestoreRemovesCredential drives both file-writing launchers' Restore
// through one table: each Edit writes its key, Restore takes it back off the
// disk, and the provider entry it created goes with it. Cline needs the undo
// because it has no environment channel for a custom provider, so the key it
// needs is in providers.json and `prizmal --restore cline` is the only way
// back off the disk; pi already had the same remedy.

// restorer is the Edit-then-Restore pair the credential-at-rest tests drive.
// Both halves are named in the launcher protocol, so the pair composes them
// rather than re-spelling the shape.
type restorer interface {
	Editor
	Restorer
}

// seedLegacyKeyedConfig writes the provider config an older build of the pi
// or cline launcher left behind, with the key itself in it, and returns its
// path. The current launchers write no key, so this is the only way one is
// still on disk for Edit to overwrite or Restore to remove.
func seedLegacyKeyedConfig(t *testing.T, home, harness string) string {
	t.Helper()
	switch harness {
	case "pi":
		sandboxedSeed(t, home, filepath.Join(".pi", "agent", "settings.json"),
			`{"defaultProvider":"prizmal","defaultModel":"probe-model"}`)
		sandboxedSeed(t, home, piModelsPath, `{"providers":{"prizmal":{`+
			`"baseUrl":"`+envconfig.Host().String()+`/v1","api":"openai-completions",`+
			`"apiKey":"`+fakeKeyAtRest+`","models":[{"id":"probe-model","_launch":true}]}}}`)
		return filepath.Join(home, piModelsPath)
	case "cline":
		sandboxedSeed(t, home, clineProvPath, `{"version":1,"lastUsedProvider":"openai-compatible","providers":{`+
			`"openai-compatible":{"settings":{"provider":"openai-compatible","model":"probe-model",`+
			`"baseUrl":"`+clineProviderBaseURL()+`","apiKey":"`+fakeKeyAtRest+`"},"tokenSource":"manual"}}}`)
		return filepath.Join(home, clineProvPath)
	}
	t.Fatalf("no legacy config for %q", harness)
	return ""
}

// restoreCaseTable lists the file-writing launchers both restore tests drive,
// so the two tables cannot drift apart.
func restoreCaseTable() []struct {
	name     string
	restorer func() restorer
} {
	return []struct {
		name     string
		restorer func() restorer
	}{
		{"pi", func() restorer { return &Pi{} }},
		{"cline", func() restorer { return &Cline{} }},
	}
}

func TestRestoreRemovesCredential(t *testing.T) {
	tests := restoreCaseTable()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := cdkSandboxHome(t)
			internaltest.WithAPIKey(t, fakeKeyAtRest)

			r := tc.restorer()
			seedLegacyKeyedConfig(t, home, tc.name)
			if hits := filesContainingKey(t, home); len(hits) == 0 {
				t.Fatal("precondition failed: the seed wrote no key, nothing to restore")
			}

			if _, err := r.Restore(); err != nil {
				t.Fatalf("%T.Restore: %v", r, err)
			}

			if hits := filesContainingKey(t, home); len(hits) > 0 {
				t.Fatalf("Restore left the provider key on disk in: %v", hits)
			}
		})
	}
}

func TestClineRestorePreservesForeignProviders(t *testing.T) {
	home := cdkSandboxHome(t)
	internaltest.WithAPIKey(t, fakeKeyAtRest)

	sandboxedSeed(t, home, clineProvPath, `{"version":1,"lastUsedProvider":"openrouter","providers":{`+
		`"openrouter":{"settings":{"provider":"openrouter","model":"their-model","apiKey":"their-own-fake-key"},"tokenSource":"manual"}}}`)

	c := &Cline{}
	if err := c.Edit([]LaunchModel{{Name: "probe-model"}}); err != nil {
		t.Fatalf("Cline.Edit: %v", err)
	}
	if _, err := c.Restore(); err != nil {
		t.Fatalf("Cline.Restore: %v", err)
	}

	if hits := filesContainingKey(t, home); len(hits) > 0 {
		t.Fatalf("Cline.Restore left the provider key on disk in: %v", hits)
	}

	providers, _ := sandboxedJSON(t, home, clineProvPath)["providers"].(map[string]any)
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
	home := cdkSandboxHome(t)
	internaltest.WithAPIKey(t, fakeKeyAtRest)

	sandboxedSeed(t, home, clineProvPath, `{"version":1,"lastUsedProvider":"openai-compatible","providers":{`+
		`"openai-compatible":{"settings":{"provider":"openai-compatible","model":"their-model",`+
		`"baseUrl":"https://their-endpoint.example/v1","apiKey":"their-own-fake-key",`+
		`"headers":{"x-their-header":"value"}},"tokenSource":"manual"}}}`)

	if _, err := (&Cline{}).Restore(); err != nil {
		t.Fatalf("Cline.Restore: %v", err)
	}

	cfg := sandboxedJSON(t, home, clineProvPath)
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

// TestRestoreTakesNoKeyBearingBackup drives both file-writing launchers
// through one table: a key-bearing config an older build wrote, then Restore, and the backup directory must never
// gain a key-bearing copy, because both restore paths bypass the backup
// helper on purpose. The trap is the same for both: writing through
// fileutil.WriteWithBackup inside Restore would scrub the config and deposit
// the key being removed under ~/.prizmal/backup.
func TestRestoreTakesNoKeyBearingBackup(t *testing.T) {
	tests := restoreCaseTable()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := cdkSandboxHome(t)
			internaltest.WithAPIKey(t, fakeKeyAtRest)

			r := tc.restorer()
			seedLegacyKeyedConfig(t, home, tc.name)
			if _, err := r.Restore(); err != nil {
				t.Fatalf("%T.Restore: %v", r, err)
			}

			backupDir := filepath.Join(home, ".prizmal", "backup")
			for _, hit := range filesContainingKey(t, backupDir) {
				t.Fatalf("Restore copied the key into the backup directory: %s", hit)
			}
		})
	}
}
