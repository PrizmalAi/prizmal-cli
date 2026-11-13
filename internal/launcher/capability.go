package launch

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/PrizmalAi/prizmal-cli/internal/model"
)

// This file is the capability vocabulary: the one place the Switch's
// input-modality names are translated into model.Capability. Each harness
// declares its own names for them, and the inputs it assumes for an unknown
// model, in its own package as a HarnessInputs. The Switch fetch, the [1m]
// decoration and the tier fold stay in switchmodels.go.

// The Switch's input types, as a harness package spells them when it declares
// which of its own names each one maps to. A harness never invents a key of
// its own: the keys are the Switch's, and the values are the harness's.
const (
	InputText  = "text"
	InputImage = "image"
	InputFile  = "file"
	InputAudio = "audio"
)

// HarnessInputs is what one harness declares about its input modalities. It
// lives in the harness's own package, so a new harness adds a declaration there
// and never edits this file.
type HarnessInputs struct {
	// Names maps a Switch input type to the key the harness declares the same
	// input under. A type absent from the map has nowhere to land in that
	// harness's config, which is a fact about the harness and not an omission
	// to fix.
	Names map[string]string

	// Unknown is what the harness declares for a model with no capabilities at
	// all, which is unknown rather than text-only. The Switch omits
	// input_modalities on some entries and the fetch is skipped outright for an
	// unauthenticated launch, so unknown is the common case. The posture is a
	// per-harness decision: over-declaring is safe for some harnesses and wrong
	// for others. Every entry must be one of the harness's own Names values.
	Unknown []string
}

// Validate reports a declaration that would give a harness a config with no
// input at all: no names, no unknown list, or a name for a type the Switch does
// not define. A harness that launches models with known modalities must at
// least name text.
func (h HarnessInputs) Validate() error {
	if len(h.Names) == 0 {
		return errors.New("declares no input names")
	}
	if len(h.Unknown) == 0 {
		return errors.New("declares no unknown-input list")
	}
	for switchInput, name := range h.Names {
		if _, ok := modalityCapabilities[switchInput]; !ok {
			return fmt.Errorf("names Switch input type %q, which the Switch does not define", switchInput)
		}
		if name == "" {
			return fmt.Errorf("names Switch input type %q with an empty name", switchInput)
		}
	}
	if _, ok := h.Names[InputText]; !ok {
		return errors.New("declares no name for text")
	}
	if !slices.Contains(h.Unknown, h.Names[InputText]) {
		return errors.New("declares an unknown-input list without text")
	}
	for _, unknown := range h.Unknown {
		found := false
		for _, name := range h.Names {
			found = found || name == unknown
		}
		if !found {
			return fmt.Errorf("lists unknown input %q, which is not one of its names", unknown)
		}
	}
	return nil
}

// inputCapability is one input modality in the vocabulary: the Switch's name
// for it and the capability it means. Each harness's own name for it is
// declared by the harness, in HarnessInputs.
type inputCapability struct {
	// switchModality is the key the Switch spells this modality under in an
	// entry's input_modalities array.
	switchModality string

	// capability is what that modality means in the launcher's vocabulary.
	capability model.Capability

	// always is set on the modality every harness declares whatever the
	// capabilities say. "text" is that modality: it is unconditional here
	// because a list of capabilities built by hand — a test, or a caller that
	// knows about images and nothing else — need not carry
	// CapabilityCompletion, and an entry that declares no modality at all is
	// a worse answer than one that declares text.
	always bool
}

// inputVocabulary is the Switch's input-modality vocabulary, mapped to the
// launcher's.
//
// "text" maps to CapabilityCompletion so that a model the Switch calls
// text-only still comes back with a non-empty capability list. That is what
// separates "the Switch says text-only" from "the Switch said nothing": the
// second leaves the list empty, and every entry builder reads an empty list as
// unknown. Nothing else in the tree reads CapabilityCompletion, so the mapping
// carries no behavior of its own — and because text is also the unconditional
// row, no harness has to declare a text modality it was not told about.
//
// "file" is the Switch's document modality, the one that says a model
// can take a PDF. It is handed out independently of "image": the catalogue has
// rows with both, and rows with image and no file. A harness declares it
// under its own name, or names nothing for it, in which case the capability
// reaches that harness and has nowhere to land.
//
// "audio" is mapped so the Switch's audio rows are not mistaken for
// text-only, but no harness declares an audio input modality today. The
// capability travels and the declaration does not exist yet.
//
// "video" is served by the Switch and deliberately missing here: it has no
// capability in the vocabulary and no entry builder has anywhere to put it.
var inputVocabulary = []inputCapability{
	{
		switchModality: InputText,
		capability:     model.CapabilityCompletion,
		always:         true,
	},
	{
		switchModality: InputImage,
		capability:     model.CapabilityVision,
	},
	{
		switchModality: InputFile,
		capability:     model.CapabilityDocument,
	},
	{
		switchModality: InputAudio,
		capability:     model.CapabilityAudio,
	},
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

// CapabilitiesFromModalities translates one entry's input_modalities array.
// An unrecognised modality is dropped rather than guessed at.
func CapabilitiesFromModalities(modalities []string) []model.Capability {
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

// KnowsInputs says whether the model carries any input capability. Thinking is
// a separate boolean, so it does not make a model's inputs known.
func (m LaunchModel) KnowsInputs() bool {
	for _, c := range m.Capabilities {
		if c != model.CapabilityThinking {
			return true
		}
	}
	return false
}

// DeclaredInputs returns the input modalities one harness declares for a model,
// in vocabulary order.
//
// A model with no capabilities is unknown, not text-only, and gets the
// harness's unknown list. Otherwise each modality the model has is declared
// under the harness's own spelling, and a modality the harness does not name
// is skipped: declaring a key that harness's schema does not have, or has not
// grown, is how a correct capability turns into a broken config.
//
// The result is a fresh slice, so a caller that appends to it cannot edit the
// unknown list every later launch would read.
func DeclaredInputs(m LaunchModel, harness HarnessInputs) []string {
	if !m.KnowsInputs() {
		return slices.Clone(harness.Unknown)
	}
	var declared []string
	for _, entry := range inputVocabulary {
		name, ok := harness.Names[entry.switchModality]
		if !ok || !entry.always && !m.HasCapability(entry.capability) {
			continue
		}
		declared = append(declared, name)
	}
	return declared
}

// CapabilityThinking is not an input modality and has no row above. The Switch
// reports reasoning as its own per-entry boolean, and ParseSwitchCatalog turns
// a true value into CapabilityThinking, which harnesses gate their
// reasoning fields on. KnowsInputs ignores it, so a model that reports
// reasoning and no modalities still gets the unknown list.
