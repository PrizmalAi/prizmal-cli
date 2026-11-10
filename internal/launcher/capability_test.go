package launch

import (
	"slices"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/model"
)

// testHarness stands in for a harness package's declaration. The shared
// vocabulary names no harness, so its tests declare one of their own.
var testHarness = HarnessInputs{
	Names:   map[string]string{InputText: "text", InputImage: "picture", InputFile: "pdf"},
	Unknown: []string{"text", "picture"},
}

// A caller that builds a model by hand, knowing about images and nothing else,
// still gets text declared. The unconditional row exists for that list, which
// carries no CapabilityCompletion, so a harness is never handed an entry that
// declares no input at all.
func TestDeclaredInputsAlwaysIncludeText(t *testing.T) {
	m := LaunchModel{Name: "hand-built", Capabilities: []model.Capability{model.CapabilityVision}}
	if got := DeclaredInputs(m, testHarness); !slices.Equal(got, []string{"text", "picture"}) {
		t.Fatalf("declared inputs = %v, want text and the harness's name for image", got)
	}
}

// A harness that declares no input names, or no unknown list, would get a
// config that declares nothing at all, so the declaration is refused.
func TestHarnessInputsWithoutNamesAreInvalid(t *testing.T) {
	for name, h := range map[string]HarnessInputs{
		"zero value":      {},
		"no names":        {Unknown: []string{"text"}},
		"no unknown list": {Names: map[string]string{InputText: "text"}},
		"no text":         {Names: map[string]string{InputImage: "image"}, Unknown: []string{"image"}},
		"foreign type":    {Names: map[string]string{InputText: "text", "video": "video"}, Unknown: []string{"text"}},
		"empty name":      {Names: map[string]string{InputText: ""}, Unknown: []string{"text"}},
	} {
		if err := h.Validate(); err == nil {
			t.Fatalf("%s: Validate accepted %+v", name, h)
		}
	}
	if err := testHarness.Validate(); err != nil {
		t.Fatalf("Validate rejected a complete declaration: %v", err)
	}
}

// An unknown model gets the harness's unknown list, and a model that reports
// only reasoning is still unknown.
func TestUnknownModelGetsTheHarnessUnknownList(t *testing.T) {
	for _, m := range []LaunchModel{
		{Name: "unknown-model"},
		{Name: "bare", Capabilities: []model.Capability{model.CapabilityThinking}},
	} {
		if got := DeclaredInputs(m, testHarness); !slices.Equal(got, testHarness.Unknown) {
			t.Fatalf("%s declared inputs = %v, want %v", m.Name, got, testHarness.Unknown)
		}
	}
}

// A modality the harness does not name is skipped, so the capability reaches
// the harness and has nowhere to land.
func TestDeclaredInputsSkipAnUnnamedModality(t *testing.T) {
	h := HarnessInputs{Names: map[string]string{InputText: "text", InputImage: "image"}, Unknown: []string{"text"}}
	m := LaunchModel{Name: "doc", Capabilities: []model.Capability{model.CapabilityVision, model.CapabilityDocument, model.CapabilityAudio}}
	if got := DeclaredInputs(m, h); !slices.Equal(got, []string{"text", "image"}) {
		t.Fatalf("declared inputs = %v, want text and image", got)
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

// Every capability a writer gates its inputs on is a row in the vocabulary. A
// capability gained by a writer without a row here means an adapter translating
// on its own again.
//
// The gates listed are the ones the writers read: vision and document decide
// what each of them declares beyond text. Reasoning is deliberately not in this
// list, and TestThinkingIsNotAnInputModality says why.
func TestEveryWriterGateHasAVocabularyRow(t *testing.T) {
	gated := map[model.Capability]bool{
		model.CapabilityVision:   false,
		model.CapabilityDocument: false,
	}
	for _, entry := range inputVocabulary {
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

// CapabilityThinking has no row: it comes from the entry's reasoning boolean,
// not from an input modality, so no modality name may produce it.
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
// cannot edit the unknown list that every later launch reads.
func TestDeclaredInputsDoNotAliasTheUnknownList(t *testing.T) {
	first := DeclaredInputs(LaunchModel{Name: "unknown-model"}, testHarness)
	first[0] = "mutated"
	if second := DeclaredInputs(LaunchModel{Name: "unknown-model"}, testHarness); !slices.Equal(second, testHarness.Unknown) {
		t.Fatalf("declared inputs = %v, want the untouched %v", second, testHarness.Unknown)
	}
}
