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
