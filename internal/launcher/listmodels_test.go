package launch

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// A Switch entry carries the decorated id and the bare spelling in `name` and
// `display_name`. The id is what the listing reads, and stripping its [1m]
// suffix yields exactly the name the Switch sends, so the listed name is the
// one a caller passes back to route.
func TestParseSwitchCatalogStripsTheSuffixOffTheID(t *testing.T) {
	models, err := ParseSwitchCatalog([]byte(`{"data":[
		{"id":"cheap[1m]","name":"cheap","display_name":"cheap"},
		{"id":"opus[1m]","name":"opus","display_name":"opus"}
	]}`))
	if err != nil {
		t.Fatalf("ParseSwitchCatalog: %v", err)
	}
	if got := LaunchModelNames(models); len(got) != 2 || got[0] != "cheap" || got[1] != "opus" {
		t.Fatalf("names = %v, want [cheap opus]", got)
	}
}

// The id is the field that routes, and name is a label for people. This CLI
// points at arbitrary endpoints, and an OpenAI-style catalog answers with both:
// OpenRouter sends {"id":"openai/gpt-4o-mini","name":"OpenAI: GPT-4o-mini"}.
// Printing the label would hand the caller a string no request accepts.
func TestParseSwitchCatalogIgnoresALabelOnlyName(t *testing.T) {
	models, err := ParseSwitchCatalog([]byte(`{"data":[
		{"id":"openai/gpt-4o-mini","name":"OpenAI: GPT-4o-mini"},
		{"id":"anthropic/claude-sonnet-5","display_name":"Anthropic: Claude Sonnet 5"}
	]}`))
	if err != nil {
		t.Fatalf("ParseSwitchCatalog: %v", err)
	}
	got := LaunchModelNames(models)
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
	models, err := ParseSwitchCatalog([]byte(`{"data":[{"id":"gpt-4o-mini"},{"id":"cheap[1m]"}]}`))
	if err != nil {
		t.Fatalf("ParseSwitchCatalog: %v", err)
	}
	if got := LaunchModelNames(models); len(got) != 2 || got[0] != "gpt-4o-mini" || got[1] != "cheap" {
		t.Fatalf("names = %v, want [gpt-4o-mini cheap]", got)
	}
}

// Only the [1m] suffix comes off an id. A :latest tag is a real tag that routes
// to a different model than the bare name does, so stripping it would print a
// name the caller cannot use to reach what was listed. The launch path strips
// it when matching because matching tolerates an implicit tag; a listing that
// prints names must not.
func TestParseSwitchCatalogKeepsARealTagOnTheID(t *testing.T) {
	models, err := ParseSwitchCatalog([]byte(`{"data":[{"id":"mymodel:latest"},{"id":"other:v2"}]}`))
	if err != nil {
		t.Fatalf("ParseSwitchCatalog: %v", err)
	}
	got := LaunchModelNames(models)
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
	models, err := ParseSwitchCatalog([]byte(`{"data":[{"name":"a label with no id"},{"id":"cheap[1m]"}]}`))
	if err != nil {
		t.Fatalf("ParseSwitchCatalog: %v", err)
	}
	if got := LaunchModelNames(models); len(got) != 1 || got[0] != "cheap" {
		t.Fatalf("names = %v, want [cheap]", got)
	}
}

// An entry naming nothing at all is skipped rather than listed as an empty
// line, which a caller would pass back as an empty model.
func TestParseSwitchCatalogSkipsEntriesWithNoName(t *testing.T) {
	models, err := ParseSwitchCatalog([]byte(`{"data":[{"id":""},{"id":"cheap[1m]","name":"cheap"}]}`))
	if err != nil {
		t.Fatalf("ParseSwitchCatalog: %v", err)
	}
	if got := LaunchModelNames(models); len(got) != 1 || got[0] != "cheap" {
		t.Fatalf("names = %v, want [cheap]", got)
	}
}

// ListSwitchModels answers the same question the picker does, so the tier
// aliases the Switch does not list, for the tiers its configs hold, lead the way they lead --pick. The
// Switch's own entries keep its order underneath: `default` first and the rest
// sorted, and re-sorting here would fight that.
func TestListSwitchModelsOffersTheHeldClaudeTiersFirst(t *testing.T) {
	srv, _, _ := switchTestServer(t, `{"data":[
		{"id":"default[1m]","name":"default"},
		{"id":"alpha[1m]","name":"alpha","tier":"opus"},
		{"id":"zeta[1m]","name":"zeta","tier":"haiku"}
	]}`, http.StatusOK)
	useSwitch(t, srv.URL, "test-switch-key")
	ResetModelCatalog()
	t.Cleanup(ResetModelCatalog)

	names, err := ListSwitchModels(context.Background())
	if err != nil {
		t.Fatalf("ListSwitchModels: %v", err)
	}
	want := []string{"claude-tier-opus", "claude-tier-haiku", "default", "alpha", "zeta"}
	if !slices.Equal(names, want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
}

// A listing and a picker must report the same routable set. `--list` used to
// read the raw catalog while `--pick` went through withClaudeTiers, so an
// operator scripting `--list` to build a menu missed four names that
// `prizmal --model <tier alias>` launches. Two readers of one question is what
// let the README's claim drift away from both.
func TestListSwitchModelsAgreesWithThePicker(t *testing.T) {
	srv, _, _ := switchTestServer(t, `{"data":[
		{"id":"team-opus-blend[1m]","tier":"opus"},
		{"id":"smart[1m]"}
	]}`, http.StatusOK)
	useSwitch(t, srv.URL, "test-switch-key")
	ResetModelCatalog()
	t.Cleanup(ResetModelCatalog)

	names, err := ListSwitchModels(context.Background())
	if err != nil {
		t.Fatalf("ListSwitchModels: %v", err)
	}
	catalog, err := FetchCatalog(context.Background())
	if err != nil {
		t.Fatalf("FetchCatalog: %v", err)
	}
	var pick []string
	for _, m := range catalog {
		pick = append(pick, m.Name)
	}
	if !slices.Equal(names, pick) {
		t.Fatalf("--list reports %v but --pick offers %v", names, pick)
	}
}

// A tenant that lists nothing has no models, which is not a failed fetch: a
// picker must be able to tell the two apart, and so must a listing, whose empty
// answer exits 0 with a message.
func TestListSwitchModelsReportsNoModelsForAnEmptyListing(t *testing.T) {
	srv, _, _ := switchTestServer(t, `{"data":[]}`, http.StatusOK)
	useSwitch(t, srv.URL, "test-switch-key")
	ResetModelCatalog()
	t.Cleanup(ResetModelCatalog)

	if _, err := ListSwitchModels(context.Background()); !errors.Is(err, ErrNoModels) {
		t.Fatalf("ListSwitchModels on an empty listing = %v, want ErrNoModels", err)
	}
}

// A listing has no graceful-degradation path: an empty list is indistinguishable
// from a tenant that serves nothing, so a failed fetch is an error the caller
// turns into a non-zero exit.
func TestListSwitchModelsFailsOnABadStatus(t *testing.T) {
	srv, _, _ := switchTestServer(t, `{"error":"nope"}`, http.StatusInternalServerError)
	useSwitch(t, srv.URL, "test-switch-key")
	ResetModelCatalog()
	t.Cleanup(ResetModelCatalog)

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
	ResetModelCatalog()
	t.Cleanup(ResetModelCatalog)

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
	ResetModelCatalog()
	t.Cleanup(ResetModelCatalog)

	_, err := ListSwitchModels(context.Background())
	if err == nil {
		t.Fatal("ListSwitchModels accepted a 401")
	}
	if strings.Contains(err.Error(), "sk-secret-value") {
		t.Fatalf("error carries the key: %v", err)
	}
}
