package fileutil

import (
	"encoding/json"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
)

// Field names that hold the key and the endpoint in every config prizmal has
// written a Switch key into: JSON (pi, cline, opencode, droid, openclaw),
// TOML (codex) and YAML (hermes, omp).
var (
	credentialFields = []string{"apiKey", "api_key"}
	endpointFields   = []string{"baseUrl", "baseURL", "base_url"}
)

// SweepBackups removes every retained copy under BackupDir that holds a
// Switch credential, and returns the paths it removed.
//
// The write-time scrub matches the configured key exactly, so it cannot see
// a key that has since been rotated, a key from another tenant or profile,
// or a copy of a file the current build no longer writes. The sweep judges a
// copy by its structure instead: a populated key field in the same object as
// an endpoint on the Switch is a Switch credential whatever its value. A
// key for any other endpoint is the user's own and stays, as does a copy
// with no key in it. The configured key is also matched exactly, anywhere in
// any file, as a reinforcement.
//
// It reads and removes only regular files inside BackupDir, and does not
// follow symlinks.
func SweepBackups() []string {
	root := BackupDir()
	var removed []string
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if carriesCredential(data) || holdsSwitchCredential(data) {
			if os.Remove(path) == nil {
				removed = append(removed, path)
			}
		}
		return nil
	})
	return removed
}

// holdsSwitchCredential parses data as JSON, then TOML, then YAML, and
// reports whether any object in it pairs a key with a Switch endpoint. A file
// that parses as none of them is left to the exact match.
func holdsSwitchCredential(data []byte) bool {
	var doc any
	if json.Unmarshal(data, &doc) == nil {
		return pairsKeyWithSwitch(doc)
	}
	var table map[string]any
	if toml.Unmarshal(data, &table) == nil {
		return pairsKeyWithSwitch(table)
	}
	if yaml.Unmarshal(data, &doc) == nil {
		return pairsKeyWithSwitch(doc)
	}
	return false
}

func pairsKeyWithSwitch(node any) bool {
	switch v := node.(type) {
	case map[string]any:
		if hasPopulatedString(v, credentialFields) && pointsAtSwitch(v) {
			return true
		}
		for _, child := range v {
			if pairsKeyWithSwitch(child) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if pairsKeyWithSwitch(child) {
				return true
			}
		}
	}
	return false
}

func hasPopulatedString(m map[string]any, fields []string) bool {
	for _, field := range fields {
		if s, ok := m[field].(string); ok && strings.TrimSpace(s) != "" {
			return true
		}
	}
	return false
}

func pointsAtSwitch(m map[string]any) bool {
	for _, field := range endpointFields {
		if s, ok := m[field].(string); ok && isSwitchEndpoint(s) {
			return true
		}
	}
	return false
}

// isSwitchEndpoint reports whether raw is on a prizmal.ai host, which covers
// every tenant and environment, or on the host this process is configured
// for, which covers a self-hosted or local Switch.
func isSwitchEndpoint(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "prizmal.ai" || strings.HasSuffix(host, ".prizmal.ai") {
		return true
	}
	for _, configured := range []*url.URL{envconfig.Host(), envconfig.ConnectableHost()} {
		if configured != nil && strings.EqualFold(configured.Host, u.Host) {
			return true
		}
	}
	return false
}
