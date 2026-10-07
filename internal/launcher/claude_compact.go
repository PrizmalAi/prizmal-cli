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

// claudeCompactFraction is the share of the effective window at which Claude
// Code compacts the conversation. 90% of it is the trigger the Compact
// contract names, and the rest of the window is the headroom: for 1M, the
// growth between one request and the next stays well under the 118k gap in
// the sessions the Switch serves today. Raise this constant when the Switch
// maps every provider's overflow error onto the shape Claude Code
// recognizes; the share, not the stated setting below, is the named knob.
const claudeCompactFraction = 90

// The three numbers Claude Code's threshold math feeds a stated window
// through (iV/N7 in the 2.1.x binaries). The resolved window is the stated
// value; the effective window subtracts min(maxOutputTokens, 20000), so the
// output budget; the threshold subtracts 13000 more, the margin. The setting
// states the WINDOW AHEAD of all three, so the stated value is
// trigger + budget + margin.
const (
	// claudeCompactOutputTokensBudget is min(maxOutputTokens, 20000) for
	// these launches: the max-output Claude Code budgets for a model it does
	// not recognize is 32000, clamped to 20000 here. Pin 2.1.283's XK shows
	// the unknown-model default 32000; the threshold math takes the min with
	// 20000.
	claudeCompactOutputTokensBudget = 20_000

	// claudeCompactMargin is the fixed margin Claude Code keeps under the
	// effective window before it compacts, read from the binaries'
	// threshold function.
	claudeCompactMargin = 13_000
)

// claudeCompactTrigger is the counted-tokens level the launch aims at: 90%
// of the 1M model's effective window.
func claudeCompactTrigger() int {
	return (claudeCompactWindow - claudeCompactOutputTokensBudget) / 100 * claudeCompactFraction
}

// claudeCompactModelSettings is the autoCompactWindow value the launcher
// states in the inline --settings JSON: the trigger plus the two subtractions
// Claude Code makes before it reaches the trigger, so that the counted
// tokens at which the summary request goes out is the trigger itself
// (915000 − 20000 − 13000 = 882000).
func claudeCompactModelSettings() int {
	return claudeCompactTrigger() + claudeCompactOutputTokensBudget + claudeCompactMargin
}

// claudeCompactKey is the settings key Claude Code resolves a model's window
// under: the canonical name, lowercased, with the [1m] budget suffix
// stripped. Claude Code canonicalizes the spelling the same way (It strips
// the suffix case-insensitively), so the suffix it carries in the settings
// model field and the --model flag, a /model switch to a bare spelling, a
// dated spelling, and a tier-suffixed one all resolve to the same key.
//
// Evidence: Claude Code's settings aggregation (E$t in 2.1.292) keys the
// byModel map by its canonical function (Aq), which strips a trailing [1m]
// case-insensitively and lowercases; its resolver (wE) reads
// byModel[e.settingsKey] before the top-level default.
func claudeCompactKey(model string) string {
	bare := model
	for {
		stripped := strings.TrimSuffix(strings.ToLower(bare), oneMillionSuffix)
		if stripped == bare {
			break
		}
		bare = stripped
	}
	return bare
}

// claudeCompactModelSettingsBlock builds the settings JSON's modelSettings
// object with the window stated under every key a session of this launch can
// resolve to: the model that runs, and every picker row the launch offers.
// Each entry carries autoCompactWindow; the two fields Claude Code otherwise
// reads there (effortLevel, maxEffortLevel) stay unset, because an entry that
// names no effort keeps the launch's own.
//
// The modelSettings schema accepts extra per-model keys on every release the
// baselines pin: 2.1.292 reads autoCompactWindow out of it, and 2.1.283
// keeps the whole per-model object as .passthrough() data.
func claudeCompactModelSettingsBlock(model string, rows []ModelRow) map[string]any {
	entry := func() map[string]any { return map[string]any{"autoCompactWindow": claudeCompactModelSettings()} }
	block := map[string]any{claudeCompactKey(claudeModelName(model)): entry()}
	for _, row := range rows {
		key := claudeCompactKey(claudeRowModelName(row))
		if _, ok := block[key]; !ok {
			block[key] = entry()
		}
	}
	return block
}