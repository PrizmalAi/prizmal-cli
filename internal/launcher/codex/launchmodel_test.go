package codex

import (
	"testing"

	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

// TestCodexEntryContextWindowFallsBackTo128k pins the surviving context-window
// rule. The Switch catalog reports no window for any entry, so codex ships the
// fallback and $HARNESS_CONTEXT_LENGTH is the only way to change it — the env
// var used to sit behind a guard (a cloud-model test and a safetensors-format
// test) that no catalog entry could ever fail.
func TestCodexEntryContextWindowFallsBackToTheDeclaredWindow(t *testing.T) {
	entry := buildCodexModelEntry(launch.LaunchModel{Name: "no-window-model"})

	if got := entry["context_window"]; got != codexFallbackContextWindow {
		t.Fatalf("context_window = %v, want %d", got, codexFallbackContextWindow)
	}
}

// TestCodexTruncationIsAlwaysTokens pins the truncation mode for every model
// name, including one that reads as cloud-served. It is tokens for all of them,
// which is what codex's own catalog declares: the mode used to be chosen by a
// name test that always answered false, so bytes was the only mode any entry
// could carry, and a bytes-mode limit cut every tool's output at 10000 bytes.
func TestCodexTruncationIsAlwaysTokens(t *testing.T) {
	for _, name := range []string{"gpt-5.6", "claude-opus-5[1m]", "some-cloud-model"} {
		policy, ok := buildCodexModelEntry(launch.LaunchModel{Name: name})["truncation_policy"].(map[string]any)
		if !ok {
			t.Fatalf("%s: entry lacks a truncation_policy object", name)
		}
		if got := policy["mode"]; got != "tokens" {
			t.Fatalf("%s: truncation_policy.mode = %v, want tokens", name, got)
		}
		if got := policy["limit"]; got != 10000 {
			t.Fatalf("%s: truncation_policy.limit = %v, want 10000", name, got)
		}
	}
}
