package claude

import (
	"github.com/PrizmalAi/prizmal-cli/internal/claudecode"
	"slices"
	"testing"

	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

// A row's behavesAs is the id of a model Claude Code knows. Claude Code reads
// the field through its model catalog, so a tier word such as "opus" resolves
// to nothing: the launch warns that the model is unknown and runs on the
// unknown-model prompt profile. Each id is one every supported Claude Code
// release carries, and one the tier's modelOverrides lineage already names.
//
// The haiku tier runs as Sonnet 5, from the sonnet lineage, because Claude Code
// refuses auto mode to Haiku 4.5.
func TestModelRowsMapEachTierToAFirstPartyID(t *testing.T) {
	for _, tc := range []struct {
		name    string
		lineage claudecode.Tier
		want    string
	}{
		{name: "claude-tier-opus", lineage: claudecode.Opus, want: "claude-opus-5"},
		{name: "claude-tier-sonnet", lineage: claudecode.Sonnet, want: "claude-sonnet-5"},
		{name: "claude-tier-haiku", lineage: claudecode.Sonnet, want: "claude-sonnet-5"},
		{name: "claude-tier-fable", lineage: claudecode.Fable, want: "claude-fable-5-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := launch.ModelRows([]launch.LaunchModel{{Name: tc.name}})
			if len(rows) != 1 {
				t.Fatalf("built %d rows, want 1", len(rows))
			}
			if rows[0].BehavesAs != tc.want {
				t.Fatalf("behavesAs = %q, want %q", rows[0].BehavesAs, tc.want)
			}
			if !slices.Contains(claudecode.FamilyIDs[tc.lineage], rows[0].BehavesAs) {
				t.Fatalf("behavesAs %q is not in the %s lineage %v", rows[0].BehavesAs, tc.lineage, claudecode.FamilyIDs[tc.lineage])
			}
		})
	}
}
