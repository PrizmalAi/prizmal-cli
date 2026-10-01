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

// autoModeCase is one launch whose Claude Code session must start in auto
// mode.
type autoModeCase struct {
	name    string
	args    []string
	catalog []string
	entries []stubserver.Entry
}

// claudeAutoModeCases launches a model of each tier, named by a tier alias and
// by a router config the Switch marks with the tier.
var claudeAutoModeCases = []autoModeCase{
	{name: "tier-alias-opus", args: []string{"claude", "--model", "claude-tier-opus"}, catalog: tierCatalog},
	{name: "tier-alias-sonnet", args: []string{"claude", "--model", "claude-tier-sonnet"}, catalog: tierCatalog},
	{name: "tier-alias-haiku", args: []string{"claude", "--model", "claude-tier-haiku"}, catalog: tierCatalog},
	{name: "tier-alias-fable", args: []string{"claude", "--model", "claude-tier-fable"}, catalog: tierCatalog},
	{name: "switch-tier-opus", args: []string{"claude", "--model", "smart"}, entries: proposedCatalog},
	{name: "switch-tier-sonnet", args: []string{"claude", "--model", "balanced"}, entries: proposedCatalog},
	{name: "switch-tier-haiku", args: []string{"claude", "--model", "flash"}, entries: proposedCatalog},
	{name: "switch-tier-fable", args: []string{"claude", "--model", "deep"}, entries: proposedCatalog},
	{name: "no-tier", args: []string{"claude", "--model", "experimental"}, entries: proposedCatalog},
}

// Claude Code starts a session in auto mode when auto mode is open to its
// model, and in manual mode when it is not. Whether it is open depends on the
// model the session runs as, which is the behavesAs prizmal writes, and on a
// rule inside Claude Code that a release can change. Behind a gateway, Claude
// Code 2.1.283 to 2.1.287 refuse auto mode to any model that runs as a haiku
// model, so a haiku behavesAs started those sessions in manual mode, with no
// way to cycle into auto.
//
// The test runs the Claude Code release that the baselines pin, so a change
// to that rule in a new release fails here when the pin moves.
func TestClaudeTiersStartInAutoMode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Claude Code launches in short mode")
	}
	require := os.Getenv(baselineRequireEnv) == "require"
	skipOrFail := func(t *testing.T, format string, args ...any) {
		t.Helper()
		if require {
			t.Fatalf(format, args...)
		}
		t.Skipf(format, args...)
	}

	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		skipOrFail(t, "tmux is not on PATH")
	}
	claudeDir, claudeReason := pinnedClaudeDir(t)
	if claudeDir == "" {
		skipOrFail(t, "%s", claudeReason)
	}

	prizmalBin := filepath.Join(t.TempDir(), "prizmal")
	if out, err := exec.Command("go", "build", "-o", prizmalBin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build prizmal: %v\n%s", err, out)
	}

	for _, tc := range claudeAutoModeCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			screen, _ := renderBaseline(t, tmuxPath, prizmalBin, claudeDir, baselineCase{
				name: tc.name, cols: 100, rows: 30,
				args: tc.args, catalog: tc.catalog, entries: tc.entries, claude: true,
				steps: []baselineStep{stepClaudeReady},
			})
			if !strings.Contains(screen, "auto mode on") {
				t.Errorf("prizmal %s started Claude Code outside auto mode:\n%s", strings.Join(tc.args, " "), screen)
			}
		})
	}
}
