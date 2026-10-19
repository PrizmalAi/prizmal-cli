package launch

// DeviceTokenRefreshMs is the interval a harness re-runs the command-backed
// credential on, in milliseconds: 4 minutes. The device token expires 10
// minutes after issue, so a background refresh starts with at least 6 minutes
// still on the token in hand. Claude Code's own default is 5 minutes, which
// would refresh with only 5 minutes left; 4 minutes buys the margin a laptop
// sleep spends.
//
// It is named after the token rather than after Claude Code because Codex and
// pi refresh the same device token on the same interval.
const DeviceTokenRefreshMs = 240000
