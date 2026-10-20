package codex

import (
	"slices"
	"testing"

	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/PrizmalAi/prizmal-cli/internal/tiers"
)

// A tenant whose Switch reports generic tiers gets Codex rows whose slug is the
// alias the Switch routes. The display name is the router config the row stands
// for, so no row passes itself off as an OpenAI model, and the description
// carries the Codex word. A config that serves every tier gives four rows.
func TestCodexCatalogSlugsAreAliasesAndNamesAreConfigs(t *testing.T) {
	catalog := launch.WithTierRows([]launch.LaunchModel{
		{Name: "glm", Tiers: tiers.All, Description: "One model"},
		{Name: "advisor-router"},
	})
	models := launch.LaunchModels("frontier", catalog, true)
	slugs, names, descriptions := codexCatalogEntries(t, "frontier", models)
	if want := []string{"frontier", "core", "flash", "premier", "advisor-router"}; !slices.Equal(slugs, want) {
		t.Errorf("slugs = %v, want %v", slugs, want)
	}
	if want := []string{"glm", "glm", "glm", "glm", "advisor-router"}; !slices.Equal(names, want) {
		t.Errorf("display names = %v, want %v", names, want)
	}
	want := []string{"Sol-like · One model", "Terra-like · One model", "Luna-like · One model", "Astra-like · One model", ""}
	if !slices.Equal(descriptions, want) {
		t.Errorf("descriptions = %q, want %q", descriptions, want)
	}
}

// A tier alias the Switch lists with no config behind it is named by the
// alias, and its description still says which tier it is like.
func TestCodexCatalogNamesAnUnheldAliasRowByTheAlias(t *testing.T) {
	catalog := launch.WithTierRows([]launch.LaunchModel{{Name: "frontier", Tiers: []tiers.Tier{tiers.Frontier}}})
	_, names, descriptions := codexCatalogEntries(t, "frontier", launch.LaunchModels("frontier", catalog, true))
	if !slices.Equal(names, []string{"frontier"}) || !slices.Equal(descriptions, []string{"Sol-like"}) {
		t.Errorf("names = %v, descriptions = %q, want frontier and Sol-like", names, descriptions)
	}
}

// A Switch that reports no tier gives Codex the router configs by name.
func TestCodexCatalogShowsConfigNamesWithoutTiers(t *testing.T) {
	catalog := launch.WithTierRows([]launch.LaunchModel{{Name: "smart"}, {Name: "flash"}})
	slugs, names, _ := codexCatalogEntries(t, "smart", launch.LaunchModels("smart", catalog, true))
	if !slices.Equal(slugs, []string{"smart", "flash"}) || !slices.Equal(names, slugs) {
		t.Errorf("slugs = %v, names = %v, want the configs", slugs, names)
	}
}
