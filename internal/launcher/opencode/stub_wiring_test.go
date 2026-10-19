package opencode

import (
	"context"
	"encoding/json"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/launchtest"
	"slices"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/PrizmalAi/prizmal-cli/internal/model"
	"github.com/PrizmalAi/prizmal-cli/internal/stubserver"
)

// These tests prove each harness's config builder carries the stub server's
// URL and key when pointed at it through envconfig. They do not launch the
// real harness binary (that is CI-only); they prove the wiring is correct
// against a real HTTP endpoint shape.

func TestStubServerOpenCodeWiring(t *testing.T) {
	srv := stubserver.New()
	defer srv.Close()
	envconfig.SetBaseURL(srv.URL)
	envconfig.SetAPIKey(stubserver.StubKey)
	defer func() { envconfig.SetBaseURL(""); envconfig.SetAPIKey("") }()

	models := []launch.LaunchModel{{Name: "prizmal/default"}}
	content, err := buildInlineConfig(models[0], models)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(content), &cfg); err != nil {
		t.Fatal(err)
	}
	provider, _ := cfg["provider"].(map[string]any)
	prizmal, _ := provider["prizmal"].(map[string]any)
	options, _ := prizmal["options"].(map[string]any)
	baseURL, _ := options["baseURL"].(string)
	if want := launchtest.StubEndpoint(srv.URL) + "/v1"; baseURL != want {
		t.Fatalf("baseURL = %q, want %q", baseURL, want)
	}
	apiKey, _ := options["apiKey"].(string)
	if apiKey != stubserver.StubKey {
		t.Fatalf("apiKey = %q, want %q", apiKey, stubserver.StubKey)
	}
}

// TestStubServerCapabilityWiring walks the whole capability path against the
// stub server's real /v1/models response: fetch, parse, and the modalities
// opencode declares for each of the four fixture shapes.
func TestStubServerCapabilityWiring(t *testing.T) {
	srv := stubserver.New()
	defer srv.Close()
	envconfig.SetBaseURL(srv.URL)
	envconfig.SetAPIKey(stubserver.StubKey)
	defer func() { envconfig.SetBaseURL(""); envconfig.SetAPIKey("") }()

	catalog, err := launch.FetchCatalog(context.Background())
	if err != nil {
		t.Fatalf("FetchCatalog: %v", err)
	}

	// The four shapes reach opencode as text-only, image, pdf, and permissive.
	for _, tc := range []struct {
		name string
		want []string
	}{
		{"prizmal/stub", []string{"text"}},
		{"prizmal/stub-vision", []string{"text", "image"}},
		{"prizmal/stub-file", []string{"text", "pdf"}},
		{"prizmal/stub-unknown", []string{"text", "image", "pdf"}},
	} {
		model, ok := launch.FindCatalogModel(catalog, tc.name)
		if !ok {
			t.Fatalf("%s is missing from the stub catalog: %v", tc.name, launch.LaunchModelNames(catalog))
		}
		if got := launch.DeclaredInputs(model, launch.HarnessOpenCode); !slices.Equal(got, tc.want) {
			t.Fatalf("opencode input modalities for %q = %v, want %v", tc.name, got, tc.want)
		}
	}

	// A known text-only entry carries a capability, so it stays distinguishable
	// from an entry the Switch said nothing about.
	textOnly, _ := launch.FindCatalogModel(catalog, "prizmal/stub")
	unknown, _ := launch.FindCatalogModel(catalog, "prizmal/stub-unknown")
	if !textOnly.HasCapability(model.CapabilityCompletion) {
		t.Fatalf("prizmal/stub capabilities = %v, want completion", textOnly.Capabilities)
	}
	if len(unknown.Capabilities) != 0 {
		t.Fatalf("prizmal/stub-unknown capabilities = %v, want unknown", unknown.Capabilities)
	}
}
