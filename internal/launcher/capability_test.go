package launch

import (
	"slices"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/model"
)

// A caller that builds a model by hand, knowing about images and nothing else,
// still gets text declared. The unconditional row exists for that list, which
// carries no CapabilityCompletion, so a harness is never handed an entry that
// declares no input at all.
func TestDeclaredInputsAlwaysIncludeText(t *testing.T) {
	m := LaunchModel{Name: "hand-built", Capabilities: []model.Capability{model.CapabilityVision}}
	for _, harness := range []string{HarnessCodex, HarnessOpenCode, HarnessPi} {
		if got := DeclaredInputs(m, harness); !slices.Contains(got, "text") {
			t.Fatalf("%s declared inputs = %v, want them to include text", harness, got)
		}
	}
}

// The unknown lists are per-harness policy, so each harness needs an entry: a
// new harness that asks the vocabulary for its unknown case and finds none gets
// an empty input list, which is a config that declares nothing at all.
func TestEveryHarnessHasAnUnknownInputList(t *testing.T) {
	for _, harness := range []string{HarnessCodex, HarnessOpenCode, HarnessPi} {
		unknown, ok := unknownInputs[harness]
		if !ok {
			t.Fatalf("no unknown-input list for %s", harness)
		}
		if len(unknown) == 0 {
			t.Fatalf("%s unknown-input list is empty, want at least text", harness)
		}
		if got := DeclaredInputs(LaunchModel{Name: "unknown-model"}, harness); !slices.Equal(got, unknown) {
			t.Fatalf("%s declared inputs for an unknown model = %v, want %v", harness, got, unknown)
		}
	}
}

// The forward map is derived from the table, so every row resolves to its own
// capability and no modality is claimed twice. A row added with a modality the
// Switch already spells would silently drop one of the two meanings.
func TestForwardMapIsDerivedFromTheVocabulary(t *testing.T) {
	seen := map[string]bool{}
	for _, entry := range inputVocabulary {
		if entry.switchModality == "" {
			t.Fatalf("vocabulary row %q has no Switch modality", entry.capability)
		}
		if seen[entry.switchModality] {
			t.Fatalf("modality %q has two vocabulary rows", entry.switchModality)
		}
		seen[entry.switchModality] = true
		if got := modalityCapabilities[entry.switchModality]; got != entry.capability {
			t.Fatalf("modality %q maps to %q, want %q", entry.switchModality, got, entry.capability)
		}
	}
	if len(modalityCapabilities) != len(inputVocabulary) {
		t.Fatalf("forward map has %d keys for %d vocabulary rows", len(modalityCapabilities), len(inputVocabulary))
	}
}

// Every capability a writer gates its inputs on is a row in the vocabulary, and
// every harness a row names has an unknown list. A capability gained by a
// writer without a row here means an adapter translating on its own again, and
// a harness named in a row with no unknown list means that harness asks the
// vocabulary a question it cannot answer.
//
// The gates listed are the ones the writers read: vision and document decide
// what each of them declares beyond text. Reasoning is deliberately not in this
// list, and TestThinkingIsNotAnInputModality says why.
func TestEveryWriterGateHasAVocabularyRow(t *testing.T) {
	known := map[string]bool{
		HarnessCodex: true, HarnessOpenCode: true, HarnessPi: true,
	}
	gated := map[model.Capability]bool{
		model.CapabilityVision:   false,
		model.CapabilityDocument: false,
	}
	for _, entry := range inputVocabulary {
		for harness := range entry.harnessNames {
			if !known[harness] {
				t.Fatalf("vocabulary row %q names unknown harness %q", entry.switchModality, harness)
			}
		}
		if _, ok := gated[entry.capability]; ok {
			gated[entry.capability] = true
		}
	}
	for capability, found := range gated {
		if !found {
			t.Fatalf("a writer gates its inputs on %q, which has no vocabulary row", capability)
		}
	}
}

// CapabilityThinking has no row, on purpose: opencode and pi both gate a
// reasoning field on it, and no modality the Switch is known to send produces
// it. The Switch reports reasoning as its own per-entry field, which
// switchCatalogEntry's comment names and deliberately does not read, so there is
// no evidence for mapping one. This test states that absence, so the day
// someone maps a reasoning modality it has to be deleted here deliberately.
func TestThinkingIsNotAnInputModality(t *testing.T) {
	for _, entry := range inputVocabulary {
		if entry.capability == model.CapabilityThinking {
			t.Fatalf("vocabulary row %q claims CapabilityThinking", entry.switchModality)
		}
	}
	modalities := []string{"text", "image", "file", "audio", "video", "reasoning", "thinking", "output_text"}
	for _, capability := range CapabilitiesFromModalities(modalities) {
		if capability == model.CapabilityThinking {
			t.Fatalf("CapabilitiesFromModalities(%v) produced CapabilityThinking", modalities)
		}
	}
}

// The declared list is a fresh slice each time, so a caller that appends to it
// cannot edit the shared unknown list that every later launch reads.
func TestDeclaredInputsDoNotAliasTheUnknownList(t *testing.T) {
	first := DeclaredInputs(LaunchModel{Name: "unknown-model"}, HarnessOpenCode)
	first[0] = "mutated"
	if second := DeclaredInputs(LaunchModel{Name: "unknown-model"}, HarnessOpenCode); !slices.Equal(second, unknownInputs[HarnessOpenCode]) {
		t.Fatalf("declared inputs = %v, want the untouched %v", second, unknownInputs[HarnessOpenCode])
	}
}
