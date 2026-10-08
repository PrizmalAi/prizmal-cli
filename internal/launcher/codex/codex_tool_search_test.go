package codex

import (
	"encoding/json"
	"os"
	"testing"

	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

// TestCodexEntryEnablesToolSearch pins the catalog flag behind Codex's tool
// search. Claude Code launches set ENABLE_TOOL_SEARCH so a request carries a
// short tool list and the model fetches the rest on demand. Codex does the
// same for an entry that sets supports_search_tool, and sends every tool on
// every turn for one that leaves it out.
func TestCodexEntryEnablesToolSearch(t *testing.T) {
	entry := buildCodexModelEntry(launch.LaunchModel{Name: "prizmal-flash"})
	if got, ok := entry["supports_search_tool"].(bool); !ok || !got {
		t.Fatalf("supports_search_tool = %v, want true", entry["supports_search_tool"])
	}
}

// TestCodexLaunchHandsCodexACatalogWithToolSearch runs the launch against a
// fake codex and reads the catalog file it was given.
func TestCodexLaunchHandsCodexACatalogWithToolSearch(t *testing.T) {
	_, seen, _ := runFakeCodex(t)
	raw, err := os.ReadFile(seen)
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Models []struct {
			Slug           string `json:"slug"`
			SupportsSearch bool   `json:"supports_search_tool"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) == 0 {
		t.Fatal("the catalog lists no models")
	}
	for _, m := range catalog.Models {
		if !m.SupportsSearch {
			t.Errorf("%s lacks supports_search_tool", m.Slug)
		}
	}
}
