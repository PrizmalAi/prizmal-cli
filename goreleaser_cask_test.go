package prizmalcli

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestCaskUsesPostflightSteps pins the cask's quarantine step to Homebrew's
// declarative postflight_steps. goreleaser renders hooks.post.install as a
// Ruby postflight block, and Homebrew 7 prints "Calling `postflight` is
// deprecated!" on every install of a cask that has one.
func TestCaskUsesPostflightSteps(t *testing.T) {
	raw, err := os.ReadFile(".goreleaser.yaml")
	if err != nil {
		t.Fatalf("read .goreleaser.yaml: %v", err)
	}
	var cfg struct {
		Casks []struct {
			Name        string         `yaml:"name"`
			Hooks       map[string]any `yaml:"hooks"`
			CustomBlock string         `yaml:"custom_block"`
		} `yaml:"homebrew_casks"`
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse .goreleaser.yaml: %v", err)
	}
	if len(cfg.Casks) == 0 {
		t.Fatal(".goreleaser.yaml has no homebrew_casks entry")
	}
	for _, c := range cfg.Casks {
		if len(c.Hooks) > 0 {
			t.Errorf("cask %s sets hooks, which goreleaser renders as a deprecated postflight block", c.Name)
		}
		if !strings.Contains(c.CustomBlock, "postflight_steps do") {
			t.Errorf("cask %s custom_block has no postflight_steps block to strip the quarantine flag", c.Name)
		}
	}
}
