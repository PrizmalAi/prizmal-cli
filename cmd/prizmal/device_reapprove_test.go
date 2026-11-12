package main

import (
	"strings"
	"testing"
	"time"

	"github.com/PrizmalAi/prizmal-cli/internal/device"
)

func TestPlanApprovedLogin(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *device.Token {
		deadline := now.Add(d)
		return &device.Token{ReauthBy: &deadline}
	}
	cases := []struct {
		name      string
		token     *device.Token
		reapprove bool
		want      approvedLoginAction
	}{
		{"plenty of time, plain login", at(3 * 24 * time.Hour), false, actionReportApproved},
		{"just over a day, plain login", at(24*time.Hour + time.Minute), false, actionReportApproved},
		{"under a day, plain login", at(23 * time.Hour), false, actionReapprove},
		{"past the deadline, plain login", at(-time.Hour), false, actionReapprove},
		{"plenty of time, --reapprove", at(3 * 24 * time.Hour), true, actionReapprove},
		{"no deadline, plain login", &device.Token{}, false, actionReportApproved},
		{"no deadline, --reapprove", &device.Token{}, true, actionNoDeadline},
	}
	for _, tc := range cases {
		if got := planApprovedLogin(tc.token, tc.reapprove, now); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestReportAlreadyApprovedPointsToReapprove(t *testing.T) {
	deadline := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	var out strings.Builder
	reportAlreadyApproved(&out, &device.Token{ReauthBy: &deadline})
	if !strings.Contains(out.String(), "already approved until 2026-10-02T00:00:00Z.") ||
		!strings.Contains(out.String(), "Run prizmal login --reapprove to extend it now.") {
		t.Fatalf("output = %q", out.String())
	}
	out.Reset()
	reportAlreadyApproved(&out, &device.Token{})
	if strings.Contains(out.String(), "--reapprove") {
		t.Fatalf("a key with no deadline was told to re-approve: %q", out.String())
	}
}
