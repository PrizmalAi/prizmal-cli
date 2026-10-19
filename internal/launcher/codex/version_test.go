package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The floor is the release the terminal baselines pin. The launch passes
// --no-daemon, command-backed auth, `debug models --bundled`, the
// supports_search_tool and apply_patch_tool_type catalog fields and
// agents.default_subagent_model, and Codex 0.160 is the first release the
// stack was run against. Tying the two numbers together means a bump of the
// pinned release moves the floor with it.
func TestCodexVersionFloorMatchesThePinnedBaselineRelease(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "cmd", "prizmal", "testdata", "terminal", "codex", "codex-version"))
	if err != nil {
		t.Fatal(err)
	}
	if pinned := strings.TrimSpace(string(data)); pinned != codexMinVersion {
		t.Fatalf("codexMinVersion = %q, but the baselines pin Codex %q", codexMinVersion, pinned)
	}
}

func TestCheckCodexVersionRejectsAReleaseBelowTheFloor(t *testing.T) {
	skipWithoutShell(t)
	fakeBin(t, map[string]string{"codex": `echo "codex-cli 0.134.0"`})

	err := checkCodexVersion()
	if err == nil || !strings.Contains(err.Error(), "too old") || !strings.Contains(err.Error(), codexMinVersion) {
		t.Fatalf("checkCodexVersion = %v, want a too-old error naming %s", err, codexMinVersion)
	}
}

func TestCheckCodexVersionAcceptsTheFloor(t *testing.T) {
	skipWithoutShell(t)
	fakeBin(t, map[string]string{"codex": `echo "codex-cli ` + codexMinVersion + `"`})

	if err := checkCodexVersion(); err != nil {
		t.Fatalf("checkCodexVersion at the floor: %v", err)
	}
}
