package opencode

import (
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

// A plain (non---persist) run must leave nothing key-shaped in the home
// directory: the credential rides the child environment and dies with the
// process.
func TestOpenCodeLeavesNoKeyOnDisk(t *testing.T) {
	home := internaltest.SandboxedHome(t)
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)

	if err := (&OpenCode{}).Edit([]launch.LaunchModel{{Name: "probe-model"}}); err != nil {
		t.Fatalf("configure opencode: %v", err)
	}
	internaltest.AssertNoKeyUnder(t, home, "opencode")
}
