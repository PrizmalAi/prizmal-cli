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

// OwnsModelFlag is opt-in, and only Claude Code and Codex opt in. Both take
// the launch model from prizmal's own resolution, so the CLI consumes a --model
// typed after the integration name. pi reads a provider-qualified --model as a
// provider choice, so a runner that starts returning true there would change
// which provider pi runs.
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
			t.Errorf("%s claims --model; pi reads a provider-qualified --model as a provider choice", r.String())
		}
	}
}
