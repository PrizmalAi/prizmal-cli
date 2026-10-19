package installers

import (
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/claude"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/cline"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/codex"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/opencode"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/pi"
)

// Each accessor reads a harness package's installer, so a platform table in
// the launcher's external tests covers every harness in one place while each
// installer stays unexported state of the package that owns it.
var (
	ClaudeInstaller   = claude.ClaudeInstaller
	ClineInstaller    = cline.ClineInstaller
	CodexInstaller    = codex.CodexInstaller
	OpenCodeInstaller = opencode.OpenCodeInstaller
	PiInstaller       = pi.PiInstaller
)
