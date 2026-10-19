package opencode

import (
	"testing"

	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/PrizmalAi/prizmal-cli/internal/model"
)

// TestOpenCodeEntryHasNoLimit pins that opencode's entry carries no limit
// object. No catalog entry reported an output-token budget, so the limit block
// never ran and opencode kept applying its own defaults; writing a limit here
// now would change the config the harness reads.
func TestOpenCodeEntryHasNoLimit(t *testing.T) {
	entry := opencodeModelEntry(t, launch.LaunchModel{Name: "no-limit-model"})

	if got, ok := entry["limit"]; ok {
		t.Fatalf("entry has limit = %v, want no limit key", got)
	}
}

// TestOpenCodeThinkingLevelsComeFromTheName pins the reasoning-levels check
// that survives the removal of the serving-family fields. No catalog entry
// reported a family, so the family loop was a no-op and the model's own name
// decided the scale; opencode's two scales differ by model line, and answering
// "none of the above" costs a usable effort setting on a model that takes one.
func TestOpenCodeThinkingLevelsComeFromTheName(t *testing.T) {
	thinking := launch.LaunchModel{
		Name:         "openai/gpt-oss-120b",
		Capabilities: []model.Capability{model.CapabilityThinking},
	}
	entry := opencodeModelEntry(t, thinking)

	if entry["reasoning"] != true {
		t.Fatalf("entry lacks reasoning = true: %v", entry)
	}
	options, _ := entry["options"].(map[string]any)
	if got := options["reasoningEffort"]; got != "medium" {
		t.Fatalf("options.reasoningEffort = %v, want medium", got)
	}
	variants, _ := entry["variants"].(map[string]any)
	for _, level := range []string{"low", "medium", "high", "max"} {
		effort, _ := variants[level].(map[string]any)["reasoningEffort"].(string)
		if effort != level {
			t.Fatalf("variants.%s.reasoningEffort = %q, want %q", level, effort, level)
		}
	}

	// A model off the gpt-oss line keeps opencode's other scale, so the check
	// is a name test and not a blanket "four levels".
	plain := launch.LaunchModel{
		Name:         "anthropic/claude-sonnet-5",
		Capabilities: []model.Capability{model.CapabilityThinking},
	}
	plainVariants := opencodeModelEntry(t, plain)["variants"].(map[string]any)
	if _, ok := plainVariants["none"]; !ok {
		t.Fatalf("a non-gpt-oss model lost its none variant: %v", plainVariants)
	}
}
