package registry

import (
	"testing"

	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/claude"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/cline"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/codex"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/opencode"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/pi"
)

// OwnsModelFlag is opt-in, and only Claude Code opts in. pi and codex read
// --model for something other than the launch model, so the CLI must leave
// their arguments alone; a runner that starts returning true would change which
// provider pi runs.
func TestOnlyClaudeOwnsTheModelFlag(t *testing.T) {
	owner := func(r launch.Runner) bool {
		o, ok := r.(launch.OwningModelFlag)
		return ok && o.OwnsModelFlag()
	}
	if !owner(&claude.Claude{}) {
		t.Error("Claude does not own --model; the CLI would forward it and the harness would outrank prizmal's settings")
	}
	for _, r := range []launch.Runner{&pi.Pi{}, &codex.Codex{}, &opencode.OpenCode{}, &cline.Cline{}} {
		if owner(r) {
			t.Errorf("%s claims --model; pi reads a provider-qualified --model as a provider choice, and codex already refuses the flag", r.String())
		}
	}
}
