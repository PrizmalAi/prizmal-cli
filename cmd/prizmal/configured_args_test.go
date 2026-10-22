package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/config"
)

func TestWithConfiguredArgs(t *testing.T) {
	cfg := &config.Config{Harnesses: map[string]config.HarnessConfig{
		"claude": {Args: "--allow-dangerously-skip-permissions --effort high"},
	}}

	got, err := withConfiguredArgs(cfg, "claude", []string{"--effort", "low"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"--effort", "low", "--allow-dangerously-skip-permissions"}; !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}

	// Another harness's section never reaches this one.
	got, err = withConfiguredArgs(cfg, "codex", []string{"exec"})
	if err != nil || !slices.Equal(got, []string{"exec"}) {
		t.Fatalf("codex got %q, %v", got, err)
	}

	// No config file at all leaves the arguments alone.
	got, err = withConfiguredArgs(nil, "claude", []string{"-p"})
	if err != nil || !slices.Equal(got, []string{"-p"}) {
		t.Fatalf("nil config got %q, %v", got, err)
	}
}

func TestWithConfiguredArgsNamesABadString(t *testing.T) {
	cfg := &config.Config{Harnesses: map[string]config.HarnessConfig{"claude": {Args: `--x "open`}}}
	_, err := withConfiguredArgs(cfg, "claude", nil)
	if err == nil || !strings.Contains(err.Error(), "harnesses.claude.args") {
		t.Fatalf("err = %v, want one naming harnesses.claude.args", err)
	}
}
