package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// slowestWindowsLaunch is the longest harness launch step measured on a
// windows-latest runner (the opencode session step, 157s).
const slowestWindowsLaunch = 157 * time.Second

func TestHarnessLaunchBudgetLeavesHeadroomOnWindows(t *testing.T) {
	got := harnessLaunchBudgetFor("windows")
	if got < 2*slowestWindowsLaunch {
		t.Errorf("windows launch budget = %s, want at least twice the slowest measured launch (%s)", got, 2*slowestWindowsLaunch)
	}
	// The job's timeout-minutes: 15 stays the outer bound, and go test's own
	// 10m default must not cut a single launch short.
	if got >= 10*time.Minute {
		t.Errorf("windows launch budget = %s, want under go test's 10m default", got)
	}
}

func TestHarnessLaunchBudgetKeepsThreeMinutesElsewhere(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		if got := harnessLaunchBudgetFor(goos); got != 3*time.Minute {
			t.Errorf("%s launch budget = %s, want 3m", goos, got)
		}
	}
}

func TestLaunchDeadlineErrorNamesTheDeadline(t *testing.T) {
	cause := errors.New("exit status 1")
	err := launchDeadlineError(cause, true, 7*time.Minute)
	if !strings.Contains(err.Error(), "7m0s") || !strings.Contains(err.Error(), "launch deadline") {
		t.Errorf("error %q does not name the deadline that expired", err)
	}
	if !errors.Is(err, cause) {
		t.Errorf("error %q does not wrap the run error", err)
	}
	if got := launchDeadlineError(cause, false, time.Minute); got != cause {
		t.Errorf("without a timeout the run error must pass through, got %v", got)
	}
	if got := launchDeadlineError(nil, true, time.Minute); got != nil {
		t.Errorf("a nil run error must stay nil, got %v", got)
	}
}
