package codex

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

// docsCatalogExample is the example entry in docs/codex.md, the one a manual
// setup copies into its own catalog.
func docsCatalogExample(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "codex.md"))
	if err != nil {
		t.Fatal(err)
	}
	block := regexp.MustCompile("(?s)<!-- catalog-example -->\\s*```json\\n(.*?)\\n```").FindSubmatch(data)
	if block == nil {
		t.Fatal("docs/codex.md has no catalog example: a ```json block after <!-- catalog-example -->")
	}
	var doc struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(block[1], &doc); err != nil {
		t.Fatalf("the catalog example is not JSON: %v", err)
	}
	if len(doc.Models) != 1 {
		t.Fatalf("the catalog example has %d models, want 1", len(doc.Models))
	}
	return doc.Models[0]
}

// The README and docs/codex.md tell a manual setup to write a catalog entry.
// Codex rejects an entry that lacks a field it requires, such as shell_type, so
// the example must carry what a launch writes. This test builds the entry the
// way a launch does, with the prompt placeholder the doc shows, and requires the
// example to equal it, so a field added to the builder fails the doc until the
// doc has it too.
func TestDocsCatalogExampleMatchesTheEntryALaunchWrites(t *testing.T) {
	example := docsCatalogExample(t)

	t.Setenv("HARNESS_CONTEXT_LENGTH", "")
	entry := buildCodexModelEntry(launch.LaunchModel{Name: "your-model", ContextLength: 1_000_000})
	entry["display_name"] = "your-model"
	entry["description"] = "A model your switch key serves"
	entry["base_instructions"] = example["base_instructions"] // copied from `codex debug models --bundled`
	if _, ok := example["base_instructions"].(string); !ok || example["base_instructions"] == "" {
		t.Fatal("the example must show base_instructions with text to copy, since Codex reads an empty one as no prompt")
	}

	// Compare as JSON, which is the form both sides reach Codex in.
	want, _ := json.Marshal(entry)
	var wantMap map[string]any
	if err := json.Unmarshal(want, &wantMap); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(example, wantMap) {
		got, _ := json.MarshalIndent(example, "", "  ")
		exp, _ := json.MarshalIndent(wantMap, "", "  ")
		t.Fatalf("the docs/codex.md example differs from the entry a launch writes\n--- doc ---\n%s\n--- builder ---\n%s", got, exp)
	}
}

// The installed Codex is the final judge of the example: it parses the catalog
// file and answers `debug models` with the entry, or fails with the missing
// field. The test skips when Codex is not installed.
func TestDocsCatalogExampleIsAcceptedByTheInstalledCodex(t *testing.T) {
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("codex is not installed")
	}
	example := docsCatalogExample(t)
	data, err := json.Marshal(map[string]any{"models": []any{example}})
	if err != nil {
		t.Fatal(err)
	}
	catalog := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(catalog, data, 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("codex", "--no-daemon", "-c", `model_catalog_json=`+strconv.Quote(catalog), "debug", "models")
	cmd.Env = append(os.Environ(), "CODEX_HOME="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("codex rejects the documented catalog entry: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), `"slug":"your-model"`) {
		t.Fatalf("codex did not list the documented entry:\n%s", out)
	}
}
