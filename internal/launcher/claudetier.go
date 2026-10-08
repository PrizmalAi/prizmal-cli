package launch

import (
	"strings"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
)

// EnsureHelperBaseURL makes sure the Switch URL this launch resolved is in the
// child environment, so the apiKeyHelper Claude Code spawns refreshes against
// the same host.
//
// The helper is a separate prizmal process and resolves the URL from scratch:
// --url, then $PRIZMAL_SWITCH_URL, then the config file, then the production
// default. A launch aimed at a non-default host by --url or by the environment
// would otherwise let the helper fall back to production and mint a token the
// launch's own Switch cannot use. Pinning the launch's resolved URL removes
// that fallback. A config-file URL needs no pin, because the helper reads the
// same file.
func EnsureHelperBaseURL(env []string, baseURL string) []string {
	if baseURL == "" {
		return env
	}
	for _, kv := range env {
		if name, _, _ := strings.Cut(kv, "="); name == envconfig.EnvVar {
			return env // the operator's own environment already names the host
		}
	}
	return append(env, envconfig.EnvVar+"="+baseURL)
}

// DeviceTokenRefreshMs is the interval a harness re-runs prizmal's token helper
// on, in milliseconds: 4 minutes. The device token expires 10 minutes after issue, so
// a background refresh starts with at least 6 minutes still on the token in
// hand. Claude Code's own default is 5 minutes, which would refresh with only
// 5 minutes left; 4 minutes buys the margin a laptop sleep spends.
const DeviceTokenRefreshMs = 240000

// OneMillionSuffix is Claude Code's own context-budgeting instruction. It is
// not part of any model id: Claude Code strips the suffix from the name before
// it sends the request and budgets a 1M context window for that name.
const OneMillionSuffix = "[1m]"

// ClaudeTierModel is the model a tier runs, as the tenant named it.
func ClaudeTierModel(tier ModelTier) string {
	return ClaudeModelPrefix + "tier-" + string(tier)
}
