package claudecode

import (
	"slices"
	"strings"
	"testing"
)

// A model whose name carries a tier word behaves as that tier, matched anywhere
// in the name and case-insensitively. The tier sets how Claude Code runs and
// shows the model. It never decides what serves the request.
func TestInferTier(t *testing.T) {
	for _, tc := range []struct {
		name string
		want Tier
		ok   bool
	}{
		{name: "claude-tier-haiku", want: Haiku, ok: true},
		{name: "claude-tier-sonnet", want: Sonnet, ok: true},
		{name: "claude-tier-opus", want: Opus, ok: true},
		{name: "claude-tier-fable", want: Fable, ok: true},
		{name: "claude-Opus-5", want: Opus, ok: true},
		{name: "my-sonnet-thing", want: Sonnet, ok: true},
		{name: "gpt-oss:20b"},
		{name: "ollama-open-china-1"},
		{name: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := InferTier(tc.name)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("InferTier(%q) = (%q, %v), want (%q, %v)", tc.name, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// Every tier is in the vocabulary, in the order tiers are tried. A tier
// missing from Tiers is invisible to InferTier, to the Switch's wire
// validation, and to the rows the catalog builder puts first — the three
// places a tier has to appear, none of which fails loudly when one does not.
func TestEveryProfileTierIsInTheVocabulary(t *testing.T) {
	for _, tier := range Tiers {
		if _, ok := Profiles[tier]; !ok {
			t.Errorf("tier %q has no Profile, so a row for it would carry no behavesAs and no text", tier)
		}
	}
	for tier := range Profiles {
		if !slices.Contains(Tiers, tier) {
			t.Errorf("tier %q has a Profile but is not in Tiers, so no row is ever built for it", tier)
		}
		if _, ok := FamilyIDs[tier]; !ok {
			t.Errorf("tier %q has no FamilyIDs, so a request naming one of its catalog ids would pass through unmapped", tier)
		}
	}
}

// The profile id is the id a row behaves as, so it must itself be one of the
// catalog ids the tier resolves to. A profile id outside the lineage is
// resolved by nothing in the running Claude Code, and the row warns that the
// model is unknown.
// Every tier behaves as a model of its own family, with one deliberate
// exception: no tier runs as a haiku model. Claude Code refuses auto mode to a
// model released before Claude Opus 4.6, so haiku runs as Sonnet 5 and takes
// its profile from the sonnet lineage. That exception is what profileInLineage
// in the launcher exists to detect, so it is pinned here rather than left to a
// reader to notice.
func TestProfileIDIsInItsOwnLineage(t *testing.T) {
	for tier, profile := range Profiles {
		lineage := FamilyIDs[tier]
		if tier == Haiku {
			lineage = FamilyIDs[Sonnet]
		}
		if !slices.Contains(lineage, profile.BehavesAs) {
			t.Errorf("tier %q behaves as %q, which is not in the %v lineage %v", tier, profile.BehavesAs, tier, lineage)
		}
	}
}

// A tier alias is the name a tenant writes for the tier. It carries the prefix
// Claude Code's row filter keeps, so a launch resolves the row's model through
// the name the tenant provisioned.
func TestTierModelNames(t *testing.T) {
	for _, tier := range Tiers {
		got := TierModel(tier)
		if want := ModelPrefix + "tier-" + string(tier); got != want {
			t.Errorf("TierModel(%q) = %q, want %q", tier, got, want)
		}
	}
}

// The Switch tags a catalog entry with a tier. The name is lowercased and
// trimmed before it is matched, and anything outside the vocabulary is dropped
// rather than passed through, because every reader treats Tier as one of the
// known tiers and indexes Profiles with it.
func TestKnownTier(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{in: "opus", want: "opus"},
		{in: "OPUS", want: "opus"},
		{in: "  Sonnet  ", want: "sonnet"},
		{in: "haiku", want: "haiku"},
		{in: "fable", want: "fable"},
		{in: "mythos"},
		{in: "opus-5"},
		{in: ""},
	} {
		t.Run(tc.in, func(t *testing.T) {
			if got := KnownTier(tc.in); got != tc.want {
				t.Fatalf("KnownTier(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// The Switch's wire field is validated against the vocabulary, not against the
// rendering table. The two hold the same set today, so the only thing that
// distinguishes the two implementations is what happens when they diverge — and
// a tier with no Profile is exactly the case: the Switch may name a tier this
// CLI has no first-party id to render it as, and dropping it there would make
// the wire parser depend on what the picker can draw.
func TestKnownTierFollowsTheVocabularyNotTheProfiles(t *testing.T) {
	defer func(tiers []Tier, profiles map[Tier]Profile) {
		Tiers, Profiles = tiers, profiles
	}(Tiers, Profiles)

	Tiers = []Tier{Opus, "unrendered"}
	Profiles = map[Tier]Profile{Opus: Profiles[Opus]}

	if got := KnownTier("unrendered"); got != "unrendered" {
		t.Errorf("KnownTier(%q) = %q, want it kept: it is in the vocabulary, and a tier with no Profile to render it as is still a tier the Switch can name", "unrendered", got)
	}
	if got := KnownTier("sonnet"); got != "" {
		t.Errorf("KnownTier(%q) = %q, want \"\": it is in neither set", "sonnet", got)
	}
}

// The Switch decorates the id it publishes exactly once, so reading a name off
// the wire removes one suffix and stops. Removing more would rename a model by
// deleting a substring of its id.
func TestRoutableNameRemovesOneSuffix(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{in: "claude-tier-opus[1m]", want: "claude-tier-opus"},
		{in: "claude-tier-opus", want: "claude-tier-opus"},
		{in: "", want: ""},
		{in: "claude-tier-opus[1m][1m]", want: "claude-tier-opus[1m]"},
		{in: "gpt-oss:20b", want: "gpt-oss:20b"},
		// A :latest tag routes, so it survives: stripping it would print a
		// name that resolves to a different model than the one listed.
		{in: "claude-tier-opus[1m]:latest", want: "claude-tier-opus[1m]:latest"},
	} {
		if got := RoutableName(tc.in); got != tc.want {
			t.Errorf("RoutableName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A name reaching a launch through a config file or a shell export can already
// be decorated, and suffixes must not accumulate. The whole trailing run goes.
func TestRoutableNameAllRemovesEverySuffix(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{in: "claude-tier-opus[1m]", want: "claude-tier-opus"},
		{in: "claude-tier-opus[1m][1m]", want: "claude-tier-opus"},
		{in: "claude-tier-opus[1m][1m][1m]", want: "claude-tier-opus"},
		{in: "claude-tier-opus", want: "claude-tier-opus"},
		{in: "", want: ""},
		// Not a cutset strip: the letters of an id that ends in one of the
		// suffix's characters are part of the name.
		{in: "some-m", want: "some-m"},
		{in: "some-1", want: "some-1"},
		{in: "claude-tier-opus[1m]:latest", want: "claude-tier-opus[1m]:latest"},
	} {
		if got := RoutableNameAll(tc.in); got != tc.want {
			t.Errorf("RoutableNameAll(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Stripping a suffix is the same operation on a name the two spellings agree
// about, so no caller has to know which decoration a name arrived with.
func TestRoutableNameAllAgreesOnASingleSuffix(t *testing.T) {
	for _, name := range []string{"claude-tier-haiku", "claude-tier-opus[1m]", "gpt-oss:20b", ""} {
		if RoutableName(name) != RoutableNameAll(name) {
			t.Errorf("RoutableName(%q) = %q, RoutableNameAll = %q, want them to agree on one suffix",
				name, RoutableName(name), RoutableNameAll(name))
		}
	}
}

// Only Haiku 4.5 has no 1M window. A tier marked 1M that is not sends the
// [1m] suffix, and Claude Code drops a /model row that asks for 1M on a model
// without one, so a wrong answer here loses the row rather than the window.
// Every tier accepts a 1M window, haiku included: it runs as Sonnet 5, which
// has one. This is the invariant the [1m] suffix on every override value
// depends on, because Claude Code drops a /model row that asks for 1M on a
// model without one.
func TestEveryTierHasAMillionWindow(t *testing.T) {
	for _, tier := range Tiers {
		if !Profiles[tier].OneMillion {
			t.Errorf("Profiles[%q].OneMillion = false, want true: every tier runs as a model with a 1M window", tier)
		}
	}
}

// The suffix is Claude Code's instruction, not part of any model id, so every
// id in a lineage is the bare spelling. A decorated id here would be a
// modelOverrides key Claude Code never resolves.
func TestLineageIDsCarryNoDecoration(t *testing.T) {
	for tier, ids := range FamilyIDs {
		for _, id := range ids {
			if strings.Contains(id, OneMillionSuffix) {
				t.Errorf("tier %q lineage carries the decorated id %q", tier, id)
			}
		}
	}
}
