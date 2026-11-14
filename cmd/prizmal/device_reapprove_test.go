package main

import (
	"strings"
	"testing"
	"time"

	"github.com/PrizmalAi/prizmal-cli/internal/device"
)

// A plain login never starts an approval by itself, whatever time is left,
// because that would open a browser flow nobody asked for. Only --reapprove does.
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
		{"an hour left, plain login", at(time.Hour), false, actionReportApproved},
		{"past the deadline, plain login", at(-time.Hour), false, actionReportApproved},
		{"no deadline, plain login", &device.Token{}, false, actionReportApproved},
		{"plenty of time, --reapprove", at(3 * 24 * time.Hour), true, actionReapprove},
		{"an hour left, --reapprove", at(time.Hour), true, actionReapprove},
		{"no deadline, --reapprove", &device.Token{}, true, actionNoDeadline},
	}
	for _, tc := range cases {
		if got := planApprovedLogin(tc.token, tc.reapprove); got != tc.want {
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
