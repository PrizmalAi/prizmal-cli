package codex

import (
	"slices"
	"testing"

	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/PrizmalAi/prizmal-cli/internal/tiers"
)

// A tenant whose Switch reports generic tiers gets Codex rows whose slug is the
// alias the Switch routes and whose display name is the Codex word. A config
// that serves every tier gives four rows.
func TestCodexCatalogSlugsAreAliasesAndNamesAreCodexWords(t *testing.T) {
	catalog := launch.WithTierRows([]launch.LaunchModel{
		{Name: "glm", Tiers: tiers.All, Description: "One model"},
		{Name: "advisor-router"},
	})
	models := launch.LaunchModels("frontier", catalog, true)
	slugs, names, _ := codexCatalogEntries(t, "frontier", models)
	if want := []string{"frontier", "core", "flash", "premier", "advisor-router"}; !slices.Equal(slugs, want) {
		t.Errorf("slugs = %v, want %v", slugs, want)
	}
	if want := []string{"Sol", "Terra", "Luna", "Astra", "advisor-router"}; !slices.Equal(names, want) {
		t.Errorf("display names = %v, want %v", names, want)
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
