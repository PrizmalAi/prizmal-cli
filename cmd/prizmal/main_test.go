package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/config"
	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	launcher "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/spf13/cobra"
)

func TestListIntegrationsIncludesCoreHarnesses(t *testing.T) {
	specs := launcher.ListVisibleIntegrationSpecs()
	want := map[string]bool{
		"claude": false, "codex": false, "opencode": false, "pi": false,
		"cline": false,
	}
	for _, spec := range specs {
		if _, ok := want[spec.Name]; ok {
			want[spec.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("expected integration %q to be registered", name)
		}
	}
}

func TestLookupIntegrationAliases(t *testing.T) {
	cases := map[string]string{
		"claude": "claude",
		"CLAUDE": "claude",
	}
	for alias, want := range cases {
		spec, err := launcher.LookupIntegrationSpec(alias)
		if err != nil {
			t.Fatalf("alias %q: %v", alias, err)
		}
		if spec.Name != want {
			t.Errorf("alias %q resolved to %q, want %q", alias, spec.Name, want)
		}
	}
	if _, err := launcher.LookupIntegrationSpec("does-not-exist"); err == nil {
		t.Error("expected error for unknown integration")
	}
}

// TestSplitLaunchInvocation pins the positional grammar: the first non-flag
// token is the integration name and everything after it is harness
// passthrough. The `--` separator is consumed, exactly as the old parser
// ended flag parsing at it, so a harness that needs the token (pi's own
// argument section) still receives the tokens after it.
func TestSplitLaunchInvocation(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantName string
		wantArgs []string
	}{
		{"bare launch", []string{"claude"}, "claude", nil},
		{"harness flags pass through", []string{"claude", "--resume", "a0b08857"},
			"claude", []string{"--resume", "a0b08857"}},
		{"the separator is kept for launch to read", []string{"codex", "--", "--sandbox", "workspace-write"},
			"codex", []string{"--", "--sandbox", "workspace-write"}},
		{"empty invocation", nil, "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotName, gotArgs := splitLaunchInvocation(tc.args)
			if gotName != tc.wantName || !slices.Equal(gotArgs, tc.wantArgs) {
				t.Errorf("splitLaunchInvocation(%v) = %q, %v; want %q, %v",
					tc.args, gotName, gotArgs, tc.wantName, tc.wantArgs)
			}
		})
	}
}

// TestInterspersedParsingOffPinsThePositionalGrammar drives the real cobra /
// pflag parser, so a future dependency bump cannot silently turn interspersed
// parsing back on and start swallowing harness tokens as prizmal flags.
func TestInterspersedParsingOffPinsThePositionalGrammar(t *testing.T) {
	cmd := &cobra.Command{Use: usageLine, Args: cobra.ArbitraryArgs}
	registerFlags(cmd.Flags())
	cmd.Flags().SetInterspersed(false)

	if err := cmd.Flags().Parse([]string{"-y", "claude", "--resume", "a0b08857"}); err != nil {
		t.Fatal(err)
	}
	yesFlag, err := cmd.Flags().GetBool("yes")
	if err != nil {
		t.Fatal(err)
	}
	if !yesFlag {
		t.Error("-y before the integration name must be parsed as prizmal's flag")
	}
	got := cmd.Flags().Args()
	want := []string{"claude", "--resume", "a0b08857"}
	if !slices.Equal(got, want) {
		t.Fatalf("Args() = %v, want %v: a harness flag after the integration name must survive parsing untouched", got, want)
	}
}

func TestBaseURLResolution(t *testing.T) {
	t.Setenv(envconfig.EnvVar, "")
	envconfig.SetBaseURL("")
	if got := envconfig.BaseURL(); got != envconfig.DefaultURL {
		t.Errorf("default URL = %q, want %q", got, envconfig.DefaultURL)
	}

	t.Setenv(envconfig.EnvVar, "http://localhost:1234/")
	if got := envconfig.BaseURL(); got != "http://localhost:1234" {
		t.Errorf("env URL = %q, want trailing slash trimmed", got)
	}

	envconfig.SetBaseURL("http://example.com:9999")
	if got := envconfig.BaseURL(); got != "http://example.com:9999" {
		t.Errorf("flag override = %q, want http://example.com:9999", got)
	}
	if got := envconfig.Host().Host; got != "example.com:9999" {
		t.Errorf("Host().Host = %q", got)
	}
	envconfig.SetBaseURL("")
}

func TestRestoreUnsupportedIntegration(t *testing.T) {
	spec, err := launcher.LookupIntegrationSpec("claude")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := spec.Runner.(interface {
		Restore() (launcher.RestoreOutcome, error)
	}); ok {
		t.Error("claude runner should not implement Restore")
	}
	spec, err = launcher.LookupIntegrationSpec("codex")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := spec.Runner.(interface {
		Restore() (launcher.RestoreOutcome, error)
	}); !ok {
		t.Error("codex runner should implement Restore")
	}
	// Pi writes the live key into ~/.pi/agent/models.json because it has no
	// environment channel for a custom provider, so it must offer the undo.
	spec, err = launcher.LookupIntegrationSpec("pi")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := spec.Runner.(interface {
		Restore() (launcher.RestoreOutcome, error)
	}); !ok {
		t.Error("pi runner should implement Restore")
	}
	// Cline is in the same position as pi: its provider entry in
	// providers.json carries the live key, so it needs the same undo.
	spec, err = launcher.LookupIntegrationSpec("cline")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := spec.Runner.(interface {
		Restore() (launcher.RestoreOutcome, error)
	}); !ok {
		t.Error("cline runner should implement Restore")
	}
}

func TestConfigPersistence(t *testing.T) {
	home := t.TempDir()
	// os.UserHomeDir() reads $HOME on Unix and %USERPROFILE% on Windows, so
	// redirect whichever the platform respects to keep the temp dir in play.
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if err := config.SaveIntegration("codex", []string{"gpt-oss:20b"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadIntegration("codex")
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil || len(cfg.Models) != 1 || cfg.Models[0] != "gpt-oss:20b" {
		t.Fatalf("unexpected persisted config: %+v", cfg)
	}
	if _, err := os.Stat(filepath.Join(home, ".prizmal", "config.json")); err != nil {
		t.Errorf("config file missing: %v", err)
	}
}

func TestEnvVarNameIsStable(t *testing.T) {
	if envconfig.EnvVar != "PRIZMAL_SWITCH_URL" {
		t.Errorf("unexpected env var %q", envconfig.EnvVar)
	}
}

// TestRestoreReportSaysWhatHappened pins the line --restore prints to what
// Restore did. A no-op on a machine prizmal never configured and a removal
// used to print the same "removed" line.
func TestRestoreReportSaysWhatHappened(t *testing.T) {
	cases := []struct {
		name    string
		outcome launcher.RestoreOutcome
		want    string
	}{
		{"no-op", launcher.RestoreOutcome{},
			"Nothing to restore: Pi has no Prizmal launch configuration."},
		{"removed", launcher.RestoreOutcome{Removed: true},
			"Pi launch configuration removed."},
		{"reinstated", launcher.RestoreOutcome{Removed: true, Reinstated: []string{"defaultProvider=myprov", "defaultModel=user-model-1"}},
			"Pi launch configuration removed. Put back defaultProvider=myprov, defaultModel=user-model-1."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := restoreReport("Pi", "Pi launch configuration removed.", tc.outcome)
			if got != tc.want {
				t.Errorf("restoreReport = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRestoreSweepsSwitchCredentialsFromBackups: --restore is where an
// operator goes to get prizmal off the machine, so it also removes retained
// backups holding a Switch key, including one the configured key does not
// match.
func TestRestoreSweepsSwitchCredentialsFromBackups(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(envconfig.KeyEnvVar, "")

	tainted := filepath.Join(home, ".prizmal", "backup", "pi", "models.json.1700000000")
	if err := os.MkdirAll(filepath.Dir(tainted), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"providers":{"prizmal":{"baseUrl":"https://api.prizmal.ai/v1","apiKey":"not-a-real-key-rotated"}}}`
	if err := os.WriteFile(tainted, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	restore = true
	t.Cleanup(func() { restore = false })
	if err := launch("pi", nil, &config.Config{}, askNobody); err != nil {
		t.Fatalf("launch --restore: %v", err)
	}
	if _, err := os.Lstat(tainted); err == nil {
		t.Fatalf("--restore kept a backup holding a Switch key")
	}
}

// prizmal owns the model decision, so a --model after the integration name is
// prizmal's own and must not reach the harness. Handing it through let the
// harness's flag outrank prizmal's settings, which is how a launch lost its 1M
// window and warned that the model was unknown.
func TestTakeModelFlagConsumesTheHarnessModelArgument(t *testing.T) {
	cases := []struct {
		name     string
		extra    []string
		want     []string
		wantRest []string
	}{
		{
			name:     "separate value",
			extra:    []string{"--model", "smart"},
			want:     []string{"smart"},
			wantRest: nil,
		},
		{
			name:     "joined value",
			extra:    []string{"--model=smart"},
			want:     []string{"smart"},
			wantRest: nil,
		},
		{
			name:     "both spellings together",
			extra:    []string{"--model", "a", "--model=b"},
			want:     []string{"a", "b"},
			wantRest: nil,
		},
		{
			name:     "other arguments survive, positions kept",
			extra:    []string{"--verbose", "--model", "smart", "--resume", "abc"},
			want:     []string{"smart"},
			wantRest: []string{"--verbose", "--resume", "abc"},
		},
		{
			name:     "after the separator is harness text, not prizmal's flag",
			extra:    []string{"--", "--model", "smart"},
			want:     nil,
			wantRest: []string{"--", "--model", "smart"},
		},
		{
			name:     "no model flag",
			extra:    []string{"--verbose"},
			want:     nil,
			wantRest: []string{"--verbose"},
		},
		{
			name:     "a trailing --model with no value is left for the harness to reject",
			extra:    []string{"--model"},
			want:     nil,
			wantRest: []string{"--model"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			extra := slices.Clone(tc.extra)
			got := takeModelFlag(&extra)
			if !slices.Equal(got, tc.want) {
				t.Errorf("takeModelFlag(%v) = %v, want %v", tc.extra, got, tc.want)
			}
			if !slices.Equal(extra, tc.wantRest) {
				t.Errorf("takeModelFlag(%v) left extra = %v, want %v", tc.extra, extra, tc.wantRest)
			}
		})
	}
}

// One model, one place. A flag before the integration name and one after it are
// two answers to the same question, so prizmal refuses the launch rather than
// letting the second silently lose.
func TestReconcileModelRejectsTwoDifferentModels(t *testing.T) {
	got, err := reconcileModel("smart", []string{"flash"})
	if err == nil {
		t.Fatalf("two different models did not error; got %q", got)
	}
	if !strings.Contains(err.Error(), "smart") || !strings.Contains(err.Error(), "flash") {
		t.Errorf("error does not name both values: %v", err)
	}

	// The same model named both ways is one decision, not a conflict. This is
	// the `prizmal --model X claude --model X` case.
	got, err = reconcileModel("smart", []string{"smart"})
	if err != nil {
		t.Fatalf("the same model twice errored: %v", err)
	}
	if got != "smart" {
		t.Errorf("reconcileModel = %q, want smart", got)
	}

	// A repeated value is one decision too.
	if got, err := reconcileModel("", []string{"smart", "smart"}); err != nil || got != "smart" {
		t.Errorf("a repeated model = %q, %v; want smart, nil", got, err)
	}

	// No harness model leaves prizmal's own flag alone, and a harness model
	// with no flag is the whole value. The empty flagModel is the saved-default
	// case: the harness form must override a default, not conflict with it.
	if got, err := reconcileModel("smart", nil); err != nil || got != "smart" {
		t.Errorf("no harness model = %q, %v; want smart, nil", got, err)
	}
	if got, err := reconcileModel("", []string{"flash"}); err != nil || got != "flash" {
		t.Errorf("harness model over a saved default = %q, %v; want flash, nil", got, err)
	}
}

// A --model after the integration name overrides the saved default, exactly as
// the flag form does. Only an explicit --model before the name can conflict.
func TestHarnessModelOverridesTheSavedDefault(t *testing.T) {
	cfg := &config.Config{DefaultModel: "saved-default"}
	// Both cases resolve from a named model and never open a menu, so the
	// launch carries no way to ask.
	ask := askNobody

	chosen, _, err := resolveLaunchModel(cfg, "operator-choice", ask)
	if err != nil {
		t.Fatalf("resolveLaunchModel: %v", err)
	}
	if chosen != "operator-choice" {
		t.Errorf("chosen = %q, want operator-choice (the harness form must outrank the default)", chosen)
	}

	chosen, _, err = resolveLaunchModel(cfg, "", ask)
	if err != nil {
		t.Fatalf("resolveLaunchModel: %v", err)
	}
	if chosen != "saved-default" {
		t.Errorf("chosen = %q, want saved-default", chosen)
	}
}

// A launch reads the harness arguments the way the command line wrote them.
// splitLaunchInvocation keeps the `--` separator so takeModelFlag can stop at
// it: a --model after the separator is the harness's own, and the operator
// meant it for the harness. dropSeparators then removes the separator, which
// the harness never sees.
func TestLaunchArgsLeaveAModelAfterTheSeparatorToTheHarness(t *testing.T) {
	_, extra := splitLaunchInvocation([]string{"claude", "--verbose", "--", "--model", "smart"})
	if got := takeModelFlag(&extra); got != nil {
		t.Fatalf("takeModelFlag consumed %v from after the separator", got)
	}
	if got, want := dropSeparators(extra), []string{"--verbose", "--model", "smart"}; !slices.Equal(got, want) {
		t.Fatalf("harness arguments = %v, want %v", got, want)
	}
}

func TestDropSeparatorsRemovesOnlySeparators(t *testing.T) {
	got := dropSeparators([]string{"--", "--sandbox", "--", "workspace-write"})
	if want := []string{"--sandbox", "workspace-write"}; !slices.Equal(got, want) {
		t.Fatalf("dropSeparators = %v, want %v", got, want)
	}
	if got := dropSeparators(nil); len(got) != 0 {
		t.Fatalf("dropSeparators(nil) = %v, want empty", got)
	}
}
