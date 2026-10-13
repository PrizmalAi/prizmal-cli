package envconfig

import (
	"strings"
	"testing"
)

// endpointShapes is the whole contract of the three shapes, over the inputs
// that broke them: a loopback address, a base carrying the version, that base
// with the trailing slash a user types, and a plain host. Before the shapes
// existed each adapter trimmed and appended its own way, so the same input
// produced /v1/v1 for four harnesses and the right URL for the other two, and
// 127.0.0.1 or localhost depending on which adapter ran. These are the only
// three spellings any caller gets, so a fourth rule appearing anywhere else is
// a defect this table cannot see — the adapters' own table test is what holds
// them to it.
func TestEndpointShapes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		base     string
		catalog  string
		stripped string
		openAI   string
	}{
		{
			name:     "plain host",
			base:     "https://switch.example.com",
			catalog:  "https://switch.example.com/v1/models",
			stripped: "https://switch.example.com",
			openAI:   "https://switch.example.com/v1",
		},
		{
			name:     "trailing slash is trimmed",
			base:     "https://switch.example.com/",
			catalog:  "https://switch.example.com/v1/models",
			stripped: "https://switch.example.com",
			openAI:   "https://switch.example.com/v1",
		},
		{
			name:     "a base carrying v1 keeps exactly one",
			base:     "https://switch.example.com/v1",
			catalog:  "https://switch.example.com/v1/models",
			stripped: "https://switch.example.com",
			openAI:   "https://switch.example.com/v1",
		},
		{
			name:     "v1 with a trailing slash is the same base",
			base:     "https://switch.example.com/v1/",
			catalog:  "https://switch.example.com/v1/models",
			stripped: "https://switch.example.com",
			openAI:   "https://switch.example.com/v1",
		},
		{
			name:     "two v1 collapse to one",
			base:     "https://switch.example.com/v1/v1",
			catalog:  "https://switch.example.com/v1/models",
			stripped: "https://switch.example.com",
			openAI:   "https://switch.example.com/v1",
		},
		{
			name:     "v1 inside the path is not a version suffix",
			base:     "https://switch.example.com/v1/models",
			catalog:  "https://switch.example.com/v1/models/v1/models",
			stripped: "https://switch.example.com/v1/models",
			openAI:   "https://switch.example.com/v1/models/v1",
		},
		{
			name:     "loopback is spelled localhost",
			base:     "http://127.0.0.1:8080",
			catalog:  "http://localhost:8080/v1/models",
			stripped: "http://localhost:8080",
			openAI:   "http://localhost:8080/v1",
		},
		{
			name:     "the wildcard bind is loopback too",
			base:     "http://0.0.0.0:8080",
			catalog:  "http://localhost:8080/v1/models",
			stripped: "http://localhost:8080",
			openAI:   "http://localhost:8080/v1",
		},
		{
			name:     "localhost stays localhost",
			base:     "http://localhost:8080",
			catalog:  "http://localhost:8080/v1/models",
			stripped: "http://localhost:8080",
			openAI:   "http://localhost:8080/v1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			SetBaseURL(tc.base)
			t.Cleanup(func() { SetBaseURL("") })

			if got := CatalogURL(); got != tc.catalog {
				t.Errorf("CatalogURL() = %q, want %q", got, tc.catalog)
			}
			if got := UnversionedBaseURL(); got != tc.stripped {
				t.Errorf("UnversionedBaseURL() = %q, want %q", got, tc.stripped)
			}
			if got := OpenAIBaseURL(false); got != tc.openAI {
				t.Errorf("OpenAIBaseURL(false) = %q, want %q", got, tc.openAI)
			}
			// The slash is a formatting choice of one consumer, not a second
			// version rule, so it is the same string plus the one character.
			if got, want := OpenAIBaseURL(true), tc.openAI+"/"; got != want {
				t.Errorf("OpenAIBaseURL(true) = %q, want %q", got, want)
			}
			for name, got := range map[string]string{
				"CatalogURL":         CatalogURL(),
				"UnversionedBaseURL": UnversionedBaseURL(),
				"OpenAIBaseURL":      OpenAIBaseURL(false),
			} {
				if strings.Contains(got, "/v1/v1") {
					t.Errorf("%s() = %q, which asks the Switch for a doubled version", name, got)
				}
			}
		})
	}
}

// A shape must read the same base whichever source supplied it: --url, the
// environment, or the config file all arrive as one resolved string, but the
// config file is the one that reaches SetConfigBaseURL and is the shape most
// likely to be hand-edited with the version left on the end.
func TestEndpointShapesFollowTheConfigFileBaseURL(t *testing.T) {
	SetConfigBaseURL("https://switch.example.com/v1/")
	t.Cleanup(func() { SetConfigBaseURL("") })

	if got := OpenAIBaseURL(false); got != "https://switch.example.com/v1" {
		t.Errorf("OpenAIBaseURL(false) = %q, want https://switch.example.com/v1", got)
	}
	if got := CatalogURL(); got != "https://switch.example.com/v1/models" {
		t.Errorf("CatalogURL() = %q, want https://switch.example.com/v1/models", got)
	}
}
