package registry

import (
	"os"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/cline"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/codex"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/pi"
)

// TestRestoreOnNeverRunHomeIsANoOp: on a machine prizmal never configured,
// --restore has nothing to remove and must say so instead of reporting a
// removal. It also writes nothing, so an untouched home stays untouched.
// restoreOnly is the --restore half of a launcher; codex has it without
// being an Editor, so the credential-at-rest table cannot drive it.
type restoreOnly interface {
	Restore() (launch.RestoreOutcome, error)
}

func TestRestoreOnNeverRunHomeIsANoOp(t *testing.T) {
	cases := []struct {
		name     string
		launcher restoreOnly
	}{
		{"pi", &pi.Pi{}},
		{"cline", &cline.Cline{}},
		{"codex", &codex.Codex{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := internaltest.SandboxedHome(t)
			internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)

			outcome, err := tc.launcher.Restore()
			if err != nil {
				t.Fatalf("Restore on an empty home: %v", err)
			}
			if outcome.Removed {
				t.Error("outcome.Removed = true on a home prizmal never configured")
			}
			if len(outcome.Reinstated) != 0 {
				t.Errorf("outcome.Reinstated = %v, want nothing put back", outcome.Reinstated)
			}
			entries, err := os.ReadDir(home)
			if err != nil {
				t.Fatalf("read home: %v", err)
			}
			for _, entry := range entries {
				t.Errorf("restore created %s in a home it had nothing to restore in", entry.Name())
			}
		})
	}
}
