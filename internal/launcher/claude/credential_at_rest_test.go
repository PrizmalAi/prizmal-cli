package claude

import (
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
)

// Claude writes nothing at all; the key exists only in the child environment
// assembled here.
func TestClaudeLeavesNoKeyOnDisk(t *testing.T) {
	home := internaltest.SandboxedHome(t)
	internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)

	(&Claude{}).envVars()
	internaltest.AssertNoKeyUnder(t, home, "claude")
}
