package launch

import (
	"slices"
	"strings"

	"github.com/PrizmalAi/prizmal-cli/internal/model"
)

// This file is the capability vocabulary: the one place the Switch's
// input-modality names are translated into model.Capability and back into the
// names each harness's config uses. It was moved out of switchmodels.go so
// that the forward map and the reverse map cannot drift apart, and so that
// adding a modality is an edit here rather than an edit in every config
// writer. The Switch fetch, the [1m] decoration and the tier fold stay in
// switchmodels.go.

// harness names one config writer in the vocabulary. It is a plain string so a
// row of the table names a writer and nothing else: a harness contributes its
// own spelling of a capability, never its own decision about when to declare
// it.
const (
	harnessCodex    = "codex"
	harnessOpenCode = "opencode"
	harnessPi       = "pi"
)

// inputCapability is one input modality in the vocabulary. The row carries
// both directions at once, so a modality that gains a name in one harness
// cannot be added without that harness being written down here.
type inputCapability struct {
	// switchModality is the key the Switch spells this modality under in an
	// entry's input_modalities array.
	switchModality string

	// capability is what that modality means in the launcher's vocabulary.
	capability model.Capability

	// harnessNames maps a harness to the key that harness declares the same
	// input under. A harness absent from the map has nowhere to declare it,
	// which is a fact about that harness and not an omission to fix.
	harnessNames map[string]string

	// always is set on the modality every harness declares whatever the
	// capabilities say. "text" is that modality: it is unconditional here
	// because a list of capabilities built by hand — a test, or a caller that
	// knows about images and nothing else — need not carry
	// CapabilityCompletion, and an entry that declares no modality at all is
	// a worse answer than one that declares text.
	always bool
}

// inputVocabulary is the Switch's input-modality vocabulary, mapped to the
// launcher's and on to each harness's own spelling.
//
// "text" maps to CapabilityCompletion so that a model the Switch calls
// text-only still comes back with a non-empty capability list. That is what
// separates "the Switch says text-only" from "the Switch said nothing": the
// second leaves the list empty, and every entry builder reads an empty list as
// unknown. Nothing else in the tree reads CapabilityCompletion, so the mapping
// carries no behavior of its own — and because text is also the unconditional
// row, no harness has to declare a text modality it was not told about.
//
// "file" is the Switch's document modality, the one that says a router config
// can take a PDF. It is handed out independently of "image": the catalogue has
// rows with both, and rows with image and no file. OpenCode declares it as
// "pdf"; codex and pi have no document modality at all and are absent from
// that row's names, so the capability reaches them and has nowhere to land.
//
// "audio" is mapped so the Switch's audio rows are not mistaken for
// text-only, but no harness declares an audio input modality today, so it names
// no harness. That is the whole of it: the capability travels and the
// declaration does not exist yet.
//
// "video" is served by the Switch and deliberately missing here: it has no
// capability in the vocabulary and no entry builder has anywhere to put it.
var inputVocabulary = []inputCapability{
	{
		switchModality: "text",
		capability:     model.CapabilityCompletion,
		harnessNames: map[string]string{
			harnessCodex:    "text",
			harnessOpenCode: "text",
			harnessPi:       "text",
		},
		always: true,
	},
	{
		switchModality: "image",
		capability:     model.CapabilityVision,
		harnessNames: map[string]string{
			harnessCodex:    "image",
			harnessOpenCode: "image",
			harnessPi:       "image",
		},
	},
	{
		switchModality: "file",
		capability:     model.CapabilityDocument,
		harnessNames: map[string]string{
			harnessOpenCode: "pdf",
		},
	},
	{
		switchModality: "audio",
		capability:     model.CapabilityAudio,
		harnessNames:   nil,
	},
}

// unknownInputs is what a harness declares for a model with no capabilities at
// all, which is unknown rather than text-only.
//
// The Switch omits input_modalities on some entries and the fetch is skipped
// outright for an unauthenticated launch, so unknown is the common case and
// the posture is a per-harness decision rather than one rule.
//
// OpenCode only forwards an attached image or PDF when the entry declares the
// matching input modality; otherwise it writes a refusal into the prompt and
// the model answers without the document. So an unknown model keeps the
// permissive list that entry has always declared, and a known text-only model
// gets the narrow one — over-declaring is the safe direction for opencode and
// only opencode.
//
// codex and pi declare text alone when the model is unknown. That is what they
// declare for a text-only model, and narrowing it further is what keeping the
// list unchanged here means.
var unknownInputs = map[string][]string{
	harnessCodex:    {"text"},
	harnessOpenCode: {"text", "image", "pdf"},
	harnessPi:       {"text"},
}

// modalityCapabilities is the forward half of the vocabulary, derived from
// inputVocabulary so the two directions cannot disagree about what a Switch
// modality means. Built at init rather than written out so a row added to the
// table is a row the reader of input_modalities can resolve.
var modalityCapabilities = func() map[string]model.Capability {
	capabilities := make(map[string]model.Capability, len(inputVocabulary))
	for _, entry := range inputVocabulary {
		capabilities[entry.switchModality] = entry.capability
	}
	return capabilities
}()

// capabilitiesFromModalities translates one entry's input_modalities array.
// An unrecognised modality is dropped rather than guessed at.
func capabilitiesFromModalities(modalities []string) []model.Capability {
	var capabilities []model.Capability
	for _, modality := range modalities {
		c, ok := modalityCapabilities[strings.ToLower(strings.TrimSpace(modality))]
		if !ok || slices.Contains(capabilities, c) {
			continue
		}
		capabilities = append(capabilities, c)
	}
	return capabilities
}

// declaredInputs returns the input modalities one harness declares for a model,
// in vocabulary order.
//
// A model with no capabilities is unknown, not text-only, and gets the
// per-harness unknown list. Otherwise each modality the model has is declared
// under that harness's own spelling, and a modality the harness does not name
// is skipped: declaring a key that harness's schema does not have, or has not
// grown, is how a correct capability turns into a broken config.
//
// The result is a fresh slice, so a caller that appends to it cannot edit the
// unknown list every later launch would read.
func declaredInputs(m LaunchModel, harness string) []string {
	if len(m.Capabilities) == 0 {
		return slices.Clone(unknownInputs[harness])
	}
	var declared []string
	for _, entry := range inputVocabulary {
		name, ok := entry.harnessNames[harness]
		if !ok || !entry.always && !m.HasCapability(entry.capability) {
			continue
		}
		declared = append(declared, name)
	}
	return declared
}

// CapabilityThinking is deliberately absent from the vocabulary above, and its
// absence is a question this file does not answer on its own.
//
// opencode.go and pi.go both gate a reasoning field on
// HasCapability(CapabilityThinking), and nothing in the tree can put that
// capability in a model's list: capabilitiesFromModalities is the only producer
// of LaunchModel.Capabilities and no input modality maps to it. The Switch
// reports reasoning as its own per-entry field, named in switchCatalogEntry's
// comment and deliberately not read — it is a separate boolean beside
// input_modalities, not a modality, and a reasoning trace is output rather than
// something the model takes. Nothing in the catalogue the stub server serves
// carries a reasoning modality, so no row here invents one.
//
// A vocabulary row is the place to put the answer if the Switch is ever seen
// to report reasoning inside input_modalities, or the entry's reasoning field
// is decided on. Until then the two gates stay unreachable rather than
// silently true: a model that cannot report reasoning gets no reasoning
// variants, which is the answer a harness needs to hear.
