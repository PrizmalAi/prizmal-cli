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
