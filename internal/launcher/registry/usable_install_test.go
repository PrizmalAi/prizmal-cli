package registry

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

// An installed OpenCode or Cline older than its pin stops a launch the way an
// old Codex does, before sign-in and the model pick.
func TestEnsureIntegrationInstalledRejectsAnOldInstalledOpenCodeOrCline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in harness is a shell script, which Windows does not run")
	}
	for name, old := range map[string]string{"opencode": "1.18.34", "cline": "3.0.62"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			script := "#!/bin/sh\necho \"" + old + "\"\n"
			if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)
			t.Setenv("HOME", t.TempDir())
			// Decline the upgrade offer, so the stand-in is never asked to
			// upgrade itself.
			previousIn, previousOut := launch.ConfirmInput, launch.ConfirmOutput
			launch.ConfirmInput = bufio.NewReader(strings.NewReader("n\n"))
			launch.ConfirmOutput = io.Discard
			t.Cleanup(func() { launch.ConfirmInput, launch.ConfirmOutput = previousIn, previousOut })

			spec := integrationSpecsByName[name]
			if spec.Install.CheckUsable == nil {
				t.Fatalf("%s has no CheckUsable", name)
			}
			err := EnsureIntegrationInstalled(name, spec.Runner)
			if err == nil || !strings.Contains(err.Error(), "too old") {
				t.Fatalf("EnsureIntegrationInstalled(%s %s) = %v, want a too-old error", name, old, err)
			}
		})
	}
}
