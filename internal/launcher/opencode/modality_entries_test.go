package opencode

import (
	"slices"
	"testing"

	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/launchtest"
)

// A model the Switch says takes images declares image, and only image. The
// Switch spells its document modality "file" and gives it out independently of
// "image", so image no longer stands in for PDF support.
func TestOpenCodeVisionModelDeclaresImageNotPDF(t *testing.T) {
	input := opencodeInputModalities(t, opencodeModelEntry(t, launchtest.VisionModel("sees-things")))
	if !slices.Equal(input, []string{"text", "image"}) {
		t.Fatalf("modalities.input = %v, want [text image]", input)
	}
}

// A model the Switch says takes "file" declares pdf. OpenCode only forwards an
// attached PDF when the entry declares the modality.
func TestOpenCodeFileModelDeclaresPDF(t *testing.T) {
	input := opencodeInputModalities(t, opencodeModelEntry(t, launchtest.FileModel("reads-documents")))
	if !slices.Equal(input, []string{"text", "pdf"}) {
		t.Fatalf("modalities.input = %v, want [text pdf]", input)
	}
}

// The catalogue's [file, image, text] row declares all three.
func TestOpenCodeFileAndVisionModelDeclaresBoth(t *testing.T) {
	input := opencodeInputModalities(t, opencodeModelEntry(t, launchtest.FileAndVisionModel("reads-everything")))
	if !slices.Equal(input, []string{"text", "image", "pdf"}) {
		t.Fatalf("modalities.input = %v, want [text image pdf]", input)
	}
}

// A model the Switch says is text-only declares text only. Declaring image on
// an entry the Switch reports as text-only is the over-declaration the
// capability work exists to remove.
func TestOpenCodeTextOnlyModelDeclaresTextOnly(t *testing.T) {
	input := opencodeInputModalities(t, opencodeModelEntry(t, launchtest.TextOnlyModel("words-only")))
	if !slices.Equal(input, []string{"text"}) {
		t.Fatalf("modalities.input = %v, want [text]", input)
	}
}

// A model with no capability data at all is unknown, not text-only: it keeps
// today's permissive list so attachments are not newly blocked. This is the
// same assertion as TestOpenCodeDeclaresPDFInputModality, kept here to say out
// loud that "no capabilities" is the unknown case rather than an oversight.
func TestOpenCodeUnknownModelStaysPermissive(t *testing.T) {
	input := opencodeInputModalities(t, opencodeModelEntry(t, launch.LaunchModel{Name: "unknown-model"}))
	if !slices.Equal(input, []string{"text", "image", "pdf"}) {
		t.Fatalf("modalities.input = %v, want [text image pdf]", input)
	}
}
