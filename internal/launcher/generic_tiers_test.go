package launch

import (
	"slices"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/tiers"
)

// The Switch reports every generic tier a config holds in `tiers`, and keeps
// one in `tier` for CLIs that read only that. A legacy Claude word in `tier`
// stays the Claude tier it always was.
func TestParseSwitchCatalogReadsGenericTiers(t *testing.T) {
	models, err := ParseSwitchCatalog([]byte(`{"data":[
		{"id":"glm","tier":"frontier","tiers":["frontier","core","flash","premier"]},
		{"id":"smart","tier":"frontier"},
		{"id":"legacy","tier":"opus"},
		{"id":"both","tier":"opus","tiers":["frontier","bogus"]},
		{"id":"named-smart","tier":"smart"}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range [][]tiers.Tier{tiers.All, {tiers.Frontier}, nil, {tiers.Frontier}, nil} {
		if !slices.Equal(models[i].Tiers, want) {
			t.Errorf("%s tiers = %v, want %v", models[i].Name, models[i].Tiers, want)
		}
	}
	if models[2].Tier != "opus" || models[0].Tier != "" {
		t.Errorf("legacy tier = %q, generic tier = %q, want opus and none", models[2].Tier, models[0].Tier)
	}
}

// One router config may serve every tier. Each tier it holds gets a row named
// by the tier's alias, and the config folds into the first.
func TestOneConfigHoldingEveryTierOffersFourRows(t *testing.T) {
	catalog := withClaudeTiers([]LaunchModel{
		{Name: "glm", Tiers: tiers.All, Description: "One model"},
		{Name: "extra"},
	})
	var names []string
	for _, m := range catalog {
		names = append(names, m.Name)
	}
	if want := []string{"frontier", "core", "flash", "premier", "glm", "extra"}; !slices.Equal(names, want) {
		t.Fatalf("catalog = %v, want %v", names, want)
	}
	if catalog[4].FoldedInto != "frontier" {
		t.Errorf("glm folded into %q, want frontier", catalog[4].FoldedInto)
	}
	for _, row := range catalog[:4] {
		if row.Description != "glm · One model" {
			t.Errorf("%s description = %q, want the holder named", row.Name, row.Description)
		}
	}
}

func genericTenant() []LaunchModel {
	return withClaudeTiers([]LaunchModel{
		{Name: "smart", Tiers: []tiers.Tier{tiers.Frontier}, Description: "Everyday"},
		{Name: "flash", Tiers: []tiers.Tier{tiers.Flash}},
		{Name: "advisor-router"},
	})
}

func rowFields(rows []ModelRow) (labels, models []string) {
	for _, row := range rows {
		labels = append(labels, row.Label)
		models = append(models, row.Model)
	}
	return labels, models
}

// Each harness labels a tier row with its own word, and the row's model is the
// alias the Switch routes. A config that names itself after its tier is the
// tier's row.
func TestModelRowsLabelTierRowsWithTheHarnessWord(t *testing.T) {
	for _, tc := range []struct {
		vocab      tiers.Vocabulary
		wantLabels []string
		wantModels []string
	}{
		{tiers.ClaudeCode, []string{"Opus", "Haiku", "advisor-router"}, []string{"frontier", "flash", "advisor-router"}},
		// Codex names a tier row after its config, so no row reads as an
		// OpenAI model.
		{tiers.Codex, []string{"smart", "flash", "advisor-router"}, []string{"frontier", "flash", "advisor-router"}},
		{tiers.Generic, []string{"frontier", "flash", "advisor-router"}, []string{"frontier", "flash", "advisor-router"}},
		// pi, OpenCode and Cline show the real router configs.
		{tiers.ConfigNames, []string{"flash", "smart", "advisor-router"}, []string{"flash", "smart", "advisor-router"}},
	} {
		labels, models := rowFields(ModelRowsFor(genericTenant(), tc.vocab))
		if !slices.Equal(labels, tc.wantLabels) || !slices.Equal(models, tc.wantModels) {
			t.Errorf("%s rows = %v %v, want %v %v", tc.vocab, labels, models, tc.wantLabels, tc.wantModels)
		}
	}
}

// A Claude Code tier row renders as its Claude family.
func TestGenericTierRowBehavesAsItsClaudeFamily(t *testing.T) {
	rows := ModelRowsFor(genericTenant(), tiers.ClaudeCode)
	if rows[0].Tier != "opus" || rows[0].BehavesAs != "claude-opus-5" || rows[0].Description != "smart · Everyday" {
		t.Fatalf("frontier row = %+v, want the opus profile and the holder's text", rows[0])
	}
}

// A tenant whose Switch reports no tier shows its router configs by name.
func TestNoTierShowsConfigNames(t *testing.T) {
	_, models := rowFields(ModelRowsFor(withClaudeTiers([]LaunchModel{{Name: "a"}, {Name: "b"}}), tiers.Codex))
	if !slices.Equal(models, []string{"a", "b"}) {
		t.Fatalf("rows = %v, want the configs", models)
	}
}

// A Codex word is a label. The launch maps it to the alias before it sends
// anything, because the Switch routes no Codex word.
func TestRouteNameMapsAHarnessWordToItsAlias(t *testing.T) {
	catalog := genericTenant()
	for in, want := range map[string]string{"sol": "frontier", "Luna": "flash", "terra": "terra", "smart": "smart"} {
		if got := RouteNameFor(catalog, in, tiers.Codex); got != want {
			t.Errorf("RouteNameFor(%q, codex) = %q, want %q", in, got, want)
		}
	}
	if got := RouteNameFor(catalog, "sol", tiers.ConfigNames); got != "sol" {
		t.Errorf("RouteNameFor(sol, config names) = %q, want it unchanged", got)
	}
}

// Claude Code resolves opus to its own id, and the launch maps that id onto the
// frontier alias, so a tenant holding frontier routes both.
func TestCheckRoutableAcceptsClaudeNamesForAHeldGenericTier(t *testing.T) {
	catalog := genericTenant()
	for _, name := range []string{"frontier", "opus", "claude-opus-5", "haiku", "claude-haiku-4-5-20251001"} {
		if err := CheckRoutable(name, catalog, true); err != nil {
			t.Errorf("CheckRoutable(%q) = %v, want nil", name, err)
		}
	}
	if err := CheckRoutable("sonnet", catalog, true); err == nil {
		t.Error("CheckRoutable(sonnet) = nil, want the unheld sonnet tier refused")
	}
}
