package launch

import (
	"testing"
	"time"
)

// The harness re-runs the credential command at 40% of the token's lifetime,
// so a refresh starts with 60% of the life still on the token in hand.
func TestDeviceTokenRefreshMsIsFortyPercentOfTheLifetime(t *testing.T) {
	cases := []struct {
		lifetime time.Duration
		want     int
	}{
		{10 * time.Minute, 240000},
		{5 * time.Minute, 120000},
		{15 * time.Minute, 360000},
		{2 * time.Minute, 48000},
	}
	for _, tc := range cases {
		if got := DeviceTokenRefreshMs(tc.lifetime); got != tc.want {
			t.Errorf("DeviceTokenRefreshMs(%v) = %d, want %d", tc.lifetime, got, tc.want)
		}
	}
}
