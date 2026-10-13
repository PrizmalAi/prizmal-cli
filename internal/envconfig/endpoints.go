package envconfig

import "strings"

// The endpoint shapes. Every harness is pointed at the Switch through one of
// these three, and each one answers the same two questions once, here: is the
// host spelled the way a client can dial it, and how many /v1 suffixes does
// this consumer carry? An adapter that assembles its own URL repeats both
// decisions, and one that decides them differently is a bug that only shows up
// for one harness and one configured URL — a base carrying /v1, or a loopback
// address a TLS-terminating Switch fronts as localhost.

// resolvedBase returns the resolved base with the loopback rewrite applied and
// any trailing "/" trimmed, so the shape helpers below can cut the suffix off
// the end without a trailing slash splitting the match.
func resolvedBase() string {
	return strings.TrimRight(ConnectableHost().String(), "/")
}

// UnversionedBaseURL is the resolved base for a client that appends /v1
// itself. Two consumers need that spelling: Claude Code, which appends /v1 to
// ANTHROPIC_BASE_URL and would otherwise send gateway discovery to
// /v1/v1/models (a 404 that drops its model picker back to the built-in
// list), and Cline, whose globalState.json records the host rather than the
// versioned provider endpoint its providers.json entry points at.
//
// Every trailing /v1 comes off, not just one, because a base a user typed with
// the version already on it is the case this exists for, and /v1/v1 is the
// only other outcome. A /v1 anywhere else in the path stays: it is part of the
// path the operator configured, not a version suffix.
func UnversionedBaseURL() string {
	base := resolvedBase()
	for {
		trimmed, ok := strings.CutSuffix(base, "/v1")
		if !ok {
			return base
		}
		base = trimmed
	}
}

// OpenAIBaseURL is the resolved base for the OpenAI-shaped harnesses — codex,
// cline, pi, opencode — which all expect the version on the end of the URL and
// none of which append it themselves. A configured base that already carries
// /v1 is normalized to exactly one, because two of them ask for
// /v1/v1/chat/completions, which the Switch answers with a 404 the harness
// reports as an unreachable provider. trailingSlash adds the slash Codex's
// config format expects and the other three do not want.
func OpenAIBaseURL(trailingSlash bool) string {
	if trailingSlash {
		return UnversionedBaseURL() + "/v1/"
	}
	return UnversionedBaseURL() + "/v1"
}

// CatalogURL is the endpoint prizmal itself asks the Switch for the tenant's
// models at: GET {base}/v1/models. It is the third reason a base carrying /v1
// has to be tolerated, next to Claude Code appending its own and the
// OpenAI-shaped clients needing theirs; a base that already ends in /v1 would
// otherwise ask for /v1/v1/models, which 404s and empties the model list.
//
// The path is part of what the endpoint is, so this returns a full URL rather
// than a base: the caller hands it straight to http.NewRequestWithContext and
// prints it verbatim when the request fails.
func CatalogURL() string {
	return UnversionedBaseURL() + "/v1/models"
}
