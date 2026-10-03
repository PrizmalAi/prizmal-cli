package launch

import (
	"errors"
	"os"
	"path/filepath"
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
func answerPrompts(t *testing.T, replies ...bool) *[]string {
	t.Helper()
	asked := &[]string{}
	previous := DefaultConfirmPrompt
	t.Cleanup(func() {
		DefaultConfirmPrompt = previous
		SetConfirmPolicy(false)
	})
	DefaultConfirmPrompt = func(prompt string, _ ConfirmOptions) (bool, error) {
		*asked = append(*asked, prompt)
		if len(*asked) > len(replies) {
			t.Fatalf("unexpected confirmation prompt %d: %q", len(*asked), prompt)
		}
		return replies[len(*asked)-1], nil
	}
	return asked
}

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
				return "", errHarnessAbsent
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
		return []Dependency{curlDependency, bashDependency}
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

	missing := missingOnPath([]Dependency{curlDependency, bashDependency, npmDependency})
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
func TestInstallerDependenciesPerPlatform(t *testing.T) {
	for _, tc := range []struct {
		harness  string
		depends  func(string) []Dependency
		windows  []string
		anywhere []string
	}{
		{"claude", claudeInstallerDependencies, []string{"PowerShell"}, []string{"curl", "bash"}},
		{"opencode", openCodeInstallerDependencies, []string{"npm (Node.js)"}, []string{"curl", "bash"}},
		{"cline", clineInstallerDependencies, []string{"npm (Node.js)"}, []string{"npm (Node.js)"}},
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
		{"claude", claudeInstallerCommand, "bash", "powershell"},
		{"opencode", openCodeInstallerCommand, "bash", "npm"},
		{"cline", clineInstallerCommand, "npm", "npm"},
		{"pi", piInstallerCommand, "npm", "npm"},
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
		{"claude", claudeInstallerCommand},
		{"opencode", openCodeInstallerCommand},
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
		{"cline", clineInstallerCommand, "cline@latest"},
		{"pi", piInstallerCommand, piNpmPackage + "@latest"},
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
		installer    Installer
	}{
		{"claude", "Claude Code is not installed. Install now?", claudeInstaller},
		{"opencode", "OpenCode is not installed. Install now?", openCodeInstaller},
		{"cline", "Cline is not installed. Install with npm?", clineInstaller},
		{"pi", "Install Pi with npm?", piInstaller},
	} {
		if got := tc.installer.prompt(); got != tc.prompt {
			t.Errorf("%s asks %q, want %q", tc.name, got, tc.prompt)
		}
	}
}

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
		withEmptyPATH(t)
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

	withEmptyPATH(t)
	if _, err := (&Claude{}).findPath("linux"); !errors.Is(err, errHarnessAbsent) {
		t.Errorf("findPath on a machine with no claude = %v, want errHarnessAbsent", err)
	}
}

// TestOpenCodeInstallerFindsItsOwnInstallDirectory is the same guarantee for
// OpenCode's curl installer, which writes ~/.opencode/bin.
func TestOpenCodeInstallerFindsItsOwnInstallDirectory(t *testing.T) {
	withEmptyPATH(t)
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

	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := findOpenCode("linux"); !errors.Is(err, errHarnessAbsent) {
		t.Errorf("findOpenCode with no opencode anywhere = %v, want errHarnessAbsent", err)
	}
}

// TestLocatesReadAnUnreadableHomeAsAbsent pins the one place a locate may not
// pass its own lookup error up. Claude Code and OpenCode both fall back to a
// directory under the home directory, and a machine where the home directory
// cannot be named has already failed the PATH lookup, so there is no binary
// here either way and "not installed" is the true answer. Handing the error up
// instead refuses a launch the operator can finish by installing, and prints
// "$HOME is not defined" about a variable they never set.
func TestLocatesReadAnUnreadableHomeAsAbsent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		locate func(string) (string, error)
	}{
		{"claude", (&Claude{}).findPath},
		{"opencode", findOpenCode},
	} {
		withEmptyPATH(t)
		t.Setenv("HOME", "")
		t.Setenv("USERPROFILE", "")

		if _, err := tc.locate("linux"); !errors.Is(err, errHarnessAbsent) {
			t.Errorf("%s locate with no home directory = %v, want errHarnessAbsent", tc.name, err)
		}
	}

	// The operator-visible consequence: the launch reaches the confirm rather
	// than failing on the lookup. Claude Code's installer needs curl and bash,
	// so both are faked on PATH — the empty PATH above put them on the missing
	// list, and the dependency probe correctly answers before the confirm.
	// Claude's own installer command is replaced too, so the test cannot reach
	// the network; what is asserted is that the prompt was asked and the
	// installer ran at all.
	dir := withEmptyPATH(t)
	writeFakeBinary(t, dir, "curl")
	writeFakeBinary(t, dir, "bash")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	asked := answerPrompts(t, true)
	saved := claudeInstaller
	t.Cleanup(func() { claudeInstaller = saved })
	ran := false
	claudeInstaller.Run = func(string, []string) error { ran = true; return nil }

	if _, err := claudeInstaller.EnsureInstalled(); err == nil || !strings.Contains(err.Error(), "not found on PATH") {
		// The re-locate still finds nothing, because this test's Run is a stub
		// that installs nothing. What must not happen is the launch dying on
		// the home directory instead of getting that far.
		t.Fatalf("EnsureInstalled with no home directory = %v, want the re-locate failure", err)
	}
	if len(*asked) != 1 {
		t.Errorf("prompted %v, want one confirm before installing", *asked)
	}
	if !ran {
		t.Error("did not run the installer: the launch failed before the confirm")
	}
}

// TestLocatePiReportsMissingNodeBeforeAnythingElse covers pi's one dependency
// check living inside its locate. Everything past it runs npm, and npm is not
// on the machine, so the operator must be told that before they are asked
// whether to install — and it must not read as "absent", which would go on to
// confirm an install and then report the exec failure instead.
func TestLocatePiReportsMissingNodeBeforeAnythingElse(t *testing.T) {
	withEmptyPATH(t)

	_, err := locatePi("linux")
	want := "pi is not installed and required dependencies are missing\n\n" +
		"Install the following first:\n  npm (Node.js): https://nodejs.org/\n\n" +
		"Then re-run:\n  prizmal pi"
	if err == nil || err.Error() != want {
		t.Fatalf("locatePi = %v,\nwant\n%q", err, want)
	}
	if errors.Is(err, errHarnessAbsent) {
		t.Error("a missing npm read as an absent harness, so the install would run without its tool")
	}
}

// TestRelocatePiOnlyLooks covers pi's second locate: it must not reinstall a
// package npm already reports, because the install that ran has just failed and
// the operator is reading that failure.
func TestRelocatePiOnlyLooks(t *testing.T) {
	dir := withEmptyPATH(t)
	t.Setenv("PATH", dir)

	if _, err := relocatePi("linux"); !errors.Is(err, errHarnessAbsent) {
		t.Errorf("relocatePi with no pi on PATH = %v, want errHarnessAbsent", err)
	}

	writeFakeBinary(t, dir, "pi")
	path, err := relocatePi("linux")
	if err != nil {
		t.Fatalf("relocatePi with pi on PATH: %v", err)
	}
	if path != "pi" {
		t.Errorf("relocatePi = %q, want the bare command name every pi launch execs", path)
	}
}

// TestPiInstallerKeepsItsOwnStory pins the three accommodations pi keeps, so a
// later edit to the shared pipeline cannot quietly take them away: its locate
// may upgrade what it finds, its re-locate does not, and its install output is
// captured rather than streamed.
func TestPiInstallerKeepsItsOwnStory(t *testing.T) {
	if piInstaller.Relocate == nil {
		t.Error("piInstaller.Relocate is nil, so a failing install would be retried by the upgrade in Locate")
	}
	if piInstaller.Run == nil {
		t.Error("piInstaller.Run is nil, so npm's output would stream and pi's install errors would lose npm's message")
	}
	if piInstaller.Dependencies != nil {
		t.Error("piInstaller.Dependencies is set, so npm would be probed twice: once in Locate and once behind the confirm")
	}
	if piInstaller.Locate == nil || piInstaller.Command == nil {
		t.Fatal("piInstaller must declare both a locate and an install command")
	}
}

// TestAdapterInstallersAreWiredToTheRegistry covers the registry declaring an
// install hook for every harness that has one, so a launch that goes through
// the CLI's ensure gate and a launch that goes through the runner take the same
// path rather than two that can drift.
func TestAdapterInstallersAreWiredToTheRegistry(t *testing.T) {
	for _, name := range []string{"claude", "cline", "opencode", "pi"} {
		spec, err := LookupIntegrationSpec(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if spec.Install.CheckInstalled == nil {
			t.Errorf("%s does not declare an install check", name)
		}
		if spec.Install.EnsureInstalled == nil {
			t.Errorf("%s does not declare an install", name)
		}
	}
}
