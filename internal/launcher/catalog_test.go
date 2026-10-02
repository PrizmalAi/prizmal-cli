package launch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/model"
)

// switchCatalogJSON mirrors the shape the Switch serves on GET /v1/models,
// as observed live on staging: a top-level "data" array whose entries carry
// "id" and, on some entries only, "input_modalities".
const switchCatalogJSON = `{
  "data": [
    {"id": "vision-model", "input_modalities": ["image", "text", "video"]},
    {"id": "text-model", "input_modalities": ["text"]},
    {"id": "empty-model", "input_modalities": []},
    {"id": "silent-model"}
  ],
  "extra_fields": {"request_type": "", "latency": 0}
}`

// switchTestServer serves the catalog and records the Authorization header
// and path it was asked for.
func switchTestServer(t *testing.T, body string, status int) (*httptest.Server, *string, *string) {
	t.Helper()
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &gotAuth, &gotPath
}

// useSwitch points envconfig at srv with the given key for one test.
func useSwitch(t *testing.T, url, key string) {
	t.Helper()
	envconfig.SetBaseURL(url)
	envconfig.SetAPIKey(key)
	t.Cleanup(func() {
		envconfig.SetBaseURL("")
		envconfig.SetAPIKey("")
	})
}

// newCountingSwitch starts a server answering one catalog body and counts the
// requests it receives, so a test can prove a fetch happened exactly once. The
// status defaults to 200.
func newCountingSwitch(t *testing.T, body string, calls *int, status ...int) string {
	t.Helper()
	code := http.StatusOK
	if len(status) > 0 {
		code = status[0]
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func capsOf(t *testing.T, models []LaunchModel, name string) []model.Capability {
	t.Helper()
	m, ok := findCatalogModel(models, name)
	if !ok {
		t.Fatalf("no model %q in %v", name, launchModelNames(models))
	}
	return m.Capabilities
}

// An entry that lists "image" is the only one that may carry vision.
func TestParseSwitchCatalogMapsInputModalities(t *testing.T) {
	models, err := parseSwitchCatalog([]byte(switchCatalogJSON))
	if err != nil {
		t.Fatalf("parseSwitchCatalog: %v", err)
	}
	if len(models) != 4 {
		t.Fatalf("parsed %d models, want 4: %v", len(models), launchModelNames(models))
	}

	vision := capsOf(t, models, "vision-model")
	if !slices.Contains(vision, model.CapabilityVision) {
		t.Fatalf("vision-model capabilities = %v, want it to contain %q", vision, model.CapabilityVision)
	}
	if !slices.Contains(vision, model.CapabilityCompletion) {
		t.Fatalf("vision-model capabilities = %v, want it to contain %q", vision, model.CapabilityCompletion)
	}

	text := capsOf(t, models, "text-model")
	if slices.Contains(text, model.CapabilityVision) {
		t.Fatalf("text-model capabilities = %v, want no vision", text)
	}
	if len(text) == 0 {
		t.Fatal("text-model capabilities are empty, which reads as unknown rather than known text-only")
	}
}

// A missing or empty input_modalities array means the Switch said nothing, so
// the entry must carry no capabilities at all: unknown, not text-only.
func TestParseSwitchCatalogLeavesUnknownEntriesEmpty(t *testing.T) {
	models, err := parseSwitchCatalog([]byte(switchCatalogJSON))
	if err != nil {
		t.Fatalf("parseSwitchCatalog: %v", err)
	}
	for _, name := range []string{"empty-model", "silent-model"} {
		if caps := capsOf(t, models, name); len(caps) != 0 {
			t.Fatalf("%s capabilities = %v, want none", name, caps)
		}
	}
}

// An unrecognised modality string is dropped rather than guessed at.
func TestParseSwitchCatalogDropsUnknownModalities(t *testing.T) {
	models, err := parseSwitchCatalog([]byte(`{"data":[{"id":"m","input_modalities":["video","hologram"]}]}`))
	if err != nil {
		t.Fatalf("parseSwitchCatalog: %v", err)
	}
	if caps := capsOf(t, models, "m"); len(caps) != 0 {
		t.Fatalf("capabilities = %v, want none", caps)
	}
}

// The Switch spells its document modality "file", and its catalogue hands it
// out independently of "image": a row can carry both, either, or neither.
// "file" is the only signal that a router config can take a PDF.
func TestParseSwitchCatalogMapsFileToDocument(t *testing.T) {
	models, err := parseSwitchCatalog([]byte(`{"data":[
		{"id":"file-model","input_modalities":["file","text"]},
		{"id":"both-model","input_modalities":["file","image","text"]},
		{"id":"image-model","input_modalities":["image","text"]}
	]}`))
	if err != nil {
		t.Fatalf("parseSwitchCatalog: %v", err)
	}

	file := capsOf(t, models, "file-model")
	if !slices.Contains(file, model.CapabilityDocument) {
		t.Fatalf("file-model capabilities = %v, want it to contain %q", file, model.CapabilityDocument)
	}
	if slices.Contains(file, model.CapabilityVision) {
		t.Fatalf("file-model capabilities = %v, want no vision", file)
	}

	both := capsOf(t, models, "both-model")
	for _, want := range []model.Capability{model.CapabilityDocument, model.CapabilityVision} {
		if !slices.Contains(both, want) {
			t.Fatalf("both-model capabilities = %v, want it to contain %q", both, want)
		}
	}

	image := capsOf(t, models, "image-model")
	if slices.Contains(image, model.CapabilityDocument) {
		t.Fatalf("image-model capabilities = %v, want no document", image)
	}
}

func TestParseSwitchCatalogRejectsGarbage(t *testing.T) {
	if _, err := parseSwitchCatalog([]byte("not json")); err == nil {
		t.Fatal("parseSwitchCatalog accepted non-JSON")
	}
}

// The fetch authenticates with the switch key as a bearer token and asks the
// /v1/models path.
func TestFetchSwitchCatalogSendsBearerKey(t *testing.T) {
	srv, gotAuth, gotPath := switchTestServer(t, switchCatalogJSON, http.StatusOK)
	useSwitch(t, srv.URL, "test-switch-key")

	models, err := fetchSwitchCatalog(context.Background())
	if err != nil {
		t.Fatalf("fetchSwitchCatalog: %v", err)
	}
	if *gotAuth != "Bearer test-switch-key" {
		t.Fatalf("Authorization header = %q, want a bearer token", *gotAuth)
	}
	if *gotPath != "/v1/models" {
		t.Fatalf("path = %q, want /v1/models", *gotPath)
	}
	if len(models) != 4 {
		t.Fatalf("fetched %d models, want 4", len(models))
	}
}

// A host already carrying the /v1 suffix must not produce /v1/v1/models.
func TestSwitchCatalogURLDoesNotDoubleV1(t *testing.T) {
	for _, tc := range []struct{ base, want string }{
		{"https://api.prizmal.ai", "https://api.prizmal.ai/v1/models"},
		{"https://api.prizmal.ai/", "https://api.prizmal.ai/v1/models"},
		{"https://api.prizmal.ai/v1", "https://api.prizmal.ai/v1/models"},
		{"https://api.prizmal.ai/v1/", "https://api.prizmal.ai/v1/models"},
	} {
		if got := switchCatalogURL(tc.base); got != tc.want {
			t.Fatalf("switchCatalogURL(%q) = %q, want %q", tc.base, got, tc.want)
		}
	}
}

// A non-200 is an error, not a silently empty catalog.
func TestFetchSwitchCatalogRejectsErrorStatus(t *testing.T) {
	srv, _, _ := switchTestServer(t, `{"error":"nope"}`, http.StatusInternalServerError)
	useSwitch(t, srv.URL, "test-switch-key")

	if _, err := fetchSwitchCatalog(context.Background()); err == nil {
		t.Fatal("fetchSwitchCatalog accepted a 500")
	}
}

// A failed fetch warns once and hands the launch nothing, so a launch never
// fails because the catalog fetch failed. The warning is the one line
// CatalogError builds: the endpoint, the source of the key, and the status the
// Switch answered, so a 401 reads as a bad key for this host rather than as a
// menu that is merely short.
func TestBestEffortCatalogWarnsAndReturnsNothing(t *testing.T) {
	srv, _, _ := switchTestServer(t, `{"error":"nope"}`, http.StatusInternalServerError)
	useSwitch(t, srv.URL, "test-switch-key")
	ResetModelCatalog()
	t.Cleanup(ResetModelCatalog)

	var warn strings.Builder
	catalog := BestEffortCatalog(context.Background(), &warn)

	if catalog != nil {
		t.Fatalf("catalog = %v, want nil after a failed fetch", launchModelNames(catalog))
	}
	line := warn.String()
	for _, want := range []string{srv.URL, "500"} {
		if !strings.Contains(line, want) {
			t.Errorf("warning = %q, want it to contain %q", line, want)
		}
	}
	if strings.Count(strings.TrimSuffix(line, "\n"), "\n") != 0 {
		t.Errorf("warning = %q, want a single line", line)
	}
}

// Never call the endpoint without a credential, and never warn about a fetch
// that was never attempted: an unauthenticated launch is not a failed one.
func TestBestEffortCatalogSkipsFetchWithoutKey(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = w.Write([]byte(switchCatalogJSON))
	}))
	defer srv.Close()
	useSwitch(t, srv.URL, "")

	var warn strings.Builder
	if catalog := BestEffortCatalog(context.Background(), &warn); catalog != nil {
		t.Fatalf("catalog = %v, want nil with no credential present", launchModelNames(catalog))
	}
	if called {
		t.Fatal("fetched the catalog with no credential present")
	}
	if warn.String() != "" {
		t.Fatalf("warning = %q, want silence when there is simply no key", warn.String())
	}
}

// The launch reader offers the same rows the picker reader does: every model the
// Switch lists, carrying its capabilities, and the tier aliases the CLI supplies
// because the Switch does not list them. A model the catalog does not hold stays
// unknown, which every entry builder reads as "ask nothing of the harness".
func TestBestEffortCatalogPopulatesCapabilitiesByName(t *testing.T) {
	srv, _, _ := switchTestServer(t, switchCatalogJSON, http.StatusOK)
	useSwitch(t, srv.URL, "test-switch-key")
	ResetModelCatalog()
	t.Cleanup(ResetModelCatalog)

	var warn strings.Builder
	catalog := BestEffortCatalog(context.Background(), &warn)
	if warn.String() != "" {
		t.Fatalf("warning = %q, want none on a successful fetch", warn.String())
	}

	vision := capsOf(t, catalog, "vision-model")
	if !slices.Contains(vision, model.CapabilityVision) {
		t.Fatalf("vision-model capabilities = %v, want vision", vision)
	}
	text := capsOf(t, catalog, "text-model")
	if slices.Contains(text, model.CapabilityVision) {
		t.Fatalf("text-model capabilities = %v, want no vision", text)
	}
	if len(text) == 0 {
		t.Fatal("text-model capabilities are empty, which reads as unknown rather than known text-only")
	}
	// silent-model lists no modalities, so it comes back with nothing at all.
	if caps := capsOf(t, catalog, "silent-model"); len(caps) != 0 {
		t.Fatalf("silent-model capabilities = %v, want none", caps)
	}
	// The tier aliases are the first four rows, in tier order, ahead of the
	// Switch's own entries.
	var names []string
	for _, m := range catalog {
		names = append(names, m.Name)
	}
	want := []string{
		"claude-tier-opus", "claude-tier-sonnet", "claude-tier-haiku", "claude-tier-fable",
		"vision-model", "text-model", "empty-model", "silent-model",
	}
	if !slices.Equal(names, want) {
		t.Fatalf("catalog = %v, want %v", names, want)
	}
}

// A tenant that lists nothing is a failure for both launch readers, not a
// menu of four aliases nothing holds. The launch warns once and gets no rows.
func TestBestEffortCatalogReportsAnEmptyListingAsAFailure(t *testing.T) {
	calls := 0
	srv := newCountingSwitch(t, `{"data":[]}`, &calls)
	useSwitch(t, srv, "test-switch-key")
	ResetModelCatalog()
	t.Cleanup(ResetModelCatalog)

	var warn strings.Builder
	if got := BestEffortCatalog(context.Background(), &warn); got != nil {
		t.Fatalf("catalog = %v, want nil for a tenant that serves none", launchModelNames(got))
	}
	if !strings.Contains(warn.String(), envconfig.BaseURL()) {
		t.Errorf("warning = %q, want it to name the endpoint", warn.String())
	}
}

// The warning must never carry the key, in any form.
func TestBestEffortCatalogWarningOmitsKey(t *testing.T) {
	const key = "sk-switch-secret-value"
	srv, _, _ := switchTestServer(t, `{"error":"nope"}`, http.StatusUnauthorized)
	useSwitch(t, srv.URL, key)
	ResetModelCatalog()
	t.Cleanup(ResetModelCatalog)

	var warn strings.Builder
	BestEffortCatalog(context.Background(), &warn)
	if strings.Contains(warn.String(), key) || strings.Contains(warn.String(), "secret-value") {
		t.Fatalf("warning leaks the key: %q", warn.String())
	}
}

// The Switch decorates every catalog id with Claude Code's [1m] context
// budget suffix, and the launcher sends the bare name. The lookup must match
// across the suffix in both directions, the way it already tolerates :latest,
// or every launch model falls through to the unknown-capabilities path.
func TestFindCatalogModelMatchesAcrossOneMillionSuffix(t *testing.T) {
	catalog, err := parseSwitchCatalog([]byte(`{"data":[
		{"id":"vision-config[1m]","input_modalities":["image","text","video"]},
		{"id":"text-config[1m]","input_modalities":["text"]},
		{"id":"bare-config","input_modalities":["file","text"]}
	]}`))
	if err != nil {
		t.Fatalf("parseSwitchCatalog: %v", err)
	}

	for _, tc := range []struct {
		launch string
		want   []model.Capability
	}{
		{"vision-config", []model.Capability{model.CapabilityVision, model.CapabilityCompletion}},
		{"text-config", []model.Capability{model.CapabilityCompletion}},
		{"bare-config[1m]", []model.Capability{model.CapabilityDocument, model.CapabilityCompletion}},
		{"vision-config[1m]", []model.Capability{model.CapabilityVision, model.CapabilityCompletion}},
	} {
		entry, ok := findCatalogModel(catalog, tc.launch)
		if !ok {
			t.Errorf("findCatalogModel(%q) found nothing; catalog ids: %v", tc.launch, launchModelNames(catalog))
			continue
		}
		if !slices.Equal(entry.Capabilities, tc.want) {
			t.Errorf("findCatalogModel(%q) capabilities = %v, want %v", tc.launch, entry.Capabilities, tc.want)
		}
	}

	if _, ok := findCatalogModel(catalog, "other-config"); ok {
		t.Error("findCatalogModel matched a name the catalog does not hold")
	}
}

// The launch reader is the one that has to see through the Switch's decoration:
// the catalog holds "vision-config[1m]" while the launch names "vision-config",
// and without the tolerant rule every launch model would arrive with unknown
// capabilities and each entry builder would fall back to its permissive list.
func TestBestEffortCatalogPopulatesAcrossOneMillionSuffix(t *testing.T) {
	srv, _, _ := switchTestServer(t, `{"data":[{"id":"vision-config[1m]","input_modalities":["image","text"]}]}`, http.StatusOK)
	useSwitch(t, srv.URL, "test-key")
	ResetModelCatalog()
	t.Cleanup(ResetModelCatalog)

	var warn strings.Builder
	catalog := BestEffortCatalog(context.Background(), &warn)
	if warn.Len() != 0 {
		t.Fatalf("unexpected warning: %s", warn.String())
	}
	if caps := capsOf(t, catalog, "vision-config"); !slices.Contains(caps, model.CapabilityVision) {
		t.Fatalf("vision-config capabilities = %v, want vision from the [1m] catalog entry", caps)
	}
}

// The listing shares the one fetch with the launch readers: a process that
// asked for the catalog once must not ask again.
func TestListSwitchModelsSharesTheOneFetch(t *testing.T) {
	calls := 0
	srv := newCountingSwitch(t, `{"data":[{"id":"cheap[1m]"}]}`, &calls)
	useSwitch(t, srv, "test-switch-key")
	ResetModelCatalog()
	t.Cleanup(ResetModelCatalog)

	for range 2 {
		if _, err := ListSwitchModels(context.Background()); err != nil {
			t.Fatalf("ListSwitchModels: %v", err)
		}
	}
	if _, err := FetchCatalog(context.Background()); err != nil {
		t.Fatalf("FetchCatalog: %v", err)
	}
	if calls != 1 {
		t.Fatalf("the catalog was fetched %d times, want 1", calls)
	}
}

// The name-matching rule, in one place: identical names match, a name matches
// across the [1m] decoration in either position, a name matches across the
// :latest tag the Switch leaves on, and two names that differ by more than a
// decoration do not. A lookup that guessed wrong here loses a model's
// capabilities, tier and description without saying so.
func TestModelNamesSameAcrossDecorations(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"cheap", "cheap", true},
		{"cheap[1m]", "cheap", true},
		{"cheap", "cheap[1m]", true},
		{"cheap:latest", "cheap", true},
		{"cheap:latest[1m]", "cheap", true},
		{"cheap[1m]", "cheap:latest", true},
		{"cheap", "cheaper", false},
		{"cheap", "", false},
		{"cheap", "cheap-preview", false},
	} {
		if got := modelNamesSame(tc.a, tc.b); got != tc.want {
			t.Errorf("modelNamesSame(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// The strip a printed or sent name gets removes the suffix and nothing else:
// ":latest" is a real tag a caller can route by, so stripping it would print a
// name that resolves to a different model than the one listed. Tolerating the
// tag when comparing names is a separate decision, in modelNamesSame.
func TestModelNameWithoutSuffixKeepsTags(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"cheap", "cheap"},
		{"cheap[1m]", "cheap"},
		{"cheap:latest", "cheap:latest"},
		{"cheap:latest[1m]", "cheap:latest"},
		{"", ""},
	} {
		if got := ModelNameWithoutSuffix(tc.name); got != tc.want {
			t.Errorf("ModelNameWithoutSuffix(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A discovery call the Switch refuses must not fall back in silence. The launch
// keeps going without the catalog, but it says so in one stderr line that names
// where the key came from and the status the Switch answered, so a wrong key
// reads as a wrong key rather than as a picker that is merely short.
func TestBestEffortCatalogNamesKeySourceAndStatusOnFailure(t *testing.T) {
	srv, _, _ := switchTestServer(t, `{"error":"invalid api key"}`, http.StatusUnauthorized)
	envconfig.SetBaseURL(srv.URL)
	envconfig.SetAPIKey("")
	envconfig.SetConfigAPIKey("")
	t.Setenv(envconfig.KeyEnvVar, "sk-env-wrong-host")
	ResetModelCatalog()
	t.Cleanup(func() {
		envconfig.SetBaseURL("")
		ResetModelCatalog()
	})

	var warn strings.Builder
	if got := BestEffortCatalog(context.Background(), &warn); got != nil {
		t.Fatalf("catalog = %v, want nil after a 401", got)
	}
	line := warn.String()
	for _, want := range []string{"$PRIZMAL_SWITCH_KEY", "401"} {
		if !strings.Contains(line, want) {
			t.Errorf("warning = %q, want it to contain %q", line, want)
		}
	}
	if strings.Count(strings.TrimSuffix(line, "\n"), "\n") != 0 {
		t.Errorf("warning = %q, want a single line", line)
	}
	if strings.Contains(line, "sk-env-wrong-host") {
		t.Errorf("warning leaked the key value")
	}
}

// The Switch can tag a router config with the Claude tier it serves, and give
// it a description. An unknown tier is dropped rather than guessed at.
func TestParseSwitchCatalogReadsTierAndDescription(t *testing.T) {
	models, err := parseSwitchCatalog([]byte(`{"data":[
		{"id":"smart[1m]","tier":"Opus","description":"Fast and cheap"},
		{"id":"flash[1m]","tier":"mythos"},
		{"id":"plain[1m]"}]}`))
	if err != nil {
		t.Fatalf("parseSwitchCatalog: %v", err)
	}
	if len(models) != 3 {
		t.Fatalf("parsed %d models, want 3", len(models))
	}
	if models[0].Tier != "opus" || models[0].Description != "Fast and cheap" {
		t.Errorf("smart = tier %q, description %q, want opus and the Switch's text", models[0].Tier, models[0].Description)
	}
	if models[1].Tier != "" {
		t.Errorf("flash tier = %q, want an unknown tier dropped", models[1].Tier)
	}
	if models[2].Tier != "" || models[2].Description != "" {
		t.Errorf("plain = %+v, want no tier and no description", models[2])
	}
}

// A description is drawn in the operator's terminal, so the CLI keeps only its
// text: escape sequences and control characters go, and the rest collapses to
// one line.
func TestParseSwitchCatalogCleansTheDescription(t *testing.T) {
	body := `{"data":[{"id":"smart[1m]","description":"Fast\n cheap\u001b]0;title\u0007 \u001b[31mred\u001b[0m\t"}]}`
	models, err := parseSwitchCatalog([]byte(body))
	if err != nil {
		t.Fatalf("parseSwitchCatalog: %v", err)
	}
	if got := models[0].Description; got != "Fast cheap red" {
		t.Fatalf("description = %q, want %q", got, "Fast cheap red")
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
