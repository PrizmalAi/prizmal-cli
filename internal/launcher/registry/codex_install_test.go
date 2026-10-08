package registry

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCodexIntegrationIsAutoInstallable(t *testing.T) {
	spec := integrationSpecsByName["codex"]
	if spec == nil || spec.Install.EnsureInstalled == nil {
		t.Fatal("the codex integration has no EnsureInstalled")
	}
}

// An installed Codex that is too old stops a launch where a missing one does,
// before sign-in and the model pick. With nothing to upgrade it with, the error
// still says what is wrong.
func TestEnsureIntegrationInstalledRejectsAnOldInstalledCodex(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in Codex is a shell script, which Windows does not run as codex")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\necho \"codex-cli 0.134.0\"\n"
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	spec := integrationSpecsByName["codex"]
	err := EnsureIntegrationInstalled("codex", spec.Runner)
	if err == nil || !strings.Contains(err.Error(), "too old") {
		t.Fatalf("EnsureIntegrationInstalled = %v, want a too-old error", err)
	}
}
