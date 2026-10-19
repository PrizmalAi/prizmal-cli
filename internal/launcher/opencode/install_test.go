package opencode

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

// TestOpenCodeInstallerFindsItsOwnInstallDirectory is the guarantee for
// OpenCode's curl installer, which writes ~/.opencode/bin.
func TestOpenCodeInstallerFindsItsOwnInstallDirectory(t *testing.T) {
	internaltest.WithEmptyPATH(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(home, ".opencode", "bin", "opencode")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := findOpenCode("linux")
	if err != nil {
		t.Fatalf("findOpenCode with the binary in ~/.opencode/bin: %v", err)
	}
	if got != path {
		t.Errorf("findOpenCode = %q, want %q", got, path)
	}

	internaltest.SandboxedHome(t)
	if _, err := findOpenCode("linux"); !errors.Is(err, launch.ErrHarnessAbsent) {
		t.Errorf("findOpenCode with no opencode anywhere = %v, want ErrHarnessAbsent", err)
	}
}

// TestOpenCodeLocateReadsAnUnreadableHomeAsAbsent is the opencode half of the
// locate contract: a machine where the home directory cannot be named has no
// binary a PATH lookup found either, so "not installed" is the true answer and
// the launch must offer the install rather than print "$HOME is not defined".
func TestOpenCodeLocateReadsAnUnreadableHomeAsAbsent(t *testing.T) {
	internaltest.WithEmptyPATH(t)
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")

	if _, err := findOpenCode("linux"); !errors.Is(err, launch.ErrHarnessAbsent) {
		t.Fatalf("findOpenCode with no home = %v, want ErrHarnessAbsent", err)
	}
}
