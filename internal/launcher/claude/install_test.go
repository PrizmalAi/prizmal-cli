package claude

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

// TestClaudeInstallerFindsItsOwnInstallDirectories covers the directories
// Claude Code's installer writes to, which PATH does not carry on a machine
// that ran it and never added them. Without these a machine with Claude Code
// installed is told to install it again, in front of an operator who can see
// the binary.
func TestClaudeInstallerFindsItsOwnInstallDirectories(t *testing.T) {
	for _, tc := range []struct {
		name    string
		goos    string
		relPath []string
	}{
		{"~/.local/bin", "linux", []string{".local", "bin", "claude"}},
		{"~/.claude/local", "darwin", []string{".claude", "local", "claude"}},
		{"the Windows name", "windows", []string{".local", "bin", "claude.exe"}},
	} {
		internaltest.WithEmptyPATH(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		path := filepath.Join(append([]string{home}, tc.relPath...)...)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o755); err != nil {
			t.Fatal(err)
		}

		got, err := (&Claude{}).findPath(tc.goos)
		if err != nil {
			t.Fatalf("findPath(%s) with the binary in %s: %v", tc.goos, tc.name, err)
		}
		if got != path {
			t.Errorf("findPath(%s) = %q, want %q", tc.goos, got, path)
		}
	}

	internaltest.WithEmptyPATH(t)
	if _, err := (&Claude{}).findPath("linux"); !errors.Is(err, launch.ErrHarnessAbsent) {
		t.Errorf("findPath on a machine with no claude = %v, want ErrHarnessAbsent", err)
	}
}

// TestClaudeLocateReadsAnUnreadableHomeAsAbsent pins the one place a locate
// may not pass its own lookup error up. Claude Code falls back to a directory
// under the home directory, and a machine where the home directory cannot be
// named has already failed the PATH lookup, so there is no binary here either
// way and "not installed" is the true answer. Handing the error up instead
// refuses a launch the operator can finish by installing, and prints "$HOME is
// not defined" about a variable they never set.
func TestClaudeLocateReadsAnUnreadableHomeAsAbsent(t *testing.T) {
	internaltest.WithEmptyPATH(t)
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")

	if _, err := (&Claude{}).findPath("linux"); !errors.Is(err, launch.ErrHarnessAbsent) {
		t.Fatalf("findPath with no home = %v, want ErrHarnessAbsent", err)
	}

	// The operator-visible consequence: the launch reaches the confirm rather
	// than failing on the lookup. Claude Code's installer needs curl and bash,
	// so both are faked on PATH — the empty PATH above put them on the missing
	// list, and the dependency probe correctly answers before the confirm.
	// The OS is pinned to linux because Windows installs through PowerShell and
	// would ask for that instead. Claude's own installer command is replaced
	// too, so the test cannot reach the network; what is asserted is that the
	// prompt was asked and the installer ran at all.
	dir := internaltest.WithEmptyPATH(t)
	internaltest.WriteFakeBinary(t, dir, "curl")
	internaltest.WriteFakeBinary(t, dir, "bash")

	previousIn := launch.ConfirmInput
	previousOut := launch.ConfirmOutput
	var asked bytes.Buffer
	launch.ConfirmInput = bufio.NewReader(strings.NewReader("y\n"))
	launch.ConfirmOutput = &asked
	saved := ClaudeInstaller
	ran := false
	ClaudeInstaller.GOOS = func() string { return "linux" }
	ClaudeInstaller.Run = func(string, []string) error { ran = true; return nil }
	t.Cleanup(func() {
		launch.ConfirmInput = previousIn
		launch.ConfirmOutput = previousOut
		ClaudeInstaller = saved
		launch.SetConfirmPolicy(false)
	})

	if _, err := ClaudeInstaller.EnsureInstalled(); err == nil || !strings.Contains(err.Error(), "not found on PATH") {
		// The re-locate still finds nothing, because this test's Run is a stub
		// that installs nothing. What must not happen is the launch dying on
		// the home directory instead of getting that far.
		t.Fatalf("EnsureInstalled with no home directory = %v, want the re-locate failure", err)
	}
	if got := strings.Count(asked.String(), "(Y/n)"); got != 1 {
		t.Errorf("prompted %d times (%q), want one confirm before installing", got, asked.String())
	}
	if !ran {
		t.Error("did not run the installer: the launch failed before the confirm")
	}
}
