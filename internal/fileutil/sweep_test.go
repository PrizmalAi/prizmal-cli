package fileutil

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
)

// Keys that are not the configured one. The live-key match cannot see any
// of them, which is the gap the sweep closes.
const (
	rotatedKey     = "not-a-real-key-rotated-away-last-week"
	otherTenantKey = "not-a-real-key-belongs-to-another-tenant"
)

// plant writes body at rel under the sandboxed backup root and returns its
// absolute path.
func plant(t *testing.T, rel, body string) string {
	t.Helper()
	path := filepath.Join(BackupDir(), rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("plant %s: %v", rel, err)
	}
	return path
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// switchCredentialBackups are retained copies in the shapes prizmal writes,
// each holding a Switch key that is not the configured one.
var switchCredentialBackups = []struct {
	name, rel, body string
}{
	{
		name: "pi models.json with a rotated key",
		rel:  "pi/models.json.1700000000",
		body: `{"providers":{"prizmal":{"baseUrl":"https://api.prizmal.ai/v1","api":"openai-completions","apiKey":"` + rotatedKey + `"}}}`,
	},
	{
		name: "cline providers.json from another tenant",
		rel:  "cline/providers.json.1700000001",
		body: `{"providers":{"openai-compatible":{"settings":{"provider":"openai-compatible","model":"m","baseUrl":"https://api.prizmal.ai/v1","apiKey":"` + otherTenantKey + `"},"tokenSource":"manual"}}}`,
	},
	{
		name: "codex profile TOML written by an older build",
		rel:  "config.toml.1700000002",
		body: "model_provider = \"prizmal\"\n\n[model_providers.prizmal]\nname = \"Prizmal\"\nbase_url = \"https://api.prizmal.ai/v1/\"\nenv_key = \"OPENAI_API_KEY\"\napi_key = \"" + rotatedKey + "\"\n",
	},
	{
		name: "hermes YAML from a harness this build no longer ships",
		rel:  "hermes/config.yaml.1700000003",
		body: "model:\n  provider: custom\n  base_url: https://api.prizmal.ai/v1\n  api_key: " + otherTenantKey + "\n",
	},
}

// TestWriteWithBackupRetainsForeignKeyBackup pins the gap: the write-time
// scrub only knows the configured key, so a rotated or foreign one survives
// a write to the very same file.
func TestWriteWithBackupRetainsForeignKeyBackup(t *testing.T) {
	sandboxHome(t)
	internaltest.WithAPIKey(t, fakeKey)

	tainted := plant(t, "probe/models.json.1700000000", switchCredentialBackups[0].body)
	target := filepath.Join(t.TempDir(), "models.json")
	if err := WriteWithBackup(target, []byte(`{}`), "probe"); err != nil {
		t.Fatalf("WriteWithBackup: %v", err)
	}
	if !exists(tainted) {
		t.Fatalf("expected the write-time scrub to miss a key it does not hold; the sweep test below is what removes it")
	}
}

// TestSweepBackupsRemovesSwitchCredentialsItDoesNotHold is the fix: a
// retained copy holding a Switch key goes, whatever the key's value.
func TestSweepBackupsRemovesSwitchCredentialsItDoesNotHold(t *testing.T) {
	for _, tc := range switchCredentialBackups {
		t.Run(tc.name, func(t *testing.T) {
			sandboxHome(t)
			internaltest.WithAPIKey(t, fakeKey)

			tainted := plant(t, tc.rel, tc.body)
			removed := SweepBackups()
			if exists(tainted) {
				t.Fatalf("sweep kept %s, which holds a Switch key", tc.rel)
			}
			if !slices.Equal(removed, []string{tainted}) {
				t.Fatalf("removed = %v, want [%s]", removed, tainted)
			}
		})
	}
}

// TestSweepBackupsWorksWithNoKeyConfigured covers the install where the
// live-key match matches nothing at all.
func TestSweepBackupsWorksWithNoKeyConfigured(t *testing.T) {
	sandboxHome(t)
	internaltest.WithAPIKey(t, "")

	tainted := plant(t, switchCredentialBackups[0].rel, switchCredentialBackups[0].body)
	SweepBackups()
	if exists(tainted) {
		t.Fatalf("sweep kept a key-bearing backup because no key is configured")
	}
}

// TestSweepBackupsRemovesLiveKeyInAnyShape keeps the exact match as a
// reinforcement: the configured key goes wherever it sits, including a file
// whose shape the structural check does not know.
func TestSweepBackupsRemovesLiveKeyInAnyShape(t *testing.T) {
	sandboxHome(t)
	internaltest.WithAPIKey(t, fakeKey)

	tainted := plant(t, "retired/notes.txt.1700000000", "token: "+fakeKey+"\n")
	SweepBackups()
	if exists(tainted) {
		t.Fatalf("sweep kept a backup holding the configured key")
	}
}

// TestSweepBackupsKeepsWhatIsNotASwitchCredential guards the retention
// policy: a keyless copy, and a user's own key for an endpoint that is not
// the Switch, are the user's settings and stay.
func TestSweepBackupsKeepsWhatIsNotASwitchCredential(t *testing.T) {
	sandboxHome(t)
	internaltest.WithAPIKey(t, fakeKey)

	keep := []string{
		plant(t, "pi/settings.json.1700000000", `{"defaultProvider":"prizmal","defaultModel":"m"}`),
		plant(t, "pi/models.json.1600000000", `{"providers":{"prizmal":{"baseUrl":"https://api.prizmal.ai/v1","api":"openai-completions"}}}`),
		plant(t, "cline/providers.json.1600000000", `{"providers":{"openai-compatible":{"settings":{"baseUrl":"https://llm.example.com/v1","apiKey":"users-own-key"}}}}`),
		plant(t, "codex/config.toml.1600000000", "[model_providers.mine]\nbase_url = \"https://llm.example.com/v1\"\napi_key = \"users-own-key\"\n"),
		plant(t, "notes.txt.1600000000", "not json, not toml: [\n"),
	}
	if removed := SweepBackups(); len(removed) != 0 {
		t.Fatalf("sweep removed %v, none of which holds a Switch key", removed)
	}
	for _, path := range keep {
		if !exists(path) {
			t.Fatalf("sweep removed %s", path)
		}
	}
}

// TestSweepBackupsStaysInsideBackupDir: a Switch key elsewhere under HOME is
// not the sweep's to touch, and a symlink in the backup tree is not followed.
func TestSweepBackupsStaysInsideBackupDir(t *testing.T) {
	home := sandboxHome(t)
	internaltest.WithAPIKey(t, fakeKey)

	body := switchCredentialBackups[0].body
	outside := filepath.Join(home, ".pi", "agent", "models.json")
	if err := os.MkdirAll(filepath.Dir(outside), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(BackupDir(), "pi", "models.json.1700000000")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	SweepBackups()
	if !exists(outside) {
		t.Fatalf("sweep removed a file outside the backup directory")
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != body {
		t.Fatalf("sweep changed a file outside the backup directory")
	}
}
