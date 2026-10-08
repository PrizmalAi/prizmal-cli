// Package launchtest holds test-only helpers for the harness packages under
// internal/launcher. It imports the launcher core, so the core's own tests
// cannot use it.
package launchtest

import (
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/PrizmalAi/prizmal-cli/internal/model"
)

// WireKeyTestModel is a minimal launch model for tests that check how a
// harness wires the switch key.
func WireKeyTestModel() []launch.LaunchModel {
	return []launch.LaunchModel{{Name: "wire-test-model"}}
}

// VisionModel is what the Switch reports for an entry listing "image".
func VisionModel(name string) launch.LaunchModel {
	return launch.LaunchModel{Name: name, Capabilities: []model.Capability{model.CapabilityCompletion, model.CapabilityVision}}
}

// TextOnlyModel is what the Switch reports for an entry listing only "text".
func TextOnlyModel(name string) launch.LaunchModel {
	return launch.LaunchModel{Name: name, Capabilities: []model.Capability{model.CapabilityCompletion}}
}

// FileModel is what the Switch reports for an entry listing "file", its
// document modality. The Switch's catalogue gives a row "file" and "image"
// independently, so a file-without-image row is a shape that has to work.
func FileModel(name string) launch.LaunchModel {
	return launch.LaunchModel{Name: name, Capabilities: []model.Capability{model.CapabilityCompletion, model.CapabilityDocument}}
}

// FileAndVisionModel is the catalogue's common document row: "file", "image"
// and "text" together, as every gpt-5.6-* and claude-* row carries them.
func FileAndVisionModel(name string) launch.LaunchModel {
	return launch.LaunchModel{Name: name, Capabilities: []model.Capability{model.CapabilityCompletion, model.CapabilityDocument, model.CapabilityVision}}
}
