package launch

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// withEmptyPATH puts an empty directory on PATH for the test, so every lookup
// misses: the state of a machine that has none of the tools an installer
// needs. It returns that directory, for a test that wants a binary in it.
func withEmptyPATH(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	return dir
}

// writeFakeBinary puts an executable named name in dir, with the extension the
// platform resolves it by: a bare name on Unix, .exe on Windows, where
// exec.LookPath only finds a name a PATHEXT entry spells.
func writeFakeBinary(t *testing.T, dir, name string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// answerPrompts makes the confirmation prompt answer with the given replies in
// order and record what it was asked, standing in for the operator. It fails
// the test if a prompt arrives that the test did not prepare for, which is how
// a pipeline that asks where it should not is caught.
//
// The confirm gate reads its answer from stdin rather than from a replaceable
// variable, so the replies arrive the way an operator's do: as lines on the
// reader the gate consults. The prompts are recorded as they are written, which
// is what lets a test read what it was asked while it is still running, and the
// count is checked against the replies when the test ends so an extra prompt is
// a failure rather than an EOF the pipeline happens to survive.
func answerPrompts(t *testing.T, replies ...bool) *[]string {
	t.Helper()

	var lines strings.Builder
	for _, reply := range replies {
		if reply {
			lines.WriteString("y\n")
		} else {
			lines.WriteString("n\n")
		}
	}
	previousReader := ConfirmInput
	ConfirmInput = bufio.NewReader(strings.NewReader(lines.String()))
	t.Cleanup(func() {
		ConfirmInput = previousReader
		// The auto-approve policy is process state: a test that turned it on
		// would otherwise suppress the prompt the next test expects, and the
		// answer would never be read.
		SetConfirmPolicy(false)
	})

	asked := &[]string{}
	recorder := &promptRecorder{asked: asked}
	previousOut := ConfirmOutput
	// The restore is registered before the global is replaced, so the two
	// cannot be separated by an early return later in this helper.
	t.Cleanup(func() {
		ConfirmOutput = previousOut
		if len(*asked) > len(replies) {
			// The last recorded prompt: in range whenever this guard is, since
			// len(*asked)-1 >= len(replies) exactly when the test above holds.
			t.Errorf("unexpected confirmation prompt %d: %q", len(*asked), (*asked)[len(*asked)-1])
		}
	})
	ConfirmOutput = recorder
	return asked
}

// promptRecorder collects the confirmation prompts a launch writes, so a test
// can assert what it was asked without reading a pipe after the fact. It holds
// a partial line between writes: the gate writes the question and its suffix in
// one Fprintf today, but nothing in the interface promises that.
type promptRecorder struct {
	asked   *[]string
	pending strings.Builder
}

func (p *promptRecorder) Write(b []byte) (int, error) {
	p.pending.Write(b)
	for {
		text := p.pending.String()
		loc := promptPattern.FindStringSubmatchIndex(text)
		if loc == nil {
			break
		}
		*p.asked = append(*p.asked, strings.TrimSpace(text[loc[2]:loc[3]]))
		rest := text[loc[1]:]
		p.pending.Reset()
		p.pending.WriteString(rest)
	}
	return len(b), nil
}

// promptPattern splits what the gate writes into prompts, each of which it
// writes as the question followed by its default-answer suffix.
var promptPattern = regexp.MustCompile(`(?s)(.*?)\s\([yY]/[nN]\)\s`)

// recordingInstaller is a fake harness: it is installed or it is not, and the
// installer it offers is recorded rather than run.
type recordingInstaller struct {
	installer Installer
	located   string
	findErr   error
	runBin    string
	runArgs   []string
	runs      int
	locates   int
}

func newRecordingInstaller(t *testing.T, name, displayName string) *recordingInstaller {
	t.Helper()
	fake := &recordingInstaller{}
	fake.installer = Installer{
		Name:        name,
		DisplayName: displayName,
		Locate: func(string) (string, error) {
			fake.locates++
			if fake.findErr != nil {
				return "", fake.findErr
			}
			if fake.located == "" {
				return "", ErrHarnessAbsent
			}
			return fake.located, nil
		},
		Command: func(goos string) (string, []string, error) {
			return "installer-for-" + goos, []string{"install", name}, nil
		},
		Run: func(bin string, args []string) error {
			fake.runs++
			fake.runBin, fake.runArgs = bin, args
			fake.located = filepath.Join(t.TempDir(), name)
			return nil
		},
	}
	return fake
}

// TestInstallerReturnsALocatedBinaryWithoutAsking covers the launch that has
// nothing to install: the locate is the whole story, and a machine that cannot
// run the installer still launches the harness it already has.
func TestInstallerReturnsALocatedBinaryWithoutAsking(t *testing.T) {
	asked := answerPrompts(t)
	fake := newRecordingInstaller(t, "claude", "Claude Code")
	fake.located = "/usr/local/bin/claude"

	path, err := fake.installer.EnsureInstalled()
	if err != nil {
		t.Fatalf("EnsureInstalled: %v", err)
	}
	if path != fake.located {
		t.Errorf("path = %q, want %q", path, fake.located)
	}
	if len(*asked) != 0 {
		t.Errorf("prompted %v on an installed harness", *asked)
	}
	if fake.runs != 0 {
		t.Errorf("ran the installer %d times on an installed harness", fake.runs)
	}
}

// TestInstallerCancelledRunsNothing covers the declined prompt: nothing is
// probed on the machine, nothing is run, and the operator is told the install
// was cancelled rather than being left to wonder whether it is still running.
func TestInstallerCancelledRunsNothing(t *testing.T) {
	asked := answerPrompts(t, false)
	fake := newRecordingInstaller(t, "opencode", "OpenCode")

	_, err := fake.installer.EnsureInstalled()
	if err == nil || err.Error() != "opencode installation cancelled" {
		t.Fatalf("err = %v, want \"opencode installation cancelled\"", err)
	}
	if want := []string{"OpenCode is not installed. Install now?"}; strings.Join(*asked, "|") != strings.Join(want, "|") {
		t.Errorf("asked %v, want %v", *asked, want)
	}
	if fake.runs != 0 {
		t.Errorf("ran the installer %d times after the prompt was declined", fake.runs)
	}
}

// TestInstallerRunsTheResolvedCommand covers the accepted prompt: the command
// the adapter resolved for this OS is the one that runs, and the harness is
// located again afterwards because the installer put it there.
func TestInstallerRunsTheResolvedCommand(t *testing.T) {
	answerPrompts(t, true)
	fake := newRecordingInstaller(t, "claude", "Claude Code")
	fake.installer.GOOS = func() string { return "darwin" }

	path, err := fake.installer.EnsureInstalled()
	if err != nil {
		t.Fatalf("EnsureInstalled: %v", err)
	}
	if fake.runBin != "installer-for-darwin" {
		t.Errorf("ran %q, want the command the adapter resolved", fake.runBin)
	}
	if got := strings.Join(fake.runArgs, " "); got != "install claude" {
		t.Errorf("args = %q, want \"install claude\"", got)
	}
	if path != fake.located {
		t.Errorf("path = %q, want the located binary %q", path, fake.located)
	}
	if fake.locates != 2 {
		t.Errorf("located %d times, want twice: before the install and after it", fake.locates)
	}
}

// TestInstallerAutoApprovesWithYesFlag is the --yes guarantee: the CLI's
// confirm policy must still reach the install pipeline, or an unattended
// `prizmal --yes <harness>` on a machine without the harness would stop at a
// prompt nobody is there to answer.
func TestInstallerAutoApprovesWithYesFlag(t *testing.T) {
	answerPrompts(t) // no replies: any prompt at all fails the test
	SetConfirmPolicy(true)
	fake := newRecordingInstaller(t, "cline", "Cline")

	if _, err := fake.installer.EnsureInstalled(); err != nil {
		t.Fatalf("EnsureInstalled: %v", err)
	}
	if fake.runs != 1 {
		t.Errorf("ran the installer %d times, want once", fake.runs)
	}
}

// TestInstallerNamesTheMissingDependencies covers the probe's whole reason for
// existing: a machine with neither curl nor bash must be told which two to
// install and where to get them, and must not be asked to confirm an install
// that cannot run.
func TestInstallerNamesTheMissingDependencies(t *testing.T) {
	withEmptyPATH(t)
	asked := answerPrompts(t, true)
	fake := newRecordingInstaller(t, "claude", "Claude Code")
	fake.installer.Dependencies = func(string) []Dependency {
		return []Dependency{CurlDependency, BashDependency}
	}

	_, err := fake.installer.EnsureInstalled()
	if err == nil {
		t.Fatal("EnsureInstalled succeeded on a machine with no curl and no bash")
	}
	want := "claude is not installed and required dependencies are missing\n\n" +
		"Install the following first:\n  curl: https://curl.se/\n  bash: https://www.gnu.org/software/bash/\n\n" +
		"Then re-run:\n  prizmal claude"
	if err.Error() != want {
		t.Errorf("err =\n%q\nwant\n%q", err.Error(), want)
	}
	if len(*asked) != 0 {
		t.Errorf("prompted %v before reporting missing dependencies", *asked)
	}
	if fake.runs != 0 {
		t.Errorf("ran the installer %d times with dependencies missing", fake.runs)
	}
}

// TestInstallerWrapsInstallerFailure keeps npm's or curl's own failure inside
// the sentence that names the harness it was installing, because the operator
// reads the first line and greps the rest.
func TestInstallerWrapsInstallerFailure(t *testing.T) {
	answerPrompts(t, true)
	fake := newRecordingInstaller(t, "opencode", "OpenCode")
	fake.installer.Run = func(string, []string) error { return errors.New("exit status 22") }

	_, err := fake.installer.EnsureInstalled()
	if err == nil || err.Error() != "failed to install opencode: exit status 22" {
		t.Fatalf("err = %v, want \"failed to install opencode: exit status 22\"", err)
	}
}

// TestInstallerReportsABinaryThatNeverAppeared covers the install that ran and
// changed nothing: the pipeline says so, because "restart your shell" is the
// advice that fixes it and a bare "not found" is not.
func TestInstallerReportsABinaryThatNeverAppeared(t *testing.T) {
	answerPrompts(t, true)
	fake := newRecordingInstaller(t, "cline", "Cline")
	fake.installer.Run = func(string, []string) error { return nil }

	_, err := fake.installer.EnsureInstalled()
	want := "cline was installed but the binary was not found on PATH\n\nYou may need to restart your shell"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

// TestInstallerSurfacesADiscoveryFailure keeps a locate that failed for its own
// reason from reading as "not installed". pi's locate stops before it runs npm
// on a machine without npm; treating that as absence would go on to confirm an
// install and then report the exec failure, and the operator would never see
// the message naming what to install.
func TestInstallerSurfacesADiscoveryFailure(t *testing.T) {
	asked := answerPrompts(t, true)
	fake := newRecordingInstaller(t, "pi", "Pi")
	fake.findErr = errors.New("home directory is unreadable")

	_, err := fake.installer.EnsureInstalled()
	if err == nil || err.Error() != "home directory is unreadable" {
		t.Fatalf("err = %v, want the discovery failure itself", err)
	}
	if len(*asked) != 0 {
		t.Errorf("prompted %v after a discovery failure", *asked)
	}
}

// TestInstallerRelocatesAfterInstalling covers the second locate being the
// adapter's own: pi's is deliberately not its first one, because repeating
// pi's upgrade after the install that just failed would install twice more in
// front of an operator already reading a failure.
func TestInstallerRelocatesAfterInstalling(t *testing.T) {
	answerPrompts(t, true)
	fake := newRecordingInstaller(t, "pi", "Pi")
	relocates := 0
	fake.installer.Relocate = func(string) (string, error) {
		relocates++
		return "/usr/local/bin/pi", nil
	}

	path, err := fake.installer.EnsureInstalled()
	if err != nil {
		t.Fatalf("EnsureInstalled: %v", err)
	}
	if relocates != 1 {
		t.Errorf("relocated %d times, want once, after the install", relocates)
	}
	if path != "/usr/local/bin/pi" {
		t.Errorf("path = %q, want the relocated binary", path)
	}
}

// TestMissingOnPathKeepsItsOrder pins the message's list order: curl is named
// before bash because that is the order the pipe-to-shell installers need them
// in, so an operator installing one at a time gets there in one pass.
func TestMissingOnPathKeepsItsOrder(t *testing.T) {
	withEmptyPATH(t)

	missing := MissingOnPath([]Dependency{CurlDependency, BashDependency, NpmDependency})
	want := []string{"curl", "bash", "npm (Node.js)"}
	if len(missing) != len(want) {
		t.Fatalf("missing %d dependencies, want %d", len(missing), len(want))
	}
	for i, dependency := range missing {
		if dependency.Label != want[i] {
			t.Errorf("missing[%d] = %q, want %q", i, dependency.Label, want[i])
		}
	}
}

// TestInstallerDependenciesPerPlatform pins each harness's dependency list for
// both installer shapes. Windows installs through npm or PowerShell and asks
// for one tool; the pipe-to-shell platforms ask for curl and bash.
// TestInstallerPrompts pins the confirmation each harness asks. The default
// names the harness; an npm installer says npm, because that is what the
// operator is being asked to let write to their machine.
// TestLocatesReadAnUnreadableHomeAsAbsent pins the one place a locate may not
// pass its own lookup error up. Claude Code and OpenCode both fall back to a
// directory under the home directory, and a machine where the home directory
// cannot be named has already failed the PATH lookup, so there is no binary
// here either way and "not installed" is the true answer. Handing the error up
// instead refuses a launch the operator can finish by installing, and prints
// "$HOME is not defined" about a variable they never set.
