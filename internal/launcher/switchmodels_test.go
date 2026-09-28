package launch

import (
	"context"
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
	m, ok := findLaunchModel(models, name)
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

// A failed fetch warns once and leaves the launch models untouched, so a
// launch never fails because the capability fetch failed.
func TestWithSwitchCapabilitiesWarnsAndContinues(t *testing.T) {
	srv, _, _ := switchTestServer(t, `{"error":"nope"}`, http.StatusInternalServerError)
	useSwitch(t, srv.URL, "test-switch-key")

	var warn strings.Builder
	models := WithSwitchCapabilities(context.Background(), []LaunchModel{{Name: "vision-model"}}, &warn)

	if len(models) != 1 || models[0].Name != "vision-model" {
		t.Fatalf("models = %v, want the input unchanged", models)
	}
	if len(models[0].Capabilities) != 0 {
		t.Fatalf("capabilities = %v, want none after a failed fetch", models[0].Capabilities)
	}
	line := warn.String()
	if !strings.Contains(line, "capabilit") {
		t.Fatalf("warning = %q, want it to name the capability fetch", line)
	}
	if strings.Count(strings.TrimSuffix(line, "\n"), "\n") != 0 {
		t.Fatalf("warning = %q, want a single line", line)
	}
}

// Never call the endpoint without a credential.
func TestWithSwitchCapabilitiesSkipsFetchWithoutKey(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = w.Write([]byte(switchCatalogJSON))
	}))
	defer srv.Close()
	useSwitch(t, srv.URL, "")

	var warn strings.Builder
	models := WithSwitchCapabilities(context.Background(), []LaunchModel{{Name: "vision-model"}}, &warn)

	if called {
		t.Fatal("fetched the catalog with no credential present")
	}
	if len(models[0].Capabilities) != 0 {
		t.Fatalf("capabilities = %v, want none", models[0].Capabilities)
	}
	if warn.String() != "" {
		t.Fatalf("warning = %q, want silence when there is simply no key", warn.String())
	}
}

// The happy path: the requested model picks up the catalog's capabilities,
// and a name the catalog does not list stays unknown.
func TestWithSwitchCapabilitiesPopulatesByName(t *testing.T) {
	srv, _, _ := switchTestServer(t, switchCatalogJSON, http.StatusOK)
	useSwitch(t, srv.URL, "test-switch-key")

	var warn strings.Builder
	models := WithSwitchCapabilities(context.Background(), []LaunchModel{
		{Name: "vision-model"},
		{Name: "text-model"},
		{Name: "not-in-catalog"},
	}, &warn)

	if !models[0].HasCapability(model.CapabilityVision) {
		t.Fatalf("vision-model capabilities = %v, want vision", models[0].Capabilities)
	}
	if models[1].HasCapability(model.CapabilityVision) {
		t.Fatalf("text-model capabilities = %v, want no vision", models[1].Capabilities)
	}
	if len(models[1].Capabilities) == 0 {
		t.Fatal("text-model capabilities are empty, which reads as unknown rather than known text-only")
	}
	if len(models[2].Capabilities) != 0 {
		t.Fatalf("not-in-catalog capabilities = %v, want unknown", models[2].Capabilities)
	}
	if warn.String() != "" {
		t.Fatalf("warning = %q, want none on a successful fetch", warn.String())
	}
}

// An empty launch list is not worth a round trip.
func TestWithSwitchCapabilitiesSkipsEmptyList(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = w.Write([]byte(switchCatalogJSON))
	}))
	defer srv.Close()
	useSwitch(t, srv.URL, "test-switch-key")

	if got := WithSwitchCapabilities(context.Background(), nil, &strings.Builder{}); len(got) != 0 {
		t.Fatalf("models = %v, want empty", got)
	}
	if called {
		t.Fatal("fetched the catalog for an empty model list")
	}
}

// The warning must never carry the key, in any form.
func TestWithSwitchCapabilitiesWarningOmitsKey(t *testing.T) {
	const key = "sk-switch-secret-value"
	srv, _, _ := switchTestServer(t, `{"error":"nope"}`, http.StatusUnauthorized)
	useSwitch(t, srv.URL, key)

	var warn strings.Builder
	WithSwitchCapabilities(context.Background(), []LaunchModel{{Name: "m"}}, &warn)
	if strings.Contains(warn.String(), key) || strings.Contains(warn.String(), "secret-value") {
		t.Fatalf("warning leaks the key: %q", warn.String())
	}
}

// The Switch decorates every catalog id with Claude Code's [1m] context
// budget suffix, and the launcher sends the bare name. The lookup must match
// across the suffix in both directions, the way it already tolerates :latest,
// or every launch model falls through to the unknown-capabilities path.
func TestFindSwitchCatalogModelMatchesAcrossOneMillionSuffix(t *testing.T) {
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
		entry, ok := findSwitchCatalogModel(catalog, tc.launch)
		if !ok {
			t.Errorf("findSwitchCatalogModel(%q) found nothing; catalog ids: %v", tc.launch, launchModelNames(catalog))
			continue
		}
		if !slices.Equal(entry.Capabilities, tc.want) {
			t.Errorf("findSwitchCatalogModel(%q) capabilities = %v, want %v", tc.launch, entry.Capabilities, tc.want)
		}
	}

	if _, ok := findSwitchCatalogModel(catalog, "other-config"); ok {
		t.Error("findSwitchCatalogModel matched a name the catalog does not hold")
	}
}

// WithSwitchCapabilities is the launch path's only reader of the catalog, so
// a suffixed catalog must still populate a bare launch model's capabilities.
func TestWithSwitchCapabilitiesPopulatesAcrossOneMillionSuffix(t *testing.T) {
	srv, _, _ := switchTestServer(t, `{"data":[{"id":"vision-config[1m]","input_modalities":["image","text"]}]}`, http.StatusOK)
	useSwitch(t, srv.URL, "test-key")

	var warn strings.Builder
	models := WithSwitchCapabilities(context.Background(), []LaunchModel{{Name: "vision-config"}}, &warn)
	if warn.Len() != 0 {
		t.Fatalf("unexpected warning: %s", warn.String())
	}
	if caps := capsOf(t, models, "vision-config"); !slices.Contains(caps, model.CapabilityVision) {
		t.Fatalf("vision-config capabilities = %v, want vision from the [1m] catalog entry", caps)
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
