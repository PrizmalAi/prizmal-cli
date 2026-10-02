package launch

import "testing"

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

// The launched model keeps the tier and description the catalog gives it, so
// its own picker row does too.
func TestLaunchModelsCarryTheChosenModelsTier(t *testing.T) {
	models := LaunchModels("smart", []LaunchModel{{Name: "smart", Tier: "opus", Description: "Fast"}}, true)
	if len(models) != 1 || models[0].Tier != "opus" || models[0].Description != "Fast" {
		t.Fatalf("models = %+v, want smart with tier opus and its description", models)
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
