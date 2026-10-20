package cline

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
)

// withClineVersion makes the version check read out instead of running an
// installed Cline.
func withClineVersion(t *testing.T, out string) {
	t.Helper()
	previous := clineVersion
	clineVersion = func() (string, error) { return out, nil }
	t.Cleanup(func() { clineVersion = previous })
}

// The floor is the release the terminal baselines pin, so a bump of the pin
// moves the floor with it.
func TestClineVersionFloorMatchesThePinnedBaselineRelease(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "cmd", "prizmal", "testdata", "terminal", "cline", "cline-version"))
	if err != nil {
		t.Fatal(err)
	}
	if pinned := strings.TrimSpace(string(data)); pinned != clineMinVersion {
		t.Fatalf("clineMinVersion = %q, but the baselines pin Cline %q", clineMinVersion, pinned)
	}
}

func TestClineUsableComparesTheInstalledVersionWithTheFloor(t *testing.T) {
	for _, tc := range []struct {
		out    string
		tooOld bool
	}{
		{out: "3.0.62\n", tooOld: true},
		{out: "2.99.0\n", tooOld: true},
		{out: clineMinVersion + "\n"},
		{out: "3.0.100\n"},
		{out: "3.1.0\n"},
	} {
		withClineVersion(t, tc.out)
		err := checkClineVersion()
		var tooOld *clineTooOldError
		if got := errors.As(err, &tooOld); got != tc.tooOld {
			t.Errorf("checkClineVersion(%q) = %v, want too old: %v", tc.out, err, tc.tooOld)
		}
		if !tc.tooOld && err != nil {
			t.Errorf("checkClineVersion(%q) = %v, want nil", tc.out, err)
		}
	}
}

func TestClineUsableRejectsOutputWithoutAVersion(t *testing.T) {
	for _, out := range []string{"", "cline\n"} {
		withClineVersion(t, out)
		if err := checkClineVersion(); err == nil || !strings.Contains(err.Error(), "unexpected cline version output") {
			t.Errorf("checkClineVersion(%q) = %v, want an unexpected-output error", out, err)
		}
	}
}

// With no npm to upgrade it, an old release stops the launch with an error
// that names the floor and the upgrade command.
func TestClineCheckUsableStopsAnOldReleaseWithNothingToUpgradeIt(t *testing.T) {
	internaltest.WithEmptyPATH(t)
	withClineVersion(t, "3.0.62\n")

	err := CheckUsable()
	if err == nil || !strings.Contains(err.Error(), "too old") || !strings.Contains(err.Error(), clineMinVersion) || !strings.Contains(err.Error(), "npm install -g cline@latest") {
		t.Fatalf("CheckUsable = %v, want a too-old error naming %s and the upgrade command", err, clineMinVersion)
	}
}
