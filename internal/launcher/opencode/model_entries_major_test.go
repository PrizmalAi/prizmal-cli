package opencode

import (
	"testing"

	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/launchtest"
	"github.com/PrizmalAi/prizmal-cli/internal/model"
)

// thinkingModel is a launch model that reports the thinking capability, the
// one case buildModelEntries treats specially on 1.x.
func thinkingModel(name string) launch.LaunchModel {
	m := launchtest.TextOnlyModel(name)
	m.Capabilities = append(m.Capabilities, model.CapabilityThinking)
	return m
}

// On 1.x a thinking model still gets reasoning + variants: this change must
// not touch the existing 1.x behavior.
func TestBuildModelEntriesOnV1KeepsReasoningFields(t *testing.T) {
	entries := buildModelEntries([]launch.LaunchModel{thinkingModel("thinks")}, openCodeMajor1)
	entry, ok := entries["thinks"].(map[string]any)
	if !ok {
		t.Fatalf("no entry for thinks: %v", entries)
	}
	if entry["reasoning"] != true {
		t.Errorf("1.x entry lost reasoning: %v", entry)
	}
	if _, ok := entry["variants"]; !ok {
		t.Errorf("1.x entry lost variants: %v", entry)
	}
	if _, ok := entry["tool_call"]; ok {
		t.Errorf("1.x entry gained tool_call, which only 2.x reads: %v", entry)
	}
}

// On 2.x, reasoning, temperature and attachment are dropped fields: a model
// entry must not send "reasoning" or "variants" there at all, and instead
// declares tool_call, the field OpenCode 2.x's config migration reads.
func TestBuildModelEntriesOnV2DropsReasoningAndSendsToolCall(t *testing.T) {
	entries := buildModelEntries([]launch.LaunchModel{thinkingModel("thinks")}, openCodeMajor2)
	entry, ok := entries["thinks"].(map[string]any)
	if !ok {
		t.Fatalf("no entry for thinks: %v", entries)
	}
	if _, ok := entry["reasoning"]; ok {
		t.Errorf("2.x entry still sends reasoning, which 2.x drops with a warning: %v", entry)
	}
	if _, ok := entry["variants"]; ok {
		t.Errorf("2.x entry still sends variants: %v", entry)
	}
	if entry["tool_call"] != true {
		t.Errorf("2.x entry lacks tool_call: %v", entry)
	}
	if _, ok := entry["modalities"]; !ok {
		t.Errorf("2.x entry lost modalities, which 2.x still reads: %v", entry)
	}
}

// ModelEntries backs Kilo, a 1.x-architecture fork, and must keep the 1.x
// shape regardless of whatever major this launcher ever learns about.
func TestExportedModelEntriesStaysOnTheV1Shape(t *testing.T) {
	entries := ModelEntries([]launch.LaunchModel{thinkingModel("thinks")})
	entry, ok := entries["thinks"].(map[string]any)
	if !ok {
		t.Fatalf("no entry for thinks: %v", entries)
	}
	if entry["reasoning"] != true {
		t.Errorf("ModelEntries lost the 1.x reasoning field: %v", entry)
	}
}
