package opencode

import (
	"context"
	"encoding/json"
	"io"
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
	if baseURL != srv.URL+"/v1" {
		t.Fatalf("baseURL = %q, want %q", baseURL, srv.URL+"/v1")
	}
	apiKey, _ := options["apiKey"].(string)
	if apiKey != stubserver.StubKey {
		t.Fatalf("apiKey = %q, want %q", apiKey, stubserver.StubKey)
	}
}

// TestStubServerCapabilityWiring walks the whole capability path against the
// stub server's real /v1/models response: fetch, parse, and the opencode entry
// each of the three fixture shapes produces.
func TestStubServerCapabilityWiring(t *testing.T) {
	srv := stubserver.New()
	defer srv.Close()
	envconfig.SetBaseURL(srv.URL)
	envconfig.SetAPIKey(stubserver.StubKey)
	defer func() { envconfig.SetBaseURL(""); envconfig.SetAPIKey("") }()

	models := launch.WithSwitchCapabilities(context.Background(), []launch.LaunchModel{
		{Name: "prizmal/stub"},
		{Name: "prizmal/stub-vision"},
		{Name: "prizmal/stub-file"},
		{Name: "prizmal/stub-unknown"},
	}, io.Discard)

	if models[0].HasCapability(model.CapabilityVision) {
		t.Fatalf("prizmal/stub capabilities = %v, want no vision", models[0].Capabilities)
	}
	if len(models[0].Capabilities) == 0 {
		t.Fatal("prizmal/stub capabilities are empty, so a known text-only entry is indistinguishable from an unknown one")
	}
	if !models[1].HasCapability(model.CapabilityVision) {
		t.Fatalf("prizmal/stub-vision capabilities = %v, want vision", models[1].Capabilities)
	}
	if !models[2].HasCapability(model.CapabilityDocument) {
		t.Fatalf("prizmal/stub-file capabilities = %v, want document", models[2].Capabilities)
	}
	if models[2].HasCapability(model.CapabilityVision) {
		t.Fatalf("prizmal/stub-file capabilities = %v, want no vision", models[2].Capabilities)
	}
	if len(models[3].Capabilities) != 0 {
		t.Fatalf("prizmal/stub-unknown capabilities = %v, want unknown", models[3].Capabilities)
	}

	// The four shapes reach opencode as text-only, image, pdf, and permissive.
	for _, tc := range []struct {
		m    launch.LaunchModel
		want []string
	}{
		{models[0], []string{"text"}},
		{models[1], []string{"text", "image"}},
		{models[2], []string{"text", "pdf"}},
		{models[3], []string{"text", "image", "pdf"}},
	} {
		if got := openCodeInputModalities(tc.m); !slices.Equal(got, tc.want) {
			t.Fatalf("opencode input modalities for %q = %v, want %v", tc.m.Name, got, tc.want)
		}
	}
}
