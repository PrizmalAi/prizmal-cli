package launch

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// errHarnessAbsent is the one Locate or Relocate error the pipeline below reads
// as "this harness is not installed here", and the only one it recovers from.
// Every other error is a fault in discovery itself — pi's guard that stops
// before it runs npm on a machine with no npm is one — and is handed to the
// operator as it is, because continuing past a discovery failure ends in the
// installer's own error ("executable file not found in $PATH") instead of the
// message naming what to install first.
var errHarnessAbsent = errors.New("harness is not installed")

// Dependency is one thing the machine must already have for a harness's
// installer to run.
//
// The probe name and the label differ on purpose: the probe is what exec
// looks up on PATH ("powershell") and the label is what the message prints
// ("PowerShell"), and an installer that installs Node.js asks for "npm" and
// prints "npm (Node.js)" because the operator searching for the fix needs the
// name they will find in a download page.
type Dependency struct {
	Probe string
	Label string
	URL   string
}

// curlDependency and bashDependency are the two tools the pipe-to-shell
// installers need on macOS and Linux: each reads the script over curl and runs
// it with bash, so a machine missing either fails the installer with a bare
// exit status that names neither.
var (
	curlDependency = Dependency{Probe: "curl", Label: "curl", URL: "https://curl.se/"}
	bashDependency = Dependency{Probe: "bash", Label: "bash", URL: "https://www.gnu.org/software/bash/"}
)

// npmDependency is Node.js, which every npm-based installer needs.
var npmDependency = Dependency{Probe: "npm", Label: "npm (Node.js)", URL: "https://nodejs.org/"}

// Installer is one harness's install story, and the seam every harness's
// install path runs through: locate the binary, probe the dependencies the
// installer needs, confirm, run the installer, locate again, print the result.
//
// A harness states what is genuinely its own — where its installer leaves the
// binary when PATH does not carry it, what the machine must already have, the
// per-OS installer command — and gets the six steps, the four messages and the
// --yes gate from one implementation.
//
// The pipeline is safety-relevant, which is why it is one implementation and
// not four. The dependency probe is what turns a machine without curl, bash or
// npm into "install this first" instead of a confusing failure from a shell
// the machine does not have, and ConfirmPrompt is the gate --yes auto-approves
// (compat.go). Every field below is nil-able and each nil means the plainest
// behaviour, so a harness with nothing to say about an axis does not say it.
type Installer struct {
	// Name is the harness's own name as a command word — "claude", "pi". It is
	// what the messages name, and what the "Then re-run: prizmal <Name>" hint
	// tells the operator to type, so it must be the word the CLI accepts.
	Name string

	// DisplayName is how the harness names itself to the operator ("Claude
	// Code"), for the prose in the messages rather than the command word.
	DisplayName string

	// Prompt overrides the confirmation question. The default is
	// "<DisplayName> is not installed. Install now?". An installer that is one
	// package manager says so, because an operator deciding whether to let npm
	// write to their machine is owed that much before answering.
	Prompt string

	// GOOS is the operating-system seam. It exists because a harness's
	// fallback-directory names, dependency list and installer command all
	// branch on the OS, and reading runtime.GOOS at the point of use leaves
	// none of those branches reachable from a test running on another one. A
	// nil GOOS is runtime.GOOS.
	GOOS func() string

	// Locate returns the binary to run when the harness is already installed,
	// and errHarnessAbsent when it is not. It runs before the dependency probe
	// and before any confirmation, so it must not install anything a launch
	// has not agreed to.
	//
	// It may still repair what it finds. pi uses it to migrate a legacy npm
	// package and to upgrade a release too old to read the credential
	// reference pi's config names; both happen on machines that already have
	// pi, both run before the confirm today, and neither is an install.
	Locate func(goos string) (string, error)

	// Relocate is the second locate, after an installer has run. A nil
	// Relocate is Locate, which is right for every harness whose installer
	// leaves the binary where Locate already looks.
	//
	// pi overrides it because its Locate does more than look, and repeating
	// that work here would repeat the install that just failed: pi's Locate
	// reinstalls a package that npm reports as installed when its binary is
	// not on PATH, so running it a second time after the installer would
	// install twice over in front of an operator who is already looking at a
	// failure.
	Relocate func(goos string) (string, error)

	// Dependencies lists what the installer needs on this OS. An empty list
	// means nothing is probed: a harness whose Locate already refuses to run
	// npm on a machine without it (pi) must not be asked twice, and the
	// second answer would arrive after the confirm rather than before it.
	Dependencies func(goos string) []Dependency

	// Command returns the installer to run on this OS. It is called only after
	// the operator has agreed, so it may probe the machine (pi reads npm's
	// global prefix to install into the prefix it found).
	Command func(goos string) (bin string, args []string, err error)

	// Run runs the resolved installer command. A nil Run streams the installer's
	// own output to the operator's terminal, which is what every pipe-to-shell
	// and npm installer wants to do. pi overrides it to capture the output and
	// fold it into the returned error instead, which is what its errors have
	// always said.
	Run func(bin string, args []string) error
}

// goos is the OS this install runs on.
func (i Installer) goos() string {
	if i.GOOS != nil {
		return i.GOOS()
	}
	return runtime.GOOS
}

// prompt is the confirmation question, defaulted.
func (i Installer) prompt() string {
	if i.Prompt != "" {
		return i.Prompt
	}
	return i.DisplayName + " is not installed. Install now?"
}

// relocate is the second locate, defaulted to Locate.
func (i Installer) relocate(goos string) (string, error) {
	if i.Relocate != nil {
		return i.Relocate(goos)
	}
	return i.Locate(goos)
}

// run executes the installer command, streaming it when Run is nil.
func (i Installer) run(bin string, args []string) error {
	if i.Run != nil {
		return i.Run(bin, args)
	}
	cmd := exec.Command(bin, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Installed reports whether the harness is already installed. It is the locate
// on its own, and it is only correct for a Locate that has no side effects.
func (i Installer) Installed() bool {
	_, err := i.Locate(i.goos())
	return err == nil
}

// EnsureInstalled returns the path to the harness's binary, installing it if
// it is not there and the operator agrees.
//
// The order of the steps is load-bearing. The locate comes first because an
// installed harness must launch even on a machine that has neither curl nor
// bash — those tools are the installer's, not the harness's. The dependency
// probe comes before the confirm because an operator who cannot install it
// anyway should be told what is missing, not asked a question whose answer
// changes nothing. The confirm comes before Command because pi's Command reads
// the machine (npm's global prefix) and nothing should read the machine on a
// launch the operator declined.
//
// Every message here is spelled from Name and DisplayName, so the wording is
// one text for all harnesses and a fix to it reaches all of them.
func (i Installer) EnsureInstalled() (string, error) {
	goos := i.goos()

	path, err := i.Locate(goos)
	switch {
	case err == nil:
		return path, nil
	case !errors.Is(err, errHarnessAbsent):
		return "", err
	}

	if missing := missingOnPath(i.dependencies(goos)); len(missing) > 0 {
		return "", missingDependencyError(i.Name, missing)
	}

	ok, err := ConfirmPrompt(i.prompt())
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("%s installation cancelled", i.Name)
	}

	bin, args, err := i.Command(goos)
	if err != nil {
		return "", err
	}

	fmt.Fprintf(os.Stderr, "\nInstalling %s...\n", i.DisplayName)
	if err := i.run(bin, args); err != nil {
		return "", fmt.Errorf("failed to install %s: %w", i.Name, err)
	}

	path, err = i.relocate(goos)
	if err != nil {
		return "", fmt.Errorf("%s was installed but the binary was not found on PATH\n\nYou may need to restart your shell", i.Name)
	}

	fmt.Fprintf(os.Stderr, "%s%s installed successfully%s\n\n", ansiGreen, i.DisplayName, ansiReset)
	return path, nil
}

// dependencies is the harness's dependency list for this OS, defaulted to none.
func (i Installer) dependencies(goos string) []Dependency {
	if i.Dependencies == nil {
		return nil
	}
	return i.Dependencies(goos)
}

// missingOnPath returns the dependencies the machine does not have, in the
// order they were listed. The order is the message's: it reads as a list of
// things to do, and the two pipe-to-shell installers name curl before bash
// because that is the order the script needs them in.
func missingOnPath(dependencies []Dependency) []Dependency {
	var missing []Dependency
	for _, dependency := range dependencies {
		if _, err := exec.LookPath(dependency.Probe); err != nil {
			missing = append(missing, dependency)
		}
	}
	return missing
}

// missingDependencyError renders the one missing-dependency message every
// harness shares. It names the harness by command word, because the last line
// is a command the operator is about to type, and lists each missing tool with
// the page that installs it rather than only its name: the operator who reaches
// this message is holding a machine that cannot run the installer and has
// never installed the tool by hand.
func missingDependencyError(name string, missing []Dependency) error {
	lines := make([]string, 0, len(missing))
	for _, dependency := range missing {
		lines = append(lines, dependency.Label+": "+dependency.URL)
	}
	return fmt.Errorf("%s is not installed and required dependencies are missing\n\nInstall the following first:\n  %s\n\nThen re-run:\n  prizmal %s", name, strings.Join(lines, "\n  "), name)
}
