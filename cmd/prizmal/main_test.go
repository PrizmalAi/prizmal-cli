package main

import (
	"os"
	"path/filepath"
	"slices"
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
		{"old separator is consumed", []string{"codex", "--", "--sandbox", "workspace-write"},
			"codex", []string{"--sandbox", "workspace-write"}},
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
	if err := launch("pi", nil, &config.Config{}); err != nil {
		t.Fatalf("launch --restore: %v", err)
	}
	if _, err := os.Lstat(tainted); err == nil {
		t.Fatalf("--restore kept a backup holding a Switch key")
	}
}
