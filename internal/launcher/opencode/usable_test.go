package opencode

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
)

// withOpenCodeVersion makes the version check read out instead of running an
// installed OpenCode.
func withOpenCodeVersion(t *testing.T, out string) {
	t.Helper()
	previous := openCodeVersion
	openCodeVersion = func() (string, error) { return out, nil }
	t.Cleanup(func() { openCodeVersion = previous })
}

// The floor is the release the terminal baselines pin, so a bump of the pin
// moves the floor with it.
func TestOpenCodeVersionFloorMatchesThePinnedBaselineRelease(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "cmd", "prizmal", "testdata", "terminal", "opencode", "opencode-version"))
	if err != nil {
		t.Fatal(err)
	}
	if pinned := strings.TrimSpace(string(data)); pinned != openCodeMinVersion {
		t.Fatalf("openCodeMinVersion = %q, but the baselines pin OpenCode %q", openCodeMinVersion, pinned)
	}
}

func TestOpenCodeUsableComparesTheInstalledVersionWithTheFloor(t *testing.T) {
	for _, tc := range []struct {
		out    string
		tooOld bool
	}{
		{out: "1.18.34\n", tooOld: true},
		{out: "1.9.99\n", tooOld: true},
		{out: openCodeMinVersion + "\n"},
		{out: "v" + openCodeMinVersion + "\n"},
		{out: "1.19.10\n"},
		{out: "2.0.0\n"},
	} {
		withOpenCodeVersion(t, tc.out)
		err := checkOpenCodeVersion()
		var tooOld *openCodeTooOldError
		if got := errors.As(err, &tooOld); got != tc.tooOld {
			t.Errorf("checkOpenCodeVersion(%q) = %v, want too old: %v", tc.out, err, tc.tooOld)
		}
		if !tc.tooOld && err != nil {
			t.Errorf("checkOpenCodeVersion(%q) = %v, want nil", tc.out, err)
		}
	}
}

func TestOpenCodeUsableRejectsOutputWithoutAVersion(t *testing.T) {
	for _, out := range []string{"", "opencode\n"} {
		withOpenCodeVersion(t, out)
		if err := checkOpenCodeVersion(); err == nil || !strings.Contains(err.Error(), "unexpected opencode version output") {
			t.Errorf("checkOpenCodeVersion(%q) = %v, want an unexpected-output error", out, err)
		}
	}
}

// With no OpenCode binary to run its upgrade, an old release stops the launch
// with an error that names the floor and the upgrade command.
func TestOpenCodeCheckUsableStopsAnOldReleaseWithNothingToUpgradeIt(t *testing.T) {
	internaltest.WithEmptyPATH(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	withOpenCodeVersion(t, "1.18.34\n")

	err := CheckUsable()
	if err == nil || !strings.Contains(err.Error(), "too old") || !strings.Contains(err.Error(), openCodeMinVersion) || !strings.Contains(err.Error(), "opencode upgrade") {
		t.Fatalf("CheckUsable = %v, want a too-old error naming %s and the upgrade command", err, openCodeMinVersion)
	}
}
