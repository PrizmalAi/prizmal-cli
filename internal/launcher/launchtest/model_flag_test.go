package launchtest

import (
	"testing"

	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/claude"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/cline"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/codex"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/opencode"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/pi"
)

// OwnsModelFlag is opt-in. Claude Code and Codex opt in: the launch states the
// model itself. pi reads a provider-qualified --model as a provider choice, and
// cline and opencode have no such flag, so the CLI leaves their arguments alone;
// a runner that starts returning true would change what those harnesses run.
func TestOnlyClaudeAndCodexOwnTheModelFlag(t *testing.T) {
	owner := func(r launch.Runner) bool {
		o, ok := r.(launch.OwningModelFlag)
		return ok && o.OwnsModelFlag()
	}
	for _, r := range []launch.Runner{&claude.Claude{}, &codex.Codex{}} {
		if !owner(r) {
			t.Errorf("%s does not own --model; the CLI would forward it and the harness would outrank prizmal's settings", r.String())
		}
	}
	for _, r := range []launch.Runner{&pi.Pi{}, &opencode.OpenCode{}, &cline.Cline{}} {
		if owner(r) {
			t.Errorf("%s claims --model, which it reads for something other than the launch model", r.String())
		}
	}
}
