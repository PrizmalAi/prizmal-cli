package launch

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// A Switch entry carries the decorated id and the bare spelling in `name` and
// `display_name`. The id is what the listing reads, and stripping its [1m]
// suffix yields exactly the name the Switch sends, so the listed name is the
// one a caller passes back to route.
func TestParseSwitchCatalogStripsTheSuffixOffTheID(t *testing.T) {
	models, err := parseSwitchCatalog([]byte(`{"data":[
		{"id":"cheap[1m]","name":"cheap","display_name":"cheap"},
		{"id":"opus[1m]","name":"opus","display_name":"opus"}
	]}`))
	if err != nil {
		t.Fatalf("parseSwitchCatalog: %v", err)
	}
	if got := launchModelNames(models); len(got) != 2 || got[0] != "cheap" || got[1] != "opus" {
		t.Fatalf("names = %v, want [cheap opus]", got)
	}
}

// The id is the field that routes, and name is a label for people. This CLI
// points at arbitrary endpoints, and an OpenAI-style catalog answers with both:
// OpenRouter sends {"id":"openai/gpt-4o-mini","name":"OpenAI: GPT-4o-mini"}.
// Printing the label would hand the caller a string no request accepts.
func TestParseSwitchCatalogIgnoresALabelOnlyName(t *testing.T) {
	models, err := parseSwitchCatalog([]byte(`{"data":[
		{"id":"openai/gpt-4o-mini","name":"OpenAI: GPT-4o-mini"},
		{"id":"anthropic/claude-sonnet-5","display_name":"Anthropic: Claude Sonnet 5"}
	]}`))
	if err != nil {
		t.Fatalf("parseSwitchCatalog: %v", err)
	}
	got := launchModelNames(models)
	want := []string{"openai/gpt-4o-mini", "anthropic/claude-sonnet-5"}
	if len(got) != len(want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("names = %v, want %v", got, want)
		}
	}
}

// A provider that serves a plain OpenAI-style catalog sends ids and nothing
// else. The id is then the routable name, with the [1m] decoration stripped:
// the Switch's own decoration is the only thing removed, and only because a
// bare spelling routes for every dialect.
func TestParseSwitchCatalogFallsBackToTheIDWithoutDecoration(t *testing.T) {
	models, err := parseSwitchCatalog([]byte(`{"data":[{"id":"gpt-4o-mini"},{"id":"cheap[1m]"}]}`))
	if err != nil {
		t.Fatalf("parseSwitchCatalog: %v", err)
	}
	if got := launchModelNames(models); len(got) != 2 || got[0] != "gpt-4o-mini" || got[1] != "cheap" {
		t.Fatalf("names = %v, want [gpt-4o-mini cheap]", got)
	}
}

// Only the [1m] suffix comes off an id. A :latest tag is a real tag that routes
// to a different model than the bare name does, so stripping it would print a
// name the caller cannot use to reach what was listed. The launch path strips
// it when matching because matching tolerates an implicit tag; a listing that
// prints names must not.
func TestParseSwitchCatalogKeepsARealTagOnTheID(t *testing.T) {
	models, err := parseSwitchCatalog([]byte(`{"data":[{"id":"mymodel:latest"},{"id":"other:v2"}]}`))
	if err != nil {
		t.Fatalf("parseSwitchCatalog: %v", err)
	}
	got := launchModelNames(models)
	want := []string{"mymodel:latest", "other:v2"}
	if len(got) != len(want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("names = %v, want %v", got, want)
		}
	}
}

// An entry with no id names nothing routable, so it is skipped rather than
// listed as an empty line a caller would pass back as an empty model.
func TestParseSwitchCatalogSkipsAnEntryWithNoID(t *testing.T) {
	models, err := parseSwitchCatalog([]byte(`{"data":[{"name":"a label with no id"},{"id":"cheap[1m]"}]}`))
	if err != nil {
		t.Fatalf("parseSwitchCatalog: %v", err)
	}
	if got := launchModelNames(models); len(got) != 1 || got[0] != "cheap" {
		t.Fatalf("names = %v, want [cheap]", got)
	}
}

// An entry naming nothing at all is skipped rather than listed as an empty
// line, which a caller would pass back as an empty model.
func TestParseSwitchCatalogSkipsEntriesWithNoName(t *testing.T) {
	models, err := parseSwitchCatalog([]byte(`{"data":[{"id":""},{"id":"cheap[1m]","name":"cheap"}]}`))
	if err != nil {
		t.Fatalf("parseSwitchCatalog: %v", err)
	}
	if got := launchModelNames(models); len(got) != 1 || got[0] != "cheap" {
		t.Fatalf("names = %v, want [cheap]", got)
	}
}

// ListSwitchModels returns the names in the order the Switch listed them, which
// is `default` first and the rest sorted; re-sorting here would fight that.
func TestListSwitchModelsKeepsTheSwitchOrder(t *testing.T) {
	srv, _, _ := switchTestServer(t, `{"data":[
		{"id":"default[1m]","name":"default"},
		{"id":"alpha[1m]","name":"alpha"},
		{"id":"zeta[1m]","name":"zeta"}
	]}`, http.StatusOK)
	useSwitch(t, srv.URL, "test-switch-key")

	names, err := ListSwitchModels(context.Background())
	if err != nil {
		t.Fatalf("ListSwitchModels: %v", err)
	}
	if len(names) != 3 || names[0] != "default" || names[1] != "alpha" || names[2] != "zeta" {
		t.Fatalf("names = %v, want [default alpha zeta]", names)
	}
}

// A listing has no graceful-degradation path: an empty list is indistinguishable
// from a tenant that serves nothing, so a failed fetch is an error the caller
// turns into a non-zero exit.
func TestListSwitchModelsFailsOnABadStatus(t *testing.T) {
	srv, _, _ := switchTestServer(t, `{"error":"nope"}`, http.StatusInternalServerError)
	useSwitch(t, srv.URL, "test-switch-key")

	if _, err := ListSwitchModels(context.Background()); err == nil {
		t.Fatal("ListSwitchModels accepted a 500")
	}
}

// The fetch authenticates as the switch key, which is what makes the answer
// that tenant's: the Switch resolves the tenant from the key, never from a
// parameter, so this header is the only thing selecting whose models come back.
func TestListSwitchModelsSendsTheSwitchKey(t *testing.T) {
	srv, gotAuth, gotPath := switchTestServer(t, `{"data":[{"id":"default[1m]","name":"default"}]}`, http.StatusOK)
	useSwitch(t, srv.URL, "test-switch-key")

	if _, err := ListSwitchModels(context.Background()); err != nil {
		t.Fatalf("ListSwitchModels: %v", err)
	}
	if *gotAuth != "Bearer test-switch-key" {
		t.Fatalf("Authorization = %q, want a bearer token", *gotAuth)
	}
	if *gotPath != "/v1/models" {
		t.Fatalf("path = %q, want /v1/models", *gotPath)
	}
}

// The fetch error must carry the endpoint and no key material, since it reaches
// the operator's terminal through main's error line.
func TestListSwitchModelsErrorNamesNoKey(t *testing.T) {
	srv, _, _ := switchTestServer(t, `{"error":{"message":"boom"}}`, http.StatusUnauthorized)
	useSwitch(t, srv.URL, "sk-secret-value")

	_, err := ListSwitchModels(context.Background())
	if err == nil {
		t.Fatal("ListSwitchModels accepted a 401")
	}
	if strings.Contains(err.Error(), "sk-secret-value") {
		t.Fatalf("error carries the key: %v", err)
	}
}
