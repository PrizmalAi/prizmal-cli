package config

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
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

// TestLoadRejectsCorruptFile pins that unparseable JSON is an error, not an
// empty Config. A caller that cannot distinguish a destroyed file from an
// absent one will treat the zero value as truth and write over whatever was
// there — which is how a stored api_key went missing without a word.
func TestLoadRejectsCorruptFile(t *testing.T) {
	d := useTempHome(t)
	writeRaw(t, filepath.Join(d, PrizmalDir, PrizmalFileName), "this is not json at all")
	cfg, err := Load()
	if err == nil {
		t.Fatalf("expected a parse error, got Config %+v", cfg)
	}
	if cfg != nil {
		t.Fatalf("expected nil Config on a parse error, got %+v", cfg)
	}
}

// TestSaveRoundTripsEveryConfigField holds the property the deleted
// per-integration module broke: a Save writes every field Config models, and
// a Load of the result returns all of them. A second module with its own
// struct on the same path satisfied this for one schema by erasing the other's
// keys silently, so it is worth pinning on all four at once.
func TestSaveRoundTripsEveryConfigField(t *testing.T) {
	useTempHome(t)
	c := &Config{
		Version:      PrizmalConfigVersion,
		BaseURL:      "https://switch.example.com",
		APIKey:       NewPlainAPIKey("sk-roundtrip"),
		DefaultModel: "smart",
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.BaseURL != c.BaseURL {
		t.Errorf("base_url = %q, want %q", got.BaseURL, c.BaseURL)
	}
	if got.APIKey == nil || got.APIKey.Plain != "sk-roundtrip" {
		t.Errorf("api_key = %+v, want plain sk-roundtrip", got.APIKey)
	}
	if got.DefaultModel != c.DefaultModel {
		t.Errorf("default_model = %q, want %q", got.DefaultModel, c.DefaultModel)
	}
	if got.Version != c.Version {
		t.Errorf("version = %d, want %d", got.Version, c.Version)
	}
}

// writeRaw puts content at a path, creating the config directory first. Tests
// that need to plant a file the CLI would never write use it instead of
// Config.Save, so the planted bytes reach disk untouched.
func writeRaw(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestHarnessesRoundTrip(t *testing.T) {
	useTempHome(t)
	c := &Config{Harnesses: map[string]HarnessConfig{"claude": {Args: `--allow-dangerously-skip-permissions --append-system-prompt "be brief"`}}}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Harnesses["claude"].Args != c.Harnesses["claude"].Args {
		t.Fatalf("args = %q, want %q", got.Harnesses["claude"].Args, c.Harnesses["claude"].Args)
	}
}

func TestHarnessesOmittedWhenEmpty(t *testing.T) {
	useTempHome(t)
	if err := (&Config{}).Save(); err != nil {
		t.Fatal(err)
	}
	p, _ := Path()
	data, _ := os.ReadFile(p)
	if strings.Contains(string(data), "harnesses") {
		t.Fatalf("empty config wrote a harnesses key: %s", data)
	}
}

// captureStderr runs fn with os.Stderr redirected to a pipe and returns
// everything written to it.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()
	fn()
	_ = w.Close()
	os.Stderr = old
	return <-done
}

// TestLoadWarnsOnUnknownKeys pins the warning half of the contract: an
// option this build does not model is reported once, by name, and never
// treated as an error.
func TestLoadWarnsOnUnknownKeys(t *testing.T) {
	useTempHome(t)
	p, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	writeRaw(t, p, `{"version":1,"base_url":"https://switch.example.com","api_key":"sk-known","mystery_option":{"nested":true},"experimental":"on"}`)

	var cfg *Config
	var loadErr error
	out := captureStderr(t, func() { cfg, loadErr = Load() })
	if loadErr != nil {
		t.Fatalf("unknown options must not be an error, got %v", loadErr)
	}
	if cfg.BaseURL != "https://switch.example.com" {
		t.Errorf("base_url = %q, want the file's value", cfg.BaseURL)
	}
	for _, key := range []string{"experimental", "mystery_option"} {
		if !strings.Contains(out, key) {
			t.Errorf("stderr does not name %q: %q", key, out)
		}
	}
	for _, key := range []string{"version", "base_url", "api_key"} {
		if strings.Contains(out, key) {
			t.Errorf("stderr warns about known key %q: %q", key, out)
		}
	}
}

// TestSavePreservesUnknownKeys is the bug: --pick saves default_model through
// a Save that marshalled the struct alone, so every option this build does
// not model was deleted from the operator's file. A known field's update must
// leave those options value-for-value intact.
func TestSavePreservesUnknownKeys(t *testing.T) {
	useTempHome(t)
	p, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	writeRaw(t, p, `{
  "version": 1,
  "base_url": "https://switch.example.com",
  "api_key": "sk-known",
  "default_model": "old",
  "mystery_option": {"nested": true},
  "experimental": 42
}`)

	var cfg *Config
	captureStderr(t, func() { cfg, err = Load() })
	if err != nil {
		t.Fatal(err)
	}
	cfg.DefaultModel = "new"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	var got map[string]json.RawMessage
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("saved file is not JSON: %v\n%s", err, data)
	}
	want := map[string]string{
		"mystery_option": `{"nested": true}`,
		"experimental":   `42`,
	}
	for key, wantJSON := range want {
		raw, ok := got[key]
		if !ok {
			t.Errorf("unknown option %q was dropped on save: %s", key, data)
			continue
		}
		var wantVal, gotVal any
		if err := json.Unmarshal([]byte(wantJSON), &wantVal); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &gotVal); err != nil {
			t.Fatalf("%s is not valid JSON: %v", key, err)
		}
		if !reflect.DeepEqual(gotVal, wantVal) {
			t.Errorf("%s = %s, want %s", key, raw, wantJSON)
		}
	}
	var dm string
	if err := json.Unmarshal(got["default_model"], &dm); err != nil || dm != "new" {
		t.Errorf("default_model = %s, want \"new\"", got["default_model"])
	}
}

// TestSavePreservesUnknownKeysAcrossSaves makes sure the preservation is not
// one-shot: the options survive a Load of a file this package wrote, which is
// what the next launch reads.
func TestSavePreservesUnknownKeysAcrossSaves(t *testing.T) {
	useTempHome(t)
	p, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	writeRaw(t, p, `{"version":1,"mystery_option":"keep me"}`)

	var cfg *Config
	captureStderr(t, func() { cfg, err = Load() })
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	var reloaded *Config
	captureStderr(t, func() { reloaded, err = Load() })
	if err != nil {
		t.Fatal(err)
	}
	reloaded.Version = PrizmalConfigVersion
	if err := reloaded.Save(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	var v string
	if err := json.Unmarshal(got["mystery_option"], &v); err != nil || v != "keep me" {
		t.Errorf("mystery_option = %s, want \"keep me\"", got["mystery_option"])
	}
}
