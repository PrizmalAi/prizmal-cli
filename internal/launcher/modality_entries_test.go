package launch

import (
	"slices"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/model"
)

// visionModel is what the Switch reports for an entry listing "image".
func visionModel(name string) LaunchModel {
	return LaunchModel{Name: name, Capabilities: []model.Capability{model.CapabilityCompletion, model.CapabilityVision}}
}

// textOnlyModel is what the Switch reports for an entry listing only "text".
func textOnlyModel(name string) LaunchModel {
	return LaunchModel{Name: name, Capabilities: []model.Capability{model.CapabilityCompletion}}
}

// fileModel is what the Switch reports for an entry listing "file", its
// document modality. The Switch's catalogue gives a row "file" and "image"
// independently, so a file-without-image row is a shape that has to work.
func fileModel(name string) LaunchModel {
	return LaunchModel{Name: name, Capabilities: []model.Capability{model.CapabilityCompletion, model.CapabilityDocument}}
}

// fileAndVisionModel is the catalogue's common document row: "file", "image"
// and "text" together, as every gpt-5.6-* and claude-* row carries them.
func fileAndVisionModel(name string) LaunchModel {
	return LaunchModel{Name: name, Capabilities: []model.Capability{model.CapabilityCompletion, model.CapabilityDocument, model.CapabilityVision}}
}

// A model the Switch says takes images declares image, and only image. The
// Switch spells its document modality "file" and gives it out independently of
// "image", so image no longer stands in for PDF support.
func TestOpenCodeVisionModelDeclaresImageNotPDF(t *testing.T) {
	input := opencodeInputModalities(t, opencodeModelEntry(t, visionModel("sees-things")))
	if !slices.Equal(input, []string{"text", "image"}) {
		t.Fatalf("modalities.input = %v, want [text image]", input)
	}
}

// A model the Switch says takes "file" declares pdf. OpenCode only forwards an
// attached PDF when the entry declares the modality.
func TestOpenCodeFileModelDeclaresPDF(t *testing.T) {
	input := opencodeInputModalities(t, opencodeModelEntry(t, fileModel("reads-documents")))
	if !slices.Equal(input, []string{"text", "pdf"}) {
		t.Fatalf("modalities.input = %v, want [text pdf]", input)
	}
}

// The catalogue's [file, image, text] row declares all three.
func TestOpenCodeFileAndVisionModelDeclaresBoth(t *testing.T) {
	input := opencodeInputModalities(t, opencodeModelEntry(t, fileAndVisionModel("reads-everything")))
	if !slices.Equal(input, []string{"text", "image", "pdf"}) {
		t.Fatalf("modalities.input = %v, want [text image pdf]", input)
	}
}

// A model the Switch says is text-only declares text only. Declaring image on
// an entry the Switch reports as text-only is the over-declaration the
// capability work exists to remove.
func TestOpenCodeTextOnlyModelDeclaresTextOnly(t *testing.T) {
	input := opencodeInputModalities(t, opencodeModelEntry(t, textOnlyModel("words-only")))
	if !slices.Equal(input, []string{"text"}) {
		t.Fatalf("modalities.input = %v, want [text]", input)
	}
}

// A model with no capability data at all is unknown, not text-only: it keeps
// today's permissive list so attachments are not newly blocked. This is the
// same assertion as TestOpenCodeDeclaresPDFInputModality, kept here to say out
// loud that "no capabilities" is the unknown case rather than an oversight.
func TestOpenCodeUnknownModelStaysPermissive(t *testing.T) {
	input := opencodeInputModalities(t, opencodeModelEntry(t, LaunchModel{Name: "unknown-model"}))
	if !slices.Equal(input, []string{"text", "image", "pdf"}) {
		t.Fatalf("modalities.input = %v, want [text image pdf]", input)
	}
}

// codexInputModalities pulls input_modalities off a codex catalog entry.
func codexInputModalities(t *testing.T, m LaunchModel) []string {
	t.Helper()
	entry := buildCodexModelEntry(m)
	got, ok := entry["input_modalities"].([]string)
	if !ok {
		t.Fatalf("codex entry lacks input_modalities: %v", entry["input_modalities"])
	}
	return got
}

func TestCodexVisionModelDeclaresImage(t *testing.T) {
	if got := codexInputModalities(t, visionModel("sees-things")); !slices.Equal(got, []string{"text", "image"}) {
		t.Fatalf("input_modalities = %v, want [text image]", got)
	}
}

// Codex already gated image on vision, so a text-only and an unknown model
// both keep the text-only list they get today.
func TestCodexNonVisionModelsDeclareTextOnly(t *testing.T) {
	for _, m := range []LaunchModel{textOnlyModel("words-only"), {Name: "unknown-model"}} {
		if got := codexInputModalities(t, m); !slices.Equal(got, []string{"text"}) {
			t.Fatalf("input_modalities for %q = %v, want [text]", m.Name, got)
		}
	}
}

// piInput pulls the input list off a pi model config.
func piInput(t *testing.T, m LaunchModel) []string {
	t.Helper()
	cfg := createConfig(m)
	got, ok := cfg["input"].([]string)
	if !ok {
		t.Fatalf("pi config lacks an input list: %v", cfg["input"])
	}
	return got
}

func TestPiVisionModelDeclaresImage(t *testing.T) {
	if got := piInput(t, visionModel("sees-things")); !slices.Equal(got, []string{"text", "image"}) {
		t.Fatalf("input = %v, want [text image]", got)
	}
}

func TestPiNonVisionModelsDeclareTextOnly(t *testing.T) {
	for _, m := range []LaunchModel{textOnlyModel("words-only"), {Name: "unknown-model"}} {
		if got := piInput(t, m); !slices.Equal(got, []string{"text"}) {
			t.Fatalf("input for %q = %v, want [text]", m.Name, got)
		}
	}
}

// declaredInputsFor routes a Switch input_modalities array through the
// vocabulary the way the catalog does, then through one harness's writer, so a
// row covers the whole translation: what the Switch said, what it became, and
// what the harness is told.
func declaredInputsFor(t *testing.T, m LaunchModel, harness string) []string {
	t.Helper()
	switch harness {
	case harnessCodex:
		return codexInputModalities(t, m)
	case harnessOpenCode:
		return opencodeInputModalities(t, opencodeModelEntry(t, m))
	case harnessPi:
		return piInput(t, m)
	default:
		t.Fatalf("no writer for harness %q", harness)
		return nil
	}
}

// The translation is one table: each input_modalities array the Switch serves
// goes through the catalog and out through all three writers at once. A
// modality added to the vocabulary without a harness to name it in fails here
// rather than in one adapter's test.
//
// The empty row is the unknown case, not text-only, and it is the row where the
// three postures differ: opencode stays permissive so an attachment is not
// newly refused, codex and pi declare text alone. "video" is served by the
// Switch and dropped by the vocabulary, so it reads as the text-only row.
func TestEveryHarnessDeclaresTheVocabularyRowForACatalogEntry(t *testing.T) {
	for _, tc := range []struct {
		name       string
		modalities []string
		capable    []model.Capability
		codex      []string
		openCode   []string
		pi         []string
	}{
		{
			name:       "text-only",
			modalities: []string{"text"},
			capable:    []model.Capability{model.CapabilityCompletion},
			codex:      []string{"text"},
			openCode:   []string{"text"},
			pi:         []string{"text"},
		},
		{
			name:       "image",
			modalities: []string{"text", "image"},
			capable:    []model.Capability{model.CapabilityCompletion, model.CapabilityVision},
			codex:      []string{"text", "image"},
			openCode:   []string{"text", "image"},
			pi:         []string{"text", "image"},
		},
		{
			// Document and image are separate signals on both sides, so the
			// document row declares nothing but text and pdf: image does not
			// stand in for a PDF.
			name:       "file",
			modalities: []string{"text", "file"},
			capable:    []model.Capability{model.CapabilityCompletion, model.CapabilityDocument},
			codex:      []string{"text"},
			openCode:   []string{"text", "pdf"},
			pi:         []string{"text"},
		},
		{
			// The catalogue's common row, and the one that used to be
			// reachable only through opencode's own branch.
			name:       "file and image",
			modalities: []string{"text", "file", "image"},
			capable:    []model.Capability{model.CapabilityCompletion, model.CapabilityDocument, model.CapabilityVision},
			codex:      []string{"text", "image"},
			openCode:   []string{"text", "image", "pdf"},
			pi:         []string{"text", "image"},
		},
		{
			// Audio travels and is declared nowhere: no harness has an audio
			// input modality, so the capability must not turn one into text
			// plus a key the schema does not have.
			name:       "audio",
			modalities: []string{"text", "audio"},
			capable:    []model.Capability{model.CapabilityCompletion, model.CapabilityAudio},
			codex:      []string{"text"},
			openCode:   []string{"text"},
			pi:         []string{"text"},
		},
		{
			name:       "unknown",
			modalities: nil,
			capable:    nil,
			codex:      []string{"text"},
			openCode:   []string{"text", "image", "pdf"},
			pi:         []string{"text"},
		},
		{
			name:       "unrecognised modality dropped",
			modalities: []string{"text", "video"},
			capable:    []model.Capability{model.CapabilityCompletion},
			codex:      []string{"text"},
			openCode:   []string{"text"},
			pi:         []string{"text"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capabilities := capabilitiesFromModalities(tc.modalities)
			if !slices.Equal(capabilities, tc.capable) {
				t.Fatalf("capabilitiesFromModalities(%v) = %v, want %v", tc.modalities, capabilities, tc.capable)
			}
			m := LaunchModel{Name: "router-config", Capabilities: capabilities}
			for harness, want := range map[string][]string{
				harnessCodex:    tc.codex,
				harnessOpenCode: tc.openCode,
				harnessPi:       tc.pi,
			} {
				if got := declaredInputsFor(t, m, harness); !slices.Equal(got, want) {
					t.Fatalf("%s declared inputs = %v, want %v", harness, got, want)
				}
			}
		})
	}
}

// A caller that builds a model by hand, knowing about images and nothing else,
// still gets text declared. The unconditional row exists for that list, which
// carries no CapabilityCompletion, so a harness is never handed an entry that
// declares no input at all.
func TestDeclaredInputsAlwaysIncludeText(t *testing.T) {
	m := LaunchModel{Name: "hand-built", Capabilities: []model.Capability{model.CapabilityVision}}
	for _, harness := range []string{harnessCodex, harnessOpenCode, harnessPi} {
		if got := declaredInputs(m, harness); !slices.Contains(got, "text") {
			t.Fatalf("%s declared inputs = %v, want them to include text", harness, got)
		}
	}
}

// The unknown lists are per-harness policy, so each harness needs an entry: a
// new harness that asks the vocabulary for its unknown case and finds none gets
// an empty input list, which is a config that declares nothing at all.
func TestEveryHarnessHasAnUnknownInputList(t *testing.T) {
	for _, harness := range []string{harnessCodex, harnessOpenCode, harnessPi} {
		unknown, ok := unknownInputs[harness]
		if !ok {
			t.Fatalf("no unknown-input list for %s", harness)
		}
		if len(unknown) == 0 {
			t.Fatalf("%s unknown-input list is empty, want at least text", harness)
		}
		if got := declaredInputs(LaunchModel{Name: "unknown-model"}, harness); !slices.Equal(got, unknown) {
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
		harnessCodex: true, harnessOpenCode: true, harnessPi: true,
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

// CapabilityThinking has no row, on purpose: opencode.go and pi.go both gate a
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
	for _, capability := range capabilitiesFromModalities(modalities) {
		if capability == model.CapabilityThinking {
			t.Fatalf("capabilitiesFromModalities(%v) produced CapabilityThinking", modalities)
		}
	}
}

// The declared list is a fresh slice each time, so a caller that appends to it
// cannot edit the shared unknown list that every later launch reads.
func TestDeclaredInputsDoNotAliasTheUnknownList(t *testing.T) {
	first := declaredInputs(LaunchModel{Name: "unknown-model"}, harnessOpenCode)
	first[0] = "mutated"
	if second := declaredInputs(LaunchModel{Name: "unknown-model"}, harnessOpenCode); !slices.Equal(second, unknownInputs[harnessOpenCode]) {
		t.Fatalf("declared inputs = %v, want the untouched %v", second, unknownInputs[harnessOpenCode])
	}
}
