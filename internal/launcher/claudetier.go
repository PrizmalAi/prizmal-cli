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

// ClaudeHelperTTLMs is the interval Claude Code re-runs the apiKeyHelper on, in
// milliseconds: 4 minutes. The device token expires 10 minutes after issue, so
// a background refresh starts with at least 6 minutes still on the token in
// hand. Claude Code's own default is 5 minutes, which would refresh with only
// 5 minutes left; 4 minutes buys the margin a laptop sleep spends.
const ClaudeHelperTTLMs = 240000

// OneMillionSuffix is Claude Code's own context-budgeting instruction. It is
// not part of any model id: Claude Code strips the suffix from the name before
// it sends the request and budgets a 1M context window for that name.
const OneMillionSuffix = "[1m]"

// ClaudeFamilyIDs maps each Claude Code model family to the catalog ids a
// request can resolve to for that family, newest first. The ids are the provider_ids
// first_party strings from the installed Claude Code binary catalog.
//
// The mythos family is absent: it has no tier alias (a request for
// claude-mythos-5 or -5-1 passes through unmapped, reaching the Switch as the
// id itself), so no entry belongs here.
var ClaudeFamilyIDs = map[ModelTier][]string{
	ModelTierOpus: {
		"claude-opus-5-5",
		"claude-opus-5",
		"claude-opus-4-8",
		"claude-opus-4-7",
		"claude-opus-4-6",
		"claude-opus-4-5-20251101",
		"claude-opus-4-20250514",
		"claude-opus-4-1-20250805",
	},
	ModelTierSonnet: {
		"claude-sonnet-5-5",
		"claude-sonnet-5",
		"claude-sonnet-4-6",
		"claude-sonnet-4-5-20250929",
		"claude-sonnet-4-20250514",
		"claude-3-7-sonnet-20250219",
		"claude-3-5-sonnet-20241022",
	},
	ModelTierHaiku: {
		"claude-haiku-4-5-20251001",
		"claude-3-5-haiku-20241022",
	},
	ModelTierFable: {
		"claude-fable-5-1",
		"claude-fable-5",
	},
}

// ClaudeTierModel is the model a tier runs, as the tenant named it.
func ClaudeTierModel(tier ModelTier) string {
	return ClaudeModelPrefix + "tier-" + string(tier)
}
