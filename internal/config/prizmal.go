// Package config also holds the Prizmal CLI's own configuration:
// a single file at ~/.prizmal/config.json carrying the Prizmal Switch
// provider base URL and API key.
//
// The API key is stored as plain text by default, matching how the
// downstream coding harnesses store their own credentials. Three forms are
// supported so operators who prefer not to keep a secret on disk can opt out:
//
//	{ "api_key": "sk-plain-text..." }              // plain (default)
//	{ "api_key": { "env": "PRIZMAL_API_KEY" } }   // read from environment
//	{ "api_key": { "command": "op read op://Vault/item/field" } }  // shell out
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	// PrizmalDir is the config directory under the user's home directory.
	PrizmalDir = ".prizmal"
	// PrizmalFileName is the name of the Prizmal CLI config file.
	PrizmalFileName = "config.json"
	// PrizmalConfigVersion is the current config file version.
	PrizmalConfigVersion = 1
)

// ErrNoConfig is returned by Load when no config file exists yet.
var ErrNoConfig = errors.New("prizmal config file not found")

// Config is the persisted Prizmal CLI configuration.
type Config struct {
	Version int          `json:"version"`
	BaseURL string       `json:"base_url,omitempty"`
	APIKey  *APIKeyValue `json:"api_key,omitempty"`
	// DefaultModel is the model the picker chose last. A launch that names no
	// model sends this one. Only a pick writes it, so passing --model changes
	// one launch without changing what a bare launch does.
	DefaultModel string `json:"default_model,omitempty"`
}

// apiKeyKind discriminates the three accepted api_key forms plus an
// unresolvable unknown form kept for forward compatibility.
type apiKeyKind int

const (
	apiKeyPlain apiKeyKind = iota
	apiKeyEnv
	apiKeyCommand
	apiKeyUnknown
)

// APIKeyValue holds the provider credential. It unmarshals from either a
// JSON string (plain) or a JSON object with "env" or "command".
type APIKeyValue struct {
	Plain   string
	Env     string
	Command string
	kind    apiKeyKind
}

// UnmarshalJSON accepts a JSON string or an object with env/command. Unknown
// object shapes are accepted without error (forward compatibility) but the
// value is held as apiKeyUnknown and Resolve reports it unresolvable.
func (a *APIKeyValue) UnmarshalJSON(data []byte) error {
	var plain string
	if err := json.Unmarshal(data, &plain); err == nil {
		a.kind = apiKeyPlain
		a.Plain = plain
		return nil
	}
	var obj struct {
		Env     string `json:"env"`
		Command string `json:"command"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("api_key must be a string or an object with env/command: %w", err)
	}
	switch {
	case obj.Env != "":
		a.kind = apiKeyEnv
		a.Env = obj.Env
	case obj.Command != "":
		a.kind = apiKeyCommand
		a.Command = obj.Command
	default:
		a.kind = apiKeyUnknown
	}
	return nil
}

// MarshalJSON serializes the api_key back to its original form so Save
// round-trips the operator's chosen representation.
func (a APIKeyValue) MarshalJSON() ([]byte, error) {
	switch a.kind {
	case apiKeyEnv:
		return json.Marshal(map[string]string{"env": a.Env})
	case apiKeyCommand:
		return json.Marshal(map[string]string{"command": a.Command})
	case apiKeyUnknown:
		return json.Marshal(map[string]string{})
	default:
		return json.Marshal(a.Plain)
	}
}

// NewPlainAPIKey returns an APIKeyValue in plain form.
func NewPlainAPIKey(s string) *APIKeyValue {
	return &APIKeyValue{Plain: s, kind: apiKeyPlain}
}

// shellCommand returns the operating system's command interpreter: /bin/sh on
// Unix, %COMSPEC% (cmd.exe) on Windows. The command form of api_key shells out
// through it, so it works on every supported platform.
func shellCommand() string {
	if runtime.GOOS == "windows" {
		if c := os.Getenv("COMSPEC"); c != "" {
			return c
		}
		return "cmd.exe"
	}
	return "/bin/sh"
}

// shellFlag is the "run the following command" flag for the shell: -c on
// Unix, /C on cmd.exe.
func shellFlag() string {
	if runtime.GOOS == "windows" {
		return "/C"
	}
	return "-c"
}

// cleanCommandOutput normalizes a command's stdout into the key value. It
// trims surrounding whitespace and strips one optional layer of surrounding
// quotes, which cmd.exe and some secret tools (op, age) emit.
func cleanCommandOutput(out []byte) string {
	v := strings.TrimSpace(string(out))
	if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
		v = strings.TrimSpace(v[1 : len(v)-1])
	}
	return v
}

// Resolve returns the actual key string for the stored form. It never logs
// or writes the resolved value.
func (a APIKeyValue) Resolve() (string, error) {
	switch a.kind {
	case apiKeyEnv:
		v := os.Getenv(a.Env)
		if v == "" {
			return "", fmt.Errorf("environment variable %q for prizmal api_key is not set", a.Env)
		}
		return v, nil
	case apiKeyCommand:
		out, err := exec.Command(shellCommand(), shellFlag(), a.Command).Output()
		if err != nil {
			return "", fmt.Errorf("prizmal api_key command failed: %w", err)
		}
		v := cleanCommandOutput(out)
		if v == "" {
			return "", fmt.Errorf("prizmal api_key command produced no output")
		}
		return v, nil
	case apiKeyPlain:
		return a.Plain, nil
	default:
		return "", fmt.Errorf("prizmal api_key has an unsupported form and cannot be resolved")
	}
}

// Path returns the absolute path to the Prizmal CLI config file.
func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, PrizmalDir, PrizmalFileName), nil
}

// Load reads the config file. It returns ErrNoConfig when no file exists yet,
// which callers use to decide whether to run the first-run prompt.
func Load() (*Config, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNoConfig
		}
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if c.Version == 0 {
		c.Version = PrizmalConfigVersion
	}
	return &c, nil
}

// Save writes the config file, creating the directory with 0700 and the file
// with 0600.
func (c *Config) Save() error {
	if c.Version == 0 {
		c.Version = PrizmalConfigVersion
	}
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(p, data, 0o600)
}
