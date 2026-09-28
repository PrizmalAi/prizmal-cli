package launch

import (
	"context"
	"net/http"
	"strings"
	"testing"
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
