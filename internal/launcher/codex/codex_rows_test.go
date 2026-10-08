package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

// codexTierCatalog is a catalog as the launch holds it after the tier rows are
// added: four tier aliases, the router configs folded into them, and one
// config with no tier.
var codexTierCatalog = []launch.LaunchModel{
	{Name: "claude-tier-opus", Description: "A"},
	{Name: "claude-tier-sonnet"},
	{Name: "claude-tier-haiku"},
	{Name: "claude-tier-fable"},
	{Name: "alpha", Tier: "opus", Description: "A", FoldedInto: "claude-tier-opus"},
	{Name: "beta", Tier: "sonnet", FoldedInto: "claude-tier-sonnet"},
	{Name: "gamma", Tier: "haiku", FoldedInto: "claude-tier-haiku"},
	{Name: "delta", Tier: "fable", FoldedInto: "claude-tier-fable"},
	{Name: "extra"},
}

// codexCatalogEntries writes the catalog a launch hands Codex and returns the
// slug, display name and description of each entry, in file order.
func codexCatalogEntries(t *testing.T, chosen string, models []launch.LaunchModel) (slugs, names, descriptions []string) {
	t.Helper()
	fakeCodexBundle(t, fakeCodexCatalog)
	path := filepath.Join(t.TempDir(), "catalog.json")
	if err := writeCodexModelCatalog(path, codexCatalogModels(chosen, models)); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Models []struct {
			Slug        string `json:"slug"`
			DisplayName string `json:"display_name"`
			Description string `json:"description"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, m := range got.Models {
		slugs = append(slugs, m.Slug)
		names = append(names, m.DisplayName)
		descriptions = append(descriptions, m.Description)
	}
	return slugs, names, descriptions
}

// TestCodexCatalogListsTheRowsThePrizmalPickerLists pins one list for both
// pickers: Codex's /model shows the same rows, in the same order, as the
// prizmal picker. A tier alias is a row, and the router config folded into it
// is not a second one.
func TestCodexCatalogListsTheRowsThePrizmalPickerLists(t *testing.T) {
	models := launch.LaunchModels("claude-tier-opus", codexTierCatalog, true)
	var want []string
	for _, row := range launch.ModelRows(models) {
		want = append(want, row.Model)
	}
	slugs, _, _ := codexCatalogEntries(t, "claude-tier-opus", models)
	if !slices.Equal(slugs, want) {
		t.Fatalf("Codex lists %v, the prizmal picker lists %v", slugs, want)
	}
}

// TestCodexCatalogLabelsRowsLikeThePrizmalPicker pins the labels and the row
// text: the picker and Claude Code read "tier-opus" and the tier's description,
// and Codex's /model and footer read the same.
func TestCodexCatalogLabelsRowsLikeThePrizmalPicker(t *testing.T) {
	models := launch.LaunchModels("claude-tier-opus", codexTierCatalog, true)
	var labels, descriptions []string
	for _, row := range launch.ModelRows(models) {
		labels = append(labels, row.Label)
		descriptions = append(descriptions, row.Description)
	}
	_, names, got := codexCatalogEntries(t, "claude-tier-opus", models)
	if !slices.Equal(names, labels) {
		t.Fatalf("Codex display names = %v, want the picker labels %v", names, labels)
	}
	if !slices.Equal(got, descriptions) {
		t.Fatalf("Codex descriptions = %v, want the picker descriptions %v", got, descriptions)
	}
	if labels[0] != "tier-opus" {
		t.Fatalf("picker label = %q, want tier-opus", labels[0])
	}
}

// TestCodexCatalogKeepsTheLaunchedModelWhenNoRowShowsIt covers -m naming a
// router config that a tier row folds away. Codex reads the launched model's
// entry for its window and prompt, so the entry stays even without a row.
func TestCodexCatalogKeepsTheLaunchedModelWhenNoRowShowsIt(t *testing.T) {
	models := launch.LaunchModels("alpha", codexTierCatalog, true)
	slugs, names, _ := codexCatalogEntries(t, "alpha", models)
	i := slices.Index(slugs, "alpha")
	if i < 0 {
		t.Fatalf("slugs = %v, want the launched model alpha", slugs)
	}
	if names[i] != "alpha" {
		t.Errorf("display name = %q, want alpha", names[i])
	}
	if !slices.Contains(slugs, "claude-tier-opus") {
		t.Errorf("slugs = %v, want the tier rows as well", slugs)
	}
}
