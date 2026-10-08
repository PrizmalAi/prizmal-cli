package codex

import (
	"slices"
	"testing"

	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/launchtest"
)

// codexInputModalities pulls input_modalities off a codex catalog entry.
func codexInputModalities(t *testing.T, m launch.LaunchModel) []string {
	t.Helper()
	entry := buildCodexModelEntry(m)
	got, ok := entry["input_modalities"].([]string)
	if !ok {
		t.Fatalf("codex entry lacks input_modalities: %v", entry["input_modalities"])
	}
	return got
}

func TestCodexVisionModelDeclaresImage(t *testing.T) {
	if got := codexInputModalities(t, launchtest.VisionModel("sees-things")); !slices.Equal(got, []string{"text", "image"}) {
		t.Fatalf("input_modalities = %v, want [text image]", got)
	}
}

// Codex already gated image on vision, so a text-only and an unknown model
// both keep the text-only list they get today.
func TestCodexNonVisionModelsDeclareTextOnly(t *testing.T) {
	for _, m := range []launch.LaunchModel{launchtest.TextOnlyModel("words-only"), {Name: "unknown-model"}} {
		if got := codexInputModalities(t, m); !slices.Equal(got, []string{"text"}) {
			t.Fatalf("input_modalities for %q = %v, want [text]", m.Name, got)
		}
	}
}
