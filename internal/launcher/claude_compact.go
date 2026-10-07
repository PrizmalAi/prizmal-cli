package launch

import "strings"

// claudeCompactWindow is the context window the launcher declares to Claude
// Code for every model a launch can route to. It is stated, not learned, on
// the same evidence as codexFallbackContextWindow: GET /v1/models carries no
// context length for any entry (switchCatalogEntry reads id,
// input_modalities, tier and description), so nothing in the tree can learn
// the real one, and every model the Switch serves today has the 1M window the
// [1m] suffix already claims to Claude Code. Claude Code clamps the setting
// to the window it resolves for the model itself (min of the two), so a release
// that knows a smaller real window for a recognized model keeps that window.
const claudeCompactWindow = 1_000_000

// claudeCompactFraction is the percentage of the window at which Claude Code
// compacts. Claude Code's threshold is min(window*percent/100,
// window-13000) when the source is set, so 90% declares the compaction
// buffer itself: with 1M, compaction starts at 882000 tokens, about 118k
// under the ceiling. Production traffic over 14 days measured per-request
// context growth within a session at 13k tokens P99, 83k P99.9, and the same
// session's growth past 90% of 1M stays under that P99.9 with margin for
// tokenizer drift. Raise this constant when the Switch maps every provider's
// overflow error onto the shape Claude Code recognizes; at 95% the threshold
// is 931000.
const claudeCompactFraction = 90

// claudeCompactModelSettings is the autoCompactWindow value the launcher
// states in the inline --settings JSON.
func claudeCompactModelSettings() int {
	return claudeCompactWindow / 100 * claudeCompactFraction
}

// claudeCompactKey is the settings key Claude Code resolves a model's window
// under: the canonical name, lowercased, with the [1m] budget suffix
// stripped. Claude Code canonicalizes the spelling the same way, so the
// suffix it carries in the settings model field and the --model flag, a
// /model switch to a bare spelling, a dated spelling, and a tier-suffixed
// one all resolve to the same key.
//
// Evidence: Claude Code's settings aggregation (E$t in 2.1.292) keys the
// byModel map by its canonical function (Aq), which strips a trailing [1m]
// case-insensitively and lowercases; its resolver (wE) reads
// byModel[e.settingsKey] before the top-level default.
func claudeCompactKey(model string) string {
	return strings.ToLower(strings.TrimSuffix(model, oneMillionSuffix))
}