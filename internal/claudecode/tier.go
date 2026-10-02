// Package claudecode holds what prizmal knows about Claude Code the harness,
// as opposed to what it knows about a model a tenant serves.
//
// It owns one concept: the tier model. A tier is the model family Claude Code
// resolves a name to, and everything here is a fact about that resolution —
// the vocabulary, the first-party catalog ids a tier can land on, how Claude
// Code renders a model of that tier, the alias a tenant writes for it, and the
// [1m] decoration Claude Code puts on a name.
//
// The facts live here rather than in the launcher because four files need
// them and none of the four is the right home. The picker draws rows and needs
// how a tier is rendered. The Claude launcher spells a settings JSON and needs
// the id lineage a request resolves through. The catalog builder needs the
// alias name. The Switch's wire parser needs to know whether the `tier` field
// it just read names a real tier — and that last one is the reason this is a
// package: a wire parser that validates the Switch's schema against a Claude
// Code rendering table has the layering backwards, so a tier the Switch names
// but this CLI cannot render would be dropped with the reason nowhere near the
// code that dropped it.
//
// Everything below is researched, not guessed. The comments record which ids
// are deliberately absent, why dated and undated ids behave differently, and
// what was verified on the wire. They are the point of the tables; keep them.
package claudecode

import "strings"

// Tier is the Claude Code model family a model is recognised as, from a
// tier word in its name.
type Tier string

const (
	Opus   Tier = "opus"
	Sonnet Tier = "sonnet"
	Haiku  Tier = "haiku"
	Fable  Tier = "fable"
)

// Tiers are the tier names a model can be recognised by, in the order they
// are tried. A name carrying more than one is pathological; the first match
// wins and the row still routes by its own model id.
var Tiers = []Tier{Opus, Sonnet, Haiku, Fable}

// Profile is how Claude Code treats a model of one tier.
type Profile struct {
	// BehavesAs is the first-party id whose client-side handling (prompt
	// profile, capability and effort defaults) Claude Code applies to the
	// model. Claude Code resolves the field through its own model catalog, so
	// a tier word would resolve to nothing: the session would warn that the
	// model is unknown and run on the unknown-model profile. Each id is one
	// every supported Claude Code release carries.
	//
	// No tier runs as a haiku model. Claude Code refuses auto mode to a model
	// released before Claude Opus 4.6, Haiku 4.5 among them, so the haiku tier
	// runs as Sonnet 5 and keeps auto mode. The row still reads "Haiku tier",
	// and the Switch routes it by its own name.
	BehavesAs string
	// OneMillion reports whether that model accepts a 1M context window.
	// Claude Code drops a /model row that asks for 1M on a model without one.
	OneMillion bool
	// Description is the row's text in Claude Code's /model picker, which
	// otherwise reads "Custom model (<model>)" whatever BehavesAs says.
	Description string
}

// Profiles maps each tier to how Claude Code treats it.
var Profiles = map[Tier]Profile{
	Opus:   {BehavesAs: "claude-opus-5", OneMillion: true, Description: "Opus tier"},
	Sonnet: {BehavesAs: "claude-sonnet-5", OneMillion: true, Description: "Sonnet tier"},
	Haiku:  {BehavesAs: "claude-sonnet-5", OneMillion: true, Description: "Haiku tier"},
	Fable:  {BehavesAs: "claude-fable-5-1", OneMillion: true, Description: "Fable tier"},
}

// ModelPrefix is the decoration the tenant's aliases carry so that
// Claude Code's row filter keeps them. It is stripped from a display label
// only: the id sent to the Switch keeps it, because that is the name that
// routes.
const ModelPrefix = "claude-"

// FamilyIDs maps each Claude Code model family to the catalog ids a
// request can resolve to for that family, newest first. The ids are the provider_ids
// first_party strings from the installed Claude Code binary catalog.
//
// The mythos family is absent: it has no tier alias (a request for
// claude-mythos-5 or -5-1 passes through unmapped, reaching the Switch as the
// id itself), so no entry belongs here.
var FamilyIDs = map[Tier][]string{
	Opus: {
		"claude-opus-5-5",
		"claude-opus-5",
		"claude-opus-4-8",
		"claude-opus-4-7",
		"claude-opus-4-6",
		"claude-opus-4-5-20251101",
		"claude-opus-4-20250514",
		"claude-opus-4-1-20250805",
	},
	Sonnet: {
		"claude-sonnet-5-5",
		"claude-sonnet-5",
		"claude-sonnet-4-6",
		"claude-sonnet-4-5-20250929",
		"claude-sonnet-4-20250514",
		"claude-3-7-sonnet-20250219",
		"claude-3-5-sonnet-20241022",
	},
	Haiku: {
		"claude-haiku-4-5-20251001",
		"claude-3-5-haiku-20241022",
	},
	Fable: {
		"claude-fable-5-1",
		"claude-fable-5",
	},
}

// TierModel is the model a tier runs, as the tenant named it.
func TierModel(tier Tier) string {
	return ModelPrefix + "tier-" + string(tier)
}

// InferTier reports which tier a model name names, matching the tier words
// anywhere in the name and case-insensitively. A name without one has no tier,
// which is the common case for a model the tenant named itself.
func InferTier(name string) (Tier, bool) {
	lower := strings.ToLower(name)
	for _, tier := range Tiers {
		if strings.Contains(lower, string(tier)) {
			return tier, true
		}
	}
	return "", false
}

// KnownTier returns the tier the Switch named, lowercased, or "" for a name
// that is not one of the Claude tiers.
//
// It answers a question about the Switch's wire, from inside the package that
// owns the vocabulary the wire is validated against. The answer is the
// vocabulary, not the rendering table: Profiles exists so a /model row can name
// a profile Claude Code recognises, and a tier with no profile to render would
// still be a legal thing for the Switch to say. Reading Profiles here instead
// would make "this tier is real" and "this tier renders a Claude Code profile"
// the same question, and would drop such a tier silently.
//
// An unrecognised name is dropped rather than passed through, because Tier is
// a closed set here and a caller that reads it as free-form will index
// Profiles with a key that is not in it.
func KnownTier(tier string) string {
	tier = strings.ToLower(strings.TrimSpace(tier))
	if _, ok := Profiles[Tier(tier)]; ok {
		return tier
	}
	return ""
}

// OneMillionSuffix is Claude Code's own context-budgeting instruction. It is
// not part of any model id: Claude Code strips the suffix from the name before
// it sends the request and budgets a 1M context window for that name.
const OneMillionSuffix = "[1m]"

// RoutableName removes one trailing [1m] decoration from a model name, leaving
// the name a caller passes back to route.
//
// It removes exactly one, and that is what a name arriving from the Switch
// needs: the Switch decorates the id it publishes once, and the id it publishes
// is the name it serves. Trimming the rest would be trimming a name that never
// arrived — and would break the one thing this must not do, which is quietly
// rename a model by deleting a substring of its id.
//
// Use RoutableNameAll where a value can reach here already decorated.
func RoutableName(name string) string {
	return strings.TrimSuffix(name, OneMillionSuffix)
}

// RoutableNameAll removes every trailing [1m] decoration from a model name.
//
// A value that reached a launch through a config file or a shell export can
// carry more than the one the Switch decorates, and suffixes must not
// accumulate: a name spelled [1m] twice is sent as [1m][1m], Claude Code
// strips one, and the request reaches the Switch carrying a decoration that is
// part of no id. So the spelling paths strip the whole run.
//
// It is a loop rather than strings.TrimRight because the suffix is a bracket,
// not a character set: a cutset of "[1m]" would eat the letters of a model
// whose id ends in one of them.
func RoutableNameAll(name string) string {
	bare := name
	for {
		stripped := RoutableName(bare)
		if stripped == bare {
			return bare
		}
		bare = stripped
	}
}
