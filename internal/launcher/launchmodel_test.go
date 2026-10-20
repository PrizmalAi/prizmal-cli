package launch

import (
	"reflect"
	"slices"
	"testing"
)

// TestLaunchModelCarriesOnlyCatalogFacts pins LaunchModel's field set.
//
// Every field in it has exactly one writer: ParseSwitchCatalog fills Name,
// Capabilities, Tier, Tiers and Description, and withClaudeTiers fills FoldedInto, HeldBy and AddedTier. A
// field with no writer is zero at every runtime, and a branch reading it looks
// live while picking its default on every launch. That is how Remote,
// ToolCapable, the Details struct, ContextLength and MaxOutputTokens grew six
// unreachable branches in codex, opencode and pi. This test is what keeps the
// next one from arriving: adding a field means adding the writer in the same
// change.
func TestLaunchModelCarriesOnlyCatalogFacts(t *testing.T) {
	want := []string{"AddedTier", "Capabilities", "Description", "FoldedInto", "HeldBy", "Name", "Tier", "Tiers"}

	var got []string
	typ := reflect.TypeFor[LaunchModel]()
	for i := range typ.NumField() {
		got = append(got, typ.Field(i).Name)
	}
	slices.Sort(got)

	if !slices.Equal(got, want) {
		t.Fatalf("LaunchModel fields = %v, want %v", got, want)
	}
}
