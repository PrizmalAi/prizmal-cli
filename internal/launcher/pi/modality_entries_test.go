package pi

import (
	"slices"
	"testing"

	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/launchtest"
)

// piInput pulls the input list off a pi model config.
func piInput(t *testing.T, m launch.LaunchModel) []string {
	t.Helper()
	cfg := createConfig(m)
	got, ok := cfg["input"].([]string)
	if !ok {
		t.Fatalf("pi config lacks an input list: %v", cfg["input"])
	}
	return got
}

func TestPiVisionModelDeclaresImage(t *testing.T) {
	if got := piInput(t, launchtest.VisionModel("sees-things")); !slices.Equal(got, []string{"text", "image"}) {
		t.Fatalf("input = %v, want [text image]", got)
	}
}

func TestPiNonVisionModelsDeclareTextOnly(t *testing.T) {
	for _, m := range []launch.LaunchModel{launchtest.TextOnlyModel("words-only"), {Name: "unknown-model"}} {
		if got := piInput(t, m); !slices.Equal(got, []string{"text"}) {
			t.Fatalf("input for %q = %v, want [text]", m.Name, got)
		}
	}
}

// TestPiDeclaresTheVocabularyRowForACatalogEntry walks every shape the Switch's
// input_modalities array takes through the catalog and out through pi's writer.
func TestPiDeclaresTheVocabularyRowForACatalogEntry(t *testing.T) {
	for _, row := range launchtest.VocabularyRows {
		t.Run(row.Name, func(t *testing.T) {
			m := launch.LaunchModel{Name: "router-config", Capabilities: row.Capabilities}
			if got := piInput(t, m); !slices.Equal(got, row.Pi) {
				t.Fatalf("pi declared inputs = %v, want %v", got, row.Pi)
			}
		})
	}
}
