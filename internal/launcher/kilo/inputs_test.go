package kilo

import (
	"slices"
	"testing"

	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/PrizmalAi/prizmal-cli/internal/model"
)

// A harness that launches models with known modalities must declare names for
// them: an empty declaration writes a config with no input at all.
func TestInputsAreDeclared(t *testing.T) {
	if err := Inputs.Validate(); err != nil {
		t.Fatalf("kilo %v", err)
	}
}

func TestInputsPerModel(t *testing.T) {
	for _, tc := range []struct {
		name string
		caps []model.Capability
		want []string
	}{
		{"unknown", nil, []string{"text", "image"}},
		{"text-only", []model.Capability{model.CapabilityCompletion}, []string{"text"}},
		{"vision", []model.Capability{model.CapabilityVision}, []string{"text", "image"}},
		{"document", []model.Capability{model.CapabilityDocument}, []string{"text", "pdf"}},
		{"vision and document", []model.Capability{model.CapabilityVision, model.CapabilityDocument}, []string{"text", "image", "pdf"}},
	} {
		got := launch.DeclaredInputs(launch.LaunchModel{Name: tc.name, Capabilities: tc.caps}, Inputs)
		if !slices.Equal(got, tc.want) {
			t.Fatalf("%s declared inputs = %v, want %v", tc.name, got, tc.want)
		}
	}
}
