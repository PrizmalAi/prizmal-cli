package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// useTempHome points the config path at a temp directory and returns the dir.
func useTempHome(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	t.Setenv("HOME", d)
	t.Setenv("USERPROFILE", d) // windows
	return d
}

func TestLoadReturnsErrNoConfigWhenAbsent(t *testing.T) {
	useTempHome(t)
	if _, err := Load(); !errors.Is(err, ErrNoConfig) {
		t.Fatalf("expected ErrNoConfig, got %v", err)
	}
}

func TestPlainAPIKeyResolve(t *testing.T) {
	c := &Config{Version: 1, BaseURL: "https://api.prizmal.ai", APIKey: NewPlainAPIKey("sk-secret")}
	got, err := c.APIKey.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got != "sk-secret" {
		t.Fatalf("got %q, want sk-secret", got)
	}
}

func TestEnvAPIKeyResolve(t *testing.T) {
	t.Setenv("PRIZMAL_API_KEY", "sk-from-env")
	c := &Config{Version: 1, APIKey: &APIKeyValue{Env: "PRIZMAL_API_KEY", kind: apiKeyEnv}}
	got, err := c.APIKey.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got != "sk-from-env" {
		t.Fatalf("got %q, want sk-from-env", got)
	}
}

func TestEnvAPIKeyUnsetErrors(t *testing.T) {
	t.Setenv("PRIZMAL_MISSING", "")
	c := &Config{Version: 1, APIKey: &APIKeyValue{Env: "PRIZMAL_MISSING", kind: apiKeyEnv}}
	if _, err := c.APIKey.Resolve(); err == nil {
		t.Fatal("expected error for unset env var")
	}
}

func TestCommandAPIKeyResolve(t *testing.T) {
	c := &Config{Version: 1, APIKey: &APIKeyValue{Command: "echo sk-from-cmd", kind: apiKeyCommand}}
	got, err := c.APIKey.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got != "sk-from-cmd" {
		t.Fatalf("got %q, want sk-from-cmd", got)
	}
}

func TestCommandAPIKeyTrimsWhitespace(t *testing.T) {
	c := &Config{Version: 1, APIKey: &APIKeyValue{Command: "echo '  sk-padded  '", kind: apiKeyCommand}}
	got, err := c.APIKey.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got != "sk-padded" {
		t.Fatalf("got %q, want sk-padded", got)
	}
}

func TestSaveCreates0600AndRoundTrips(t *testing.T) {
	d := useTempHome(t)
	c := &Config{Version: 1, BaseURL: "https://switch.example.com", APIKey: NewPlainAPIKey("sk-roundtrip")}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}

	// directory 0700, file 0600
	if runtime.GOOS != "windows" {
		dir := filepath.Join(d, PrizmalDir)
		if fi, err := os.Stat(dir); err != nil {
			t.Fatalf("config dir missing: %v", err)
		} else if fi.Mode().Perm() != 0o700 {
			t.Fatalf("dir mode = %o, want 700", fi.Mode().Perm())
		}
	}
	fi, err := os.Stat(filepath.Join(d, PrizmalDir, PrizmalFileName))
	if err != nil {
		t.Fatalf("config file missing: %v", err)
	}
	if runtime.GOOS != "windows" {
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("file mode = %o, want 600", fi.Mode().Perm())
		}
	}

	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.BaseURL != "https://switch.example.com" {
		t.Fatalf("base_url = %q", got.BaseURL)
	}
	if got.APIKey == nil || got.APIKey.Plain != "sk-roundtrip" {
		t.Fatalf("api_key not round-tripped: %+v", got.APIKey)
	}
}

func TestAPIKeyUnmarshalForms(t *testing.T) {
	cases := []struct {
		name string
		json string
		want string // the api_key value field to inspect
	}{
		{"plain", `{"version":1,"api_key":"sk-plain"}`, "plain"},
		{"env", `{"version":1,"api_key":{"env":"PRIZMAL_API_KEY"}}`, "env"},
		{"command", `{"version":1,"api_key":{"command":"op read x"}}`, "command"},
		{"forward-compatible-unknown", `{"version":1,"api_key":{"kind":"kms","ref":"abc"}}`, "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var c Config
			if err := json.Unmarshal([]byte(tc.json), &c); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if c.APIKey == nil {
				t.Fatalf("api_key not parsed")
			}
			switch tc.want {
			case "plain":
				if c.APIKey.Plain != "sk-plain" || c.APIKey.kind != apiKeyPlain {
					t.Fatalf("got %+v", c.APIKey)
				}
			case "env":
				if c.APIKey.Env != "PRIZMAL_API_KEY" || c.APIKey.kind != apiKeyEnv {
					t.Fatalf("got %+v", c.APIKey)
				}
			case "command":
				if c.APIKey.Command != "op read x" || c.APIKey.kind != apiKeyCommand {
					t.Fatalf("got %+v", c.APIKey)
				}
			case "unknown":
				if c.APIKey.kind != apiKeyUnknown {
					t.Fatalf("got %+v", c.APIKey)
				}
			}
		})
	}
}

func TestSaveEmptyKeyWritesBaseURLOnly(t *testing.T) {
	useTempHome(t)
	c := &Config{BaseURL: "https://api.prizmal.ai"} // no api_key
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(mustPath(t))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["api_key"]; ok {
		t.Fatal("api_key should be omitted when empty")
	}
}

func mustPath(t *testing.T) string {
	t.Helper()
	p, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	return p
}
