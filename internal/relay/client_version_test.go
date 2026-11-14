package relay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
)

// The relay adds the version to every upstream request, whatever the harness
// sent. Cline persists its provider settings, so a header written there would
// go stale; the relay lives for one launch and always reports the running CLI.
func TestRelaySetsTheClientVersionOnEveryRequest(t *testing.T) {
	envconfig.SetClientVersion("1.4.2")
	t.Cleanup(func() { envconfig.SetClientVersion("") })

	var seen []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		seen = append(seen, req.Header.Get("x-prizmal-client-version"))
		if values := req.Header.Values("x-prizmal-client-version"); len(values) > 1 {
			t.Errorf("header sent %d times: %q", len(values), values)
		}
	}))
	defer upstream.Close()

	r, err := New(upstream.URL, func(context.Context) (string, error) { return "tok", nil })
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	for _, preset := range []string{"", "spoofed-by-the-harness"} {
		req, _ := http.NewRequest(http.MethodPost, r.URL()+"/chat/completions", nil)
		req.Header.Set("Authorization", "Bearer "+r.Placeholder())
		if preset != "" {
			req.Header.Set("X-Prizmal-Client-Version", preset)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	if len(seen) != 2 || seen[0] != "1.4.2" || seen[1] != "1.4.2" {
		t.Errorf("upstream saw %q, want 1.4.2 twice", seen)
	}
}

// Without a version the relay still sends one.
func TestRelaySendsDevUnknownWithoutAVersion(t *testing.T) {
	envconfig.SetClientVersion("")
	var got string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		got = req.Header.Get("x-prizmal-client-version")
	}))
	defer upstream.Close()
	r, err := New(upstream.URL, func(context.Context) (string, error) { return "tok", nil })
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	req, _ := http.NewRequest(http.MethodGet, r.URL()+"/models", nil)
	req.Header.Set("Authorization", "Bearer "+r.Placeholder())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if got != "dev-unknown" {
		t.Errorf("header = %q, want dev-unknown", got)
	}
}
