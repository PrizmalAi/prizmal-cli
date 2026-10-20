package claudecode

import (
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/tiers"
)

// Each Claude family runs one generic tier, and back.
func TestEachFamilyMapsToItsGenericTier(t *testing.T) {
	for family, want := range map[Tier]tiers.Tier{Opus: tiers.Frontier, Sonnet: tiers.Core, Haiku: tiers.Flash, Fable: tiers.Premier} {
		if got, ok := GenericTier(family); !ok || got != want {
			t.Errorf("GenericTier(%s) = %q, %v, want %q", family, got, ok, want)
		}
		if got, ok := FamilyOf(want); !ok || got != family {
			t.Errorf("FamilyOf(%s) = %q, %v, want %q", want, got, ok, family)
		}
	}
}

// The parser accepts the generic names and keeps the legacy Claude words.
func TestParseTierAcceptsGenericAndLegacyValues(t *testing.T) {
	for in, want := range map[string]Tier{"frontier": Opus, "Core": Sonnet, "flash": Haiku, "premier": Fable, "opus": Opus, "haiku": Haiku, "smart": "", "genius": "", "sol": ""} {
		got, ok := ParseTier(in)
		if got != want || ok != (want != "") {
			t.Errorf("ParseTier(%q) = %q, %v, want %q", in, got, ok, want)
		}
	}
}
