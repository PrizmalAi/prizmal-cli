package launch

import (
	"strconv"
	"strings"
)

// claudeCompactWindowEnv names the variable that tells Claude Code the context
// window to budget. Claude Code reads it ahead of every other source, and a
// window it reads there is one the operator or a setting chose, not one it
// guessed.
//
// That is what a launch needs. The ids a launch hands Claude Code, with their
// [1m] suffix and the tenant's own names, are not in its model catalogue, so
// its window resolver answers with the source "auto", and its auto-compact
// check returns early for that source: nothing compacts, and a session grows
// until the endpoint refuses it. A window from this variable turns the source
// into "env" and the check runs.
const claudeCompactWindowEnv = "CLAUDE_CODE_AUTO_COMPACT_WINDOW"

// claudeCompactWindow is the window the launch states, for every model it can
// route to. It is stated, not learned: GET /v1/models carries no context
// length for any entry (switchCatalogEntry reads id, input_modalities, tier
// and description), so nothing in the tree can learn the real one, and every
// model the Switch serves today has the 1M window the [1m] suffix already
// claims.
//
// Claude Code then compacts at its own threshold, 13000 tokens under the
// window less the 20000-token output budget, which is 967000 counted tokens
// for 1M. A refusal from the endpoint that Claude Code recognizes as "prompt
// is too long" also starts a compaction, whatever the window's source.
const claudeCompactWindow = 1_000_000

// claudeCompactWindowValue returns the value the child gets. An operator who
// exports a smaller positive window keeps it, since a smaller window only
// compacts sooner. Anything else, whether unset, empty, not a number, zero,
// negative or larger than the launch's window, becomes the launch's window.
func claudeCompactWindowValue(inherited string) string {
	if n, err := strconv.Atoi(strings.TrimSpace(inherited)); err == nil && n > 0 && n < claudeCompactWindow {
		return strconv.Itoa(n)
	}
	return strconv.Itoa(claudeCompactWindow)
}
