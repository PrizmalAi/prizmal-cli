package launch

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/model"
)

// The launch list carries the model to run first, with the capabilities the
// catalog gives it, followed by the rest of the tenant's models.
func TestLaunchModelsPutTheChosenModelFirst(t *testing.T) {
	catalog := []LaunchModel{
		{Name: "alpha"},
		{Name: "chosen"},
		{Name: "zeta"},
	}

	models := LaunchModels("chosen", catalog, true)

	if len(models) != 3 {
		t.Fatalf("built %d models, want 3: %v", len(models), launchModelNames(models))
	}
	if models[0].Name != "chosen" {
		t.Fatalf("models[0] = %q, want the chosen model first; pi and cline read models[0]", models[0].Name)
	}
	if got := launchModelNames(models); got[0] != "chosen" || got[1] != "alpha" || got[2] != "zeta" {
		t.Fatalf("names = %v, want [chosen alpha zeta]", got)
	}
}

// A runner that does not show the catalog is handed only the model it runs.
// pi and cline read models[0] and write it into their config, so a wider list
// would change the default they select.
func TestLaunchModelsWithoutTheCatalogAreJustTheChosenModel(t *testing.T) {
	catalog := []LaunchModel{{Name: "alpha"}, {Name: "chosen"}}

	models := LaunchModels("chosen", catalog, false)

	if len(models) != 1 || models[0].Name != "chosen" {
		t.Fatalf("models = %v, want only [chosen]", launchModelNames(models))
	}
}

// The chosen model keeps the capabilities the catalog reported for it, which
// the entry builders read to decide what the model can take as input.
func TestLaunchModelsCarryCapabilitiesForTheChosenModel(t *testing.T) {
	catalog := []LaunchModel{
		{Name: "vision-model", Capabilities: capabilitiesFromModalities([]string{"text", "image"})},
	}

	models := LaunchModels("vision-model", catalog, true)

	if !models[0].HasCapability("vision") {
		t.Fatalf("chosen model lost its capabilities: %+v", models[0])
	}
}

// The chosen model is never duplicated when it also appears in the catalog.
func TestLaunchModelsDoNotDuplicateTheChosenModel(t *testing.T) {
	catalog := []LaunchModel{{Name: "chosen"}, {Name: "other"}}

	models := LaunchModels("chosen", catalog, true)

	seen := 0
	for _, m := range models {
		if m.Name == "chosen" {
			seen++
		}
	}
	if seen != 1 {
		t.Fatalf("the chosen model appears %d times: %v", seen, launchModelNames(models))
	}
}

// The chosen model is looked up tolerating the switch's [1m] decoration, so a
// bare choice still finds its catalog entry and its capabilities.
func TestLaunchModelsMatchChosenAcrossTheSuffix(t *testing.T) {
	catalog := []LaunchModel{
		{Name: "vision-model", Capabilities: capabilitiesFromModalities([]string{"image"})},
	}

	models := LaunchModels("vision-model[1m]", catalog, true)

	if !models[0].HasCapability("vision") {
		t.Fatalf("chosen model did not match its catalog entry: %+v", models[0])
	}
}

// A launch fetches the catalog once, however many readers ask for it.
func TestFetchCatalogIsCached(t *testing.T) {
	calls := 0
	srv := newCountingSwitch(t, `{"data":[{"id":"cheap"}]}`, &calls)
	useSwitch(t, srv, "test-switch-key")
	ResetModelCatalog()
	t.Cleanup(ResetModelCatalog)

	for i := 0; i < 3; i++ {
		if _, err := FetchCatalog(context.Background()); err != nil {
			t.Fatalf("FetchCatalog: %v", err)
		}
	}
	if calls != 1 {
		t.Fatalf("the catalog was fetched %d times, want 1", calls)
	}
}

// A failed fetch is remembered, so a launch does not retry a switch that
// already refused it, and the failure reaches the caller rather than degrading.
func TestFetchCatalogPropagatesAndCachesTheFailure(t *testing.T) {
	calls := 0
	srv := newCountingSwitch(t, `{"error":"nope"}`, &calls, http.StatusInternalServerError)
	useSwitch(t, srv, "test-switch-key")
	ResetModelCatalog()
	t.Cleanup(ResetModelCatalog)

	for i := 0; i < 2; i++ {
		if _, err := FetchCatalog(context.Background()); err == nil {
			t.Fatal("FetchCatalog accepted a 500; a picker must not offer rows it could not read")
		}
	}
	if calls != 1 {
		t.Fatalf("the catalog was fetched %d times, want 1", calls)
	}
}

// The fetch error must never carry the key, since it reaches the operator's
// terminal through main's error line.
func TestFetchCatalogErrorNamesNoKey(t *testing.T) {
	srv, _, _ := switchTestServer(t, `{"error":"nope"}`, http.StatusUnauthorized)
	useSwitch(t, srv.URL, "sk-secret-value")
	ResetModelCatalog()
	t.Cleanup(ResetModelCatalog)

	_, err := FetchCatalog(context.Background())
	if err == nil {
		t.Fatal("FetchCatalog accepted a 401")
	}
	if strings.Contains(err.Error(), "sk-secret-value") {
		t.Fatalf("error carries the key: %v", err)
	}
}

// The catalog offers every Claude tier first, then the router configs the
// switch lists, so an operator can pick a tier or a named model.
func TestFetchCatalogOffersEveryClaudeTierFirst(t *testing.T) {
	calls := 0
	srv := newCountingSwitch(t, `{"data":[{"id":"team-opus-blend[1m]"},{"id":"smart[1m]"}]}`, &calls)
	useSwitch(t, srv, "test-switch-key")
	ResetModelCatalog()
	t.Cleanup(ResetModelCatalog)

	catalog, err := FetchCatalog(context.Background())
	if err != nil {
		t.Fatalf("FetchCatalog: %v", err)
	}
	var names []string
	for _, m := range catalog {
		names = append(names, m.Name)
	}
	want := []string{
		"claude-tier-opus", "claude-tier-sonnet", "claude-tier-haiku", "claude-tier-fable",
		"team-opus-blend", "smart",
	}
	if !slices.Equal(names, want) {
		t.Fatalf("catalog = %v, want %v", names, want)
	}
}

// A switch that still lists a tier alias yields one entry for it, in the
// tier's place, carrying the capabilities the switch gave it.
func TestFetchCatalogListsATierOnceWhenTheSwitchListsItToo(t *testing.T) {
	calls := 0
	srv := newCountingSwitch(t, `{"data":[{"id":"smart[1m]"},{"id":"claude-tier-sonnet[1m]","input_modalities":["text","image"]}]}`, &calls)
	useSwitch(t, srv, "test-switch-key")
	ResetModelCatalog()
	t.Cleanup(ResetModelCatalog)

	catalog, err := FetchCatalog(context.Background())
	if err != nil {
		t.Fatalf("FetchCatalog: %v", err)
	}
	var names []string
	for _, m := range catalog {
		names = append(names, m.Name)
	}
	want := []string{"claude-tier-opus", "claude-tier-sonnet", "claude-tier-haiku", "claude-tier-fable", "smart"}
	if !slices.Equal(names, want) {
		t.Fatalf("catalog = %v, want %v", names, want)
	}
	if !slices.Contains(catalog[1].Capabilities, model.CapabilityVision) {
		t.Fatalf("claude-tier-sonnet capabilities = %v, want the switch's image modality kept", catalog[1].Capabilities)
	}
}

// The launched model keeps the tier and description the catalog gives it, so
// its own picker row does too.
func TestLaunchModelsCarryTheChosenModelsTier(t *testing.T) {
	models := LaunchModels("smart", []LaunchModel{{Name: "smart", Tier: "opus", Description: "Fast"}}, true)
	if len(models) != 1 || models[0].Tier != "opus" || models[0].Description != "Fast" {
		t.Fatalf("models = %+v, want smart with tier opus and its description", models)
	}
}

// A router config the Switch tags with a tier holds that tier's alias, so the
// tier row stands for it. The tier row takes its description and
// capabilities, and the config itself is folded out of the rows.
func TestClaudeTiersFoldTheConfigThatHoldsEachTier(t *testing.T) {
	catalog := withClaudeTiers([]LaunchModel{
		{Name: "smart", Tier: "opus", Description: "Smart blend", Capabilities: []model.Capability{model.CapabilityVision}},
		{Name: "team-opus-blend", Description: "Team blend"},
		{Name: "experimental"},
	})
	rows := ModelRows(catalog)
	var got []string
	for _, row := range rows {
		got = append(got, row.Label+"="+row.Description)
	}
	want := []string{
		"tier-opus=Smart blend", "tier-sonnet=Sonnet tier", "tier-haiku=Haiku tier", "tier-fable=Fable tier",
		"team-opus-blend=Team blend", "experimental=",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	if !slices.Contains(catalog[0].Capabilities, model.CapabilityVision) {
		t.Fatalf("tier-opus capabilities = %v, want the folded config's", catalog[0].Capabilities)
	}
}

// A launch on a folded config by its own name still runs it with its tier's
// profile and description. Its row is the launch's own, first.
func TestLaunchModelsKeepAFoldedConfigLaunchedByName(t *testing.T) {
	catalog := withClaudeTiers([]LaunchModel{{Name: "smart", Tier: "opus", Description: "Smart blend"}})
	rows := ModelRows(LaunchModels("smart", catalog, true))
	if len(rows) != 5 {
		t.Fatalf("rows = %+v, want smart then the four tiers", rows)
	}
	if rows[0].Model != "smart" || rows[0].BehavesAs != "claude-opus-5" || rows[0].Description != "Smart blend" {
		t.Fatalf("row 0 = %+v, want smart on the opus profile with its description", rows[0])
	}
}

// An empty listing still means the tenant serves nothing. The four tier rows
// the CLI always offers must not hide that behind a menu of aliases nothing
// holds.
func TestFetchCatalogReportsNoModelsForAnEmptyListing(t *testing.T) {
	calls := 0
	srv := newCountingSwitch(t, `{"data":[]}`, &calls)
	useSwitch(t, srv, "test-switch-key")
	ResetModelCatalog()
	t.Cleanup(ResetModelCatalog)

	if _, err := FetchCatalog(context.Background()); !errors.Is(err, ErrNoModels) {
		t.Fatalf("FetchCatalog on an empty listing = %v, want ErrNoModels", err)
	}
}

// The switch gives each tier one holder. If it ever sends two, the tier row
// stands for the first, and the second keeps a row of its own rather than
// disappearing.
func TestClaudeTiersFoldOnlyTheFirstHolderOfATier(t *testing.T) {
	models := withClaudeTiers([]LaunchModel{
		{Name: "first", Tier: "opus", Description: "First"},
		{Name: "second", Tier: "opus", Description: "Second"},
	})
	folded := map[string]string{}
	for _, m := range models {
		folded[m.Name] = m.FoldedInto
		if m.Name == "claude-tier-opus" && m.Description != "First" {
			t.Errorf("opus tier description = %q, want the first holder's", m.Description)
		}
	}
	if folded["first"] != "claude-tier-opus" {
		t.Errorf("first holder folded into %q, want claude-tier-opus", folded["first"])
	}
	if folded["second"] != "" {
		t.Errorf("second holder folded into %q, want its own row", folded["second"])
	}
}
