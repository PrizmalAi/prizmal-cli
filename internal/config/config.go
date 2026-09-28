// Package config persists per-integration state (saved models, onboarding
// marks) for prizmal, in a single JSON file at ~/.prizmal/config.json.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// IntegrationConfig is the persisted config for one integration.
type IntegrationConfig struct {
	Models    []string `json:"models,omitempty"`
	Onboarded bool     `json:"onboarded,omitempty"`
}

type file struct {
	Integrations map[string]*IntegrationConfig `json:"integrations,omitempty"`
}

var mu sync.Mutex

func path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".prizmal", "config.json"), nil
}

func load() (*file, error) {
	p, err := path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return &file{Integrations: map[string]*IntegrationConfig{}}, nil
		}
		return nil, err
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return &file{Integrations: map[string]*IntegrationConfig{}}, nil
	}
	if f.Integrations == nil {
		f.Integrations = map[string]*IntegrationConfig{}
	}
	return &f, nil
}

func save(f *file) error {
	p, err := path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

// LoadIntegration returns the persisted config for an integration, if any.
func LoadIntegration(appName string) (*IntegrationConfig, error) {
	mu.Lock()
	defer mu.Unlock()
	f, err := load()
	if err != nil {
		return nil, err
	}
	cfg, ok := f.Integrations[appName]
	if !ok || cfg == nil {
		return nil, nil //nolint:nilnil
	}
	return cfg, nil
}

// SaveIntegration persists the saved model list for an integration.
func SaveIntegration(appName string, models []string) error {
	mu.Lock()
	defer mu.Unlock()
	f, err := load()
	if err != nil {
		return err
	}
	f.Integrations[appName] = &IntegrationConfig{Models: models}
	return save(f)
}

// MarkIntegrationOnboarded records that an integration has been onboarded.
func MarkIntegrationOnboarded(appName string) error {
	mu.Lock()
	defer mu.Unlock()
	f, err := load()
	if err != nil {
		return err
	}
	cfg, ok := f.Integrations[appName]
	if !ok || cfg == nil {
		cfg = &IntegrationConfig{}
		f.Integrations[appName] = cfg
	}
	cfg.Onboarded = true
	return save(f)
}
