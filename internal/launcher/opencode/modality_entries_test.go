package opencode

import (
	"slices"
	"strings"
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

// TestOpenCodeDeclaresTheVocabularyRowForACatalogEntry walks every shape the
// Switch's input_modalities array takes through the catalog and out through
// opencode's writer. The unknown row is opencode's own posture: permissive, so
// an attachment is not newly refused.
func TestOpenCodeDeclaresTheVocabularyRowForACatalogEntry(t *testing.T) {
	for _, row := range launchtest.VocabularyRows {
		t.Run(row.Name, func(t *testing.T) {
			m := launch.LaunchModel{Name: "router-config", Capabilities: row.Capabilities}
			if got := opencodeInputModalities(t, opencodeModelEntry(t, m)); !slices.Equal(got, row.OpenCode) {
				t.Fatalf("opencode declared inputs = %v, want %v", got, row.OpenCode)
			}
		})
	}
}

// TestInlineConfigPackageFollowsTheAPI pins the provider package each API
// selects, and that an unsupported API is refused.
func TestInlineConfigPackageFollowsTheAPI(t *testing.T) {
	for api, want := range map[string]string{"": "@ai-sdk/openai-compatible", "chat": "@ai-sdk/openai-compatible", "responses": "@ai-sdk/openai"} {
		raw, err := buildInlineConfigFor(api, launch.LaunchModel{Name: "m"}, []launch.LaunchModel{{Name: "m"}})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(raw, `"npm":"`+want+`"`) {
			t.Errorf("api %q: config %s lacks npm %s", api, raw, want)
		}
	}
	if err := (&OpenCode{}).SetAPI("anthropic"); err == nil {
		t.Error("SetAPI(anthropic) = nil, want a refusal")
	}
}
