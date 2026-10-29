// Package tiers holds the generic model tiers the Switch reports and the word
// each harness shows for them.
//
// A tenant's model answers to a tier alias: frontier, core, flash or
// premier. The alias is the name the Switch routes, so a launch sends it as it
// is. The harness words (Opus, Sol and the rest) are labels only. The Switch
// routes none of the Codex words, and Claude Code resolves its own words to its
// own ids before a request leaves the client.
package tiers

import "strings"

// Tier is a generic tier, spelled as its alias.
type Tier string

const (
	Frontier Tier = "frontier"
	Core     Tier = "core"
	Flash    Tier = "flash"
	Premier  Tier = "premier"
)

// All are the generic tiers in the order their rows appear.
var All = []Tier{Frontier, Core, Flash, Premier}

// Vocabulary is the set of words a harness shows for the tiers.
type Vocabulary string

const (
	// Generic labels each row with the tier's own name, for a pick that names
	// no harness.
	Generic Vocabulary = "generic"
	// ClaudeCode labels the rows with Claude Code's model families.
	ClaudeCode Vocabulary = "claude-code"
	// Codex labels the rows with the Codex tier words.
	Codex Vocabulary = "codex"
	// ConfigNames shows no tier rows, only the models by name. pi,
	// OpenCode and Cline use it.
	ConfigNames Vocabulary = "config-names"
)

// words is the one table that says which harness word names which tier. Each
// row lists the words for All, in order.
var words = map[Vocabulary][]string{
	ClaudeCode: {"Opus", "Sonnet", "Haiku", "Fable"},
	Codex:      {"Sol", "Terra", "Luna", "Astra"},
}

// Word returns the label a vocabulary shows for tier, or "" when the
// vocabulary shows no tier rows.
func (v Vocabulary) Word(tier Tier) string {
	if v == Generic {
		return string(tier)
	}
	row := words[v]
	for i, t := range All {
		if t == tier && i < len(row) {
			return row[i]
		}
	}
	return ""
}

// LabelsByConfig reports whether a tier row is labelled with the model
// that holds the tier, with the word in its description. Codex does: a Codex
// word as a row's name would read as an OpenAI model.
func (v Vocabulary) LabelsByConfig() bool { return v == Codex }

// Like is the description prefix that says which tier a row is like: Sol-like.
func (v Vocabulary) Like(tier Tier) string {
	if w := v.Word(tier); w != "" {
		return w + "-like"
	}
	return ""
}

// ShowsTiers reports whether the vocabulary has a row per tier.
func (v Vocabulary) ShowsTiers() bool { return v == Generic || len(words[v]) > 0 }

// FromWord returns the tier a vocabulary word labels, matched without regard
// to case.
func (v Vocabulary) FromWord(word string) (Tier, bool) {
	word = strings.TrimSpace(word)
	for _, tier := range All {
		if w := v.Word(tier); w != "" && strings.EqualFold(w, word) {
			return tier, true
		}
	}
	return "", false
}

// Parse returns the generic tier a value names, without regard to case and
// surrounding space.
func Parse(value string) (Tier, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, tier := range All {
		if string(tier) == value {
			return tier, true
		}
	}
	return "", false
}

// Alias is the name the Switch routes for a tier.
func (t Tier) Alias() string { return string(t) }
