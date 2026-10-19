package pi

import (
	"errors"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

// TestLocatePiReportsMissingNodeBeforeAnythingElse covers pi's one dependency
// check living inside its locate. Everything past it runs npm, and npm is not
// on the machine, so the operator must be told that before they are asked
// whether to install — and it must not read as "absent", which would go on to
// confirm an install and then report the exec failure instead.
func TestLocatePiReportsMissingNodeBeforeAnythingElse(t *testing.T) {
	internaltest.WithEmptyPATH(t)

	_, err := locatePi("linux")
	want := "pi is not installed and required dependencies are missing\n\n" +
		"Install the following first:\n  npm (Node.js): https://nodejs.org/\n\n" +
		"Then re-run:\n  prizmal pi"
	if err == nil || err.Error() != want {
		t.Fatalf("locatePi = %v,\nwant\n%q", err, want)
	}
	if errors.Is(err, launch.ErrHarnessAbsent) {
		t.Error("a missing npm read as an absent harness, so the install would run without its tool")
	}
}

// TestRelocatePiOnlyLooks covers pi's second locate: it must not reinstall a
// package npm already reports, because the install that ran has just failed and
// the operator is reading that failure.
func TestRelocatePiOnlyLooks(t *testing.T) {
	dir := internaltest.WithEmptyPATH(t)
	t.Setenv("PATH", dir)

	if _, err := relocatePi("linux"); !errors.Is(err, launch.ErrHarnessAbsent) {
		t.Errorf("relocatePi with no pi on PATH = %v, want ErrHarnessAbsent", err)
	}

	internaltest.WriteFakeBinary(t, dir, "pi")
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
	if PiInstaller.Relocate == nil {
		t.Error("PiInstaller.Relocate is nil, so a failing install would be retried by the upgrade in Locate")
	}
	if PiInstaller.Run == nil {
		t.Error("PiInstaller.Run is nil, so npm's output would stream and pi's install errors would lose npm's message")
	}
	if PiInstaller.Dependencies != nil {
		t.Error("PiInstaller.Dependencies is set, so npm would be probed twice: once in Locate and once behind the confirm")
	}
	if PiInstaller.Locate == nil || PiInstaller.Command == nil {
		t.Fatal("PiInstaller must declare both a locate and an install command")
	}
}
