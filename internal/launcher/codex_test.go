package launch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
)

// sandboxCodexHome points HOME and USERPROFILE at a temp dir so config writes
// never touch a real user config, and returns the dir.
func sandboxCodexHome(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	t.Setenv("HOME", d)
	t.Setenv("USERPROFILE", d)
	return d
}

// TestCodexProfileConfigSourcesKeyFromEnvNotDisk pins the credential channel
// for codex: the generated profile names the environment variable codex must
// read (env_key), and carries no literal api_key. prizmal sets that variable
// on the child at exec, so the key lives for one process instead of forever.
func TestCodexProfileConfigSourcesKeyFromEnvNotDisk(t *testing.T) {
	sandboxCodexHome(t)
	envconfig.SetAPIKey("not-a-real-key-codex-probe")
	t.Cleanup(func() { envconfig.SetAPIKey("") })

	profilePath := filepath.Join(t.TempDir(), "prizmal.config.toml")
	if err := writeCodexNamedProfileConfig(profilePath, codexProfileName, "gpt-test", "", ""); err != nil {
		t.Fatalf("writeCodexNamedProfileConfig: %v", err)
	}

	data, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	text := string(data)
	parsed, err := codexParseConfig(text)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	if got, ok := parsed.String("model_providers", codexProfileName, "env_key"); !ok || got != "OPENAI_API_KEY" {
		t.Fatalf("env_key = %q (ok=%v), want OPENAI_API_KEY", got, ok)
	}
	if got, ok := parsed.String("model_providers", codexProfileName, "api_key"); ok {
		t.Fatalf("api_key = %q is present; the profile must carry no credential at rest", got)
	}
	if strings.Contains(text, "not-a-real-key-codex-probe") {
		t.Fatalf("the provider key appears in the generated profile:\n%s", text)
	}
}

// TestCodexProfileConfigNeverWritesPlaceholderKey guards against regressing to
// the old placeholder credential, and against api_key coming back at all.
func TestCodexProfileConfigNeverWritesPlaceholderKey(t *testing.T) {
	sandboxCodexHome(t)
	envconfig.SetAPIKey("")
	t.Cleanup(func() { envconfig.SetAPIKey("") })

	profilePath := filepath.Join(t.TempDir(), "prizmal.config.toml")
	if err := writeCodexNamedProfileConfig(profilePath, codexProfileName, "gpt-test", "", ""); err != nil {
		t.Fatalf("writeCodexNamedProfileConfig: %v", err)
	}
	data, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	text := string(data)

	for _, bad := range []string{
		"api_key",
		"OPENAI_API_KEY=harness-launch",
		"OPENAI_API_KEY=ollama",
	} {
		if strings.Contains(text, bad) {
			t.Fatalf("found %q in config:\n%s", bad, text)
		}
	}
}
