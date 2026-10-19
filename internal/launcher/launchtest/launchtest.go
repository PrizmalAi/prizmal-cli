// Package launchtest holds test-only helpers for the harness packages under
// internal/launcher. It imports the launcher core, so the core's own tests
// cannot use it.
package launchtest

import (
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/PrizmalAi/prizmal-cli/internal/model"
	"strings"
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

// EnvValue returns the value of the variable named by prefix in env, or "".
func EnvValue(env []string, prefix string) string {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, prefix); ok {
			return v
		}
	}
	return ""
}

// EnvNames returns every variable name in an environment.
func EnvNames(env []string) []string {
	names := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		names = append(names, name)
	}
	return names
}

// WithDeviceMode turns device mode on for one test and restores the previous
// state, so a launch test cannot leak the mode into the next one.
func WithDeviceMode(t *testing.T) {
	t.Helper()
	previous := envconfig.DeviceMode()
	envconfig.SetDeviceMode(true)
	envconfig.SetDeviceToken("pz-d-dt-test-token")
	t.Cleanup(func() {
		envconfig.SetDeviceMode(previous)
		envconfig.SetDeviceToken("")
	})
}

// StubEndpoint is an httptest server's URL as an endpoint shape spells it.
// httptest binds 127.0.0.1 and the Switch endpoint is always handed out under
// the name a client dials, so a wiring assertion compares against the rewritten
// form. It is spelled out here instead of read from envconfig so that asserting
// the shape does not restate the function under test.
func StubEndpoint(raw string) string {
	return strings.Replace(raw, "127.0.0.1", "localhost", 1)
}
