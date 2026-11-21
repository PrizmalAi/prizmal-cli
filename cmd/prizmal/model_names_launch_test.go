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

// A name the operator passes reaches the harness as it is, whether or not the
// catalog lists it: GET /v1/models lists each config once, under its own name,
// and never the aliases the Switch also routes to it. The Switch is the
// authority on names and answers one it does not route with its own error.
// `default` is one more name: the CLI's only default is default_model.
//
// The catalog here is shaped like a tenant whose Opus config holds the
// frontier tier and answers to aliases the listing does not carry, such as the
// legacy claude-tier-opus. The harness is a stand-in that writes its
// arguments to a marker file, so the test sees whether prizmal started it and
// which model it was handed. A name the catalog does not place gets a warning
// before the harness starts, so a typo shows up before the Switch's error.
// unlistedWarning is the text of the startup warning after the model it is about.
const unlistedWarning = "is not in the model list this Switch serves"

func TestLaunchSendsAnExplicitNameTheCatalogDoesNotList(t *testing.T) {
	srv := stubserver.NewServer(stubserver.WithEntries(
		stubserver.Entry{ID: "prizmal-max", Tiers: []string{"frontier"}},
		stubserver.Entry{ID: "prizmal-flash", Tier: "haiku"},
		stubserver.Entry{ID: "prizmal-core"},
	))
	t.Cleanup(srv.Close)
	prizmalBin := buildPrizmal(t)

	for _, tc := range []struct {
		name      string
		args      []string
		wantErr   string
		wantModel string
		// wantWarn is the subject of the startup warning for a name the
		// catalog does not list, empty when the launch must not warn.
		wantWarn string
	}{
		{name: "a listed name", args: []string{"-m", "prizmal-core"}, wantModel: `"model":"prizmal-core[1m]"`},
		{name: "a held tier alias", args: []string{"-m", "claude-tier-haiku"}, wantModel: `"model":"claude-tier-haiku`},
		{name: "a legacy alias the listing does not carry", args: []string{"-m", "claude-tier-opus"}, wantModel: `"model":"claude-tier-opus[1m]"`, wantWarn: `model "claude-tier-opus"`},
		{name: "the same alias after the integration name", args: []string{"claude", "--model", "claude-tier-opus"}, wantModel: `"model":"claude-tier-opus[1m]"`, wantWarn: `model "claude-tier-opus"`},
		{name: "a name the CLI cannot place", args: []string{"-m", "gpt-5.6-luna"}, wantModel: `"model":"gpt-5.6-luna[1m]"`, wantWarn: `model "gpt-5.6-luna"`},
		{name: "an unheld tier word", args: []string{"-m", "sonnet"}, wantModel: `"model":"sonnet[1m]"`, wantWarn: `model "sonnet"`},
		{name: "a subagent model the listing does not carry", args: []string{"-m", "prizmal-core", "--subagent-model", "claude-tier-opus"}, wantModel: `"model":"prizmal-core[1m]"`, wantWarn: `--subagent-model: model "claude-tier-opus"`},
		// `default` is a model name like any other: the CLI sends it, and the
		// Switch answers it.
		{name: "default", args: []string{"-m", "default"}, wantModel: `"model":"default[1m]"`, wantWarn: `model "default"`},
		{name: "prizmal/default", args: []string{"-m", "prizmal/default"}, wantModel: `"model":"prizmal/default[1m]"`, wantWarn: `model "prizmal/default"`},
		{name: "default after the integration name", args: []string{"claude", "--model", "default"}, wantModel: `"model":"default[1m]"`, wantWarn: `model "default"`},
		{name: "a default subagent model", args: []string{"-m", "prizmal-core", "--subagent-model", "default"}, wantModel: `"model":"prizmal-core[1m]"`, wantWarn: `--subagent-model: model "default"`},
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
			writeScript(t, filepath.Join(bin, "claude"), "#!/bin/sh\nprintf '%s\\n' \"$@\" > "+shellQuote(marker)+"\n")

			args := append([]string{"-y"}, tc.args...)
			if args[1] != "claude" {
				args = append(args, "claude")
			}
			cmd := exec.Command(prizmalBin, args...)
			cmd.Env = []string{"HOME=" + home, "PATH=" + bin + ":/usr/bin:/bin", "PRIZMAL_ENV=testing"}
			out, err := cmd.CombinedOutput()

			harnessArgs, statErr := os.ReadFile(marker)
			ran := statErr == nil
			if tc.wantErr == "" {
				if err != nil || !ran {
					t.Fatalf("launch failed (harness ran = %v): %v\n%s", ran, err, out)
				}
				if !strings.Contains(string(harnessArgs), tc.wantModel) {
					t.Errorf("harness arguments do not contain %s:\n%s", tc.wantModel, harnessArgs)
				}
				warned := strings.Contains(string(out), unlistedWarning)
				if tc.wantWarn == "" && warned {
					t.Errorf("launch warned about a name the catalog places:\n%s", out)
				}
				if tc.wantWarn != "" && !strings.Contains(string(out), "warning: "+tc.wantWarn+" "+unlistedWarning) {
					t.Errorf("output does not warn about %s:\n%s", tc.wantWarn, out)
				}
				return
			}
			if ran {
				t.Errorf("harness ran, want a refusal before it starts\n%s", out)
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
