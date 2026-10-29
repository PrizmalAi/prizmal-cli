package opencode

import (
	"testing"

	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/PrizmalAi/prizmal-cli/internal/model"
)

// TestOpenCodeEntryDeclaresTheContextWindow pins the limit block of opencode's
// entry. opencode treats a model without limit.context as having no window and
// never compacts ahead of the provider's refusal, so every entry states the
// window the Switch rules and the output budget opencode keeps free in it.
func TestOpenCodeEntryDeclaresTheContextWindow(t *testing.T) {
	entry := opencodeModelEntry(t, launch.LaunchModel{Name: "any-model"})

	limit, _ := entry["limit"].(map[string]any)
	if limit["context"] != float64(1_000_000) || limit["output"] != float64(32_000) {
		t.Fatalf("entry limit = %v, want context 1000000 and output 32000", entry["limit"])
	}
}

// TestOpenCodeReasoningModelsGetEveryEffortLevel pins that a model the Switch
// reports as reasoning gets the same four effort variants whatever its name.
// The Switch decides what a route does with an effort, so the launcher does not
// guess from the name which models take one: a name test left `high` disabled
// for every model off the gpt-oss line, and `--variant high` sent nothing. No
// default effort is written either, so a launch without a variant leaves the
// route's own default alone.
func TestOpenCodeReasoningModelsGetEveryEffortLevel(t *testing.T) {
	for _, name := range []string{"openai/gpt-oss-120b", "prizmal-flash"} {
		entry := opencodeModelEntry(t, launch.LaunchModel{
			Name:         name,
			Capabilities: []model.Capability{model.CapabilityThinking},
		})
		if entry["reasoning"] != true {
			t.Fatalf("%s: entry lacks reasoning = true: %v", name, entry)
		}
		if _, ok := entry["options"]; ok {
			t.Fatalf("%s: entry sets a default effort: %v", name, entry["options"])
		}
		variants, _ := entry["variants"].(map[string]any)
		for _, level := range []string{"low", "medium", "high", "max"} {
			variant, _ := variants[level].(map[string]any)
			if variant["reasoningEffort"] != level || variant["disabled"] != nil {
				t.Fatalf("%s: variants.%s = %v, want reasoningEffort %q", name, level, variant, level)
			}
		}
	}
}
