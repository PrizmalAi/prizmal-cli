package launch

import "time"

// DeviceTokenRefreshMs is the interval a harness re-runs the command-backed
// credential on, in milliseconds: 40% of the token's lifetime. For the 10
// minute token the Config API issues that is 4 minutes, so a background refresh
// starts with at least 6 minutes still on the token in hand. Claude Code's own
// default is 5 minutes, which would refresh with only half the life left; 40%
// buys the margin a laptop sleep spends. The lifetime comes from the first
// token's expires_in, so a shorter lifetime shortens the interval with it.
//
// It is named after the token rather than after Claude Code because Codex
// refreshes the same device token on the same interval.
func DeviceTokenRefreshMs(lifetime time.Duration) int {
	return int(lifetime * 2 / 5 / time.Millisecond)
}
