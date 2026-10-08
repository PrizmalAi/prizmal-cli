//go:build !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/stubserver"
)

// A launch names a model the Switch routes, or stops before it starts the
// harness. The harness here is a stand-in that leaves a marker file, so the
// test sees whether prizmal started it.
func TestLaunchStopsOnANameTheSwitchDoesNotRoute(t *testing.T) {
	srv := stubserver.NewServer(stubserver.WithEntries(
		stubserver.Entry{ID: "prizmal-flash", Tier: "haiku"},
		stubserver.Entry{ID: "prizmal-core"},
	))
	t.Cleanup(srv.Close)
	prizmalBin := buildPrizmal(t)

	for _, tc := range []struct {
		name     string
		args     []string
		wantErr  string
		wantRuns bool
	}{
		{name: "a listed name", args: []string{"-m", "prizmal-core"}, wantRuns: true},
		{name: "a held tier alias", args: []string{"-m", "claude-tier-haiku"}, wantRuns: true},
		{name: "an unknown name", args: []string{"-m", "gpt-5.6-luna"}, wantErr: `"gpt-5.6-luna" is not a name this Switch routes`},
		{name: "a tier the tenant does not hold", args: []string{"-m", "claude-tier-opus"}, wantErr: `"claude-tier-opus" is not a name this Switch routes`},
		{name: "default", args: []string{"-m", "default"}, wantErr: "names the router config the key is bound to"},
		{name: "an unknown subagent model", args: []string{"-m", "prizmal-core", "--subagent-model", "gpt-5.6-luna"}, wantErr: `--subagent-model: model "gpt-5.6-luna"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			home := filepath.Join(root, "home")
			bin := filepath.Join(root, "bin")
			marker := filepath.Join(root, "ran")
			for _, dir := range []string{filepath.Join(home, ".prizmal"), bin} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			writeJSONFile(t, filepath.Join(home, ".prizmal", "config.json"), map[string]any{
				"version": 1, "base_url": srv.URL, "api_key": stubserver.StubKey,
			})
			writeScript(t, filepath.Join(bin, "claude"), "#!/bin/sh\ntouch "+shellQuote(marker)+"\n")

			cmd := exec.Command(prizmalBin, append(append([]string{"-y"}, tc.args...), "claude")...)
			cmd.Env = []string{"HOME=" + home, "PATH=" + bin + ":/usr/bin:/bin", "PRIZMAL_ENV=testing"}
			out, err := cmd.CombinedOutput()

			_, statErr := os.Stat(marker)
			ran := statErr == nil
			if ran != tc.wantRuns {
				t.Errorf("harness ran = %v, want %v\n%s", ran, tc.wantRuns, out)
			}
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("launch failed: %v\n%s", err, out)
				}
				return
			}
			if err == nil {
				t.Errorf("launch succeeded, want a refusal\n%s", out)
			}
			if !strings.Contains(string(out), tc.wantErr) {
				t.Errorf("output does not contain %q:\n%s", tc.wantErr, out)
			}
		})
	}
}
