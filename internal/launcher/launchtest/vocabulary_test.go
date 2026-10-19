package launchtest

import (
	"slices"
	"testing"

	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

// TestVocabularyRowsMatchTheLaunchersVocabulary pins the table every harness
// package checks its column of. Each row's Switch array must translate to the
// capabilities the row declares, through the same function the catalog uses, so
// a harness test that disagrees with a row disagrees with the catalog rather
// than with this table's copy of it.
func TestVocabularyRowsMatchTheLaunchersVocabulary(t *testing.T) {
	for _, row := range VocabularyRows {
		t.Run(row.Name, func(t *testing.T) {
			if got := launch.CapabilitiesFromModalities(row.Modalities); !slices.Equal(got, row.Capabilities) {
				t.Fatalf("CapabilitiesFromModalities(%v) = %v, want %v", row.Modalities, got, row.Capabilities)
			}
		})
	}
}
