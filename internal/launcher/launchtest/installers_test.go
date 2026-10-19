package launchtest

import (
	"strings"
	"testing"

	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/installers"
)

func TestInstallerDependenciesPerPlatform(t *testing.T) {
	for _, tc := range []struct {
		harness  string
		depends  func(string) []launch.Dependency
		windows  []string
		anywhere []string
	}{
		{"claude", installers.ClaudeInstaller.RequiredDependencies, []string{"PowerShell"}, []string{"curl", "bash"}},
		{"opencode", installers.OpenCodeInstaller.RequiredDependencies, []string{"npm (Node.js)"}, []string{"curl", "bash"}},
		{"cline", installers.ClineInstaller.RequiredDependencies, []string{"npm (Node.js)"}, []string{"npm (Node.js)"}},
	} {
		for _, platform := range []struct{ goos, want string }{
			{"windows", strings.Join(tc.windows, ",")},
			{"darwin", strings.Join(tc.anywhere, ",")},
			{"linux", strings.Join(tc.anywhere, ",")},
		} {
			var labels []string
			for _, dependency := range tc.depends(platform.goos) {
				if dependency.Probe == "" || dependency.Label == "" || dependency.URL == "" {
					t.Errorf("%s on %s: dependency %+v must name a probe, a label and a URL", tc.harness, platform.goos, dependency)
				}
				labels = append(labels, dependency.Label)
			}
			if got := strings.Join(labels, ","); got != platform.want {
				t.Errorf("%s on %s depends on %v, want %v", tc.harness, platform.goos, labels, platform.want)
			}
		}
	}
}

// TestInstallerCommandPerPlatform pins the command each installer runs, and
// the refusal for an OS with no installer. The refusal matters: a harness with
// no script for the platform must say so rather than run a command that cannot
// work.
func TestInstallerCommandPerPlatform(t *testing.T) {
	for _, tc := range []struct {
		harness string
		command func(string) (string, []string, error)
		darwin  string
		windows string
	}{
		{"claude", installers.ClaudeInstaller.Command, "bash", "powershell"},
		{"opencode", installers.OpenCodeInstaller.Command, "bash", "npm"},
		{"cline", installers.ClineInstaller.Command, "npm", "npm"},
		{"pi", installers.PiInstaller.Command, "npm", "npm"},
	} {
		for goos, want := range map[string]string{"darwin": tc.darwin, "linux": tc.darwin, "windows": tc.windows} {
			bin, args, err := tc.command(goos)
			if err != nil {
				t.Fatalf("%s on %s: %v", tc.harness, goos, err)
			}
			if bin != want {
				t.Errorf("%s on %s runs %q, want %q", tc.harness, goos, bin, want)
			}
			if len(args) == 0 {
				t.Errorf("%s on %s runs %q with no arguments", tc.harness, goos, bin)
			}
		}
	}

	// Only the two installers with a per-OS branch have a platform to refuse,
	// and they must say so rather than run a command that cannot work. The npm
	// installers have no branch and accept every platform, as they always have.
	for _, tc := range []struct {
		harness string
		command func(string) (string, []string, error)
	}{
		{"claude", installers.ClaudeInstaller.Command},
		{"opencode", installers.OpenCodeInstaller.Command},
	} {
		if _, _, err := tc.command("plan9"); err == nil {
			t.Errorf("%s accepted an OS it has no installer for", tc.harness)
		}
	}

	// The npm installs name the package they install, which is what an operator
	// reading the command line needs to recognize and what a copy-paste needs
	// to be right.
	for _, tc := range []struct {
		harness string
		command func(string) (string, []string, error)
		pkg     string
	}{
		{"cline", installers.ClineInstaller.Command, "cline@latest"},
		{"codex", installers.CodexInstaller.Command, "@openai/codex"},
		{"pi", installers.PiInstaller.Command, "@earendil-works/pi-coding-agent@latest"},
	} {
		_, args, err := tc.command("linux")
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(args, " "); got != "install -g "+tc.pkg {
			t.Errorf("%s installs %q, want \"install -g %s\"", tc.harness, got, tc.pkg)
		}
	}
}

// TestInstallerPrompts pins the confirmation each harness asks. The default
// names the harness; an npm installer says npm, because that is what the
// operator is being asked to let write to their machine.
func TestInstallerPrompts(t *testing.T) {
	for _, tc := range []struct {
		name, prompt string
		installer    launch.Installer
	}{
		{"claude", "Claude Code is not installed. Install now?", installers.ClaudeInstaller},
		{"opencode", "OpenCode is not installed. Install now?", installers.OpenCodeInstaller},
		{"cline", "Cline is not installed. Install with npm?", installers.ClineInstaller},
		{"codex", "Codex is not installed. Install with npm?", installers.CodexInstaller},
		{"pi", "Install Pi with npm?", installers.PiInstaller},
	} {
		if got := tc.installer.ConfirmPrompt(); got != tc.prompt {
			t.Errorf("%s asks %q, want %q", tc.name, got, tc.prompt)
		}
	}
}
