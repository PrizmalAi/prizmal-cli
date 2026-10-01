package device

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock advances only when Sleep is called, so the poll loop and its
// timeout are deterministic without waiting.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time        { return c.t }
func (c *fakeClock) sleep(d time.Duration) { c.t = c.t.Add(d) }

func TestLoginPollsUntilApproved(t *testing.T) {
	key, _ := GenerateKey()
	start := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	clk := &fakeClock{t: start}

	var polls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The first two polls are pending; the third is the approval.
		if atomic.AddInt32(&polls, 1) < 3 {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"NOT_FOUND"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"device_token": "pz-d-dt-token", "expires_in": 600})
	}))
	defer srv.Close()

	var opened string
	var out strings.Builder
	tok, err := Login(NewClient(srv.URL), key, LoginOptions{
		AuthorizeBaseURL: "https://app.prizmal.ai",
		DeviceName:       "test-mac",
		Sleep:            clk.sleep,
		Now:              clk.now,
		OpenBrowser:      func(u string) error { opened = u; return nil },
		Out:              &out,
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if tok.Value != "pz-d-dt-token" {
		t.Fatalf("token = %q", tok.Value)
	}
	if got := atomic.LoadInt32(&polls); got != 3 {
		t.Fatalf("polled %d times, want 3", got)
	}
	if opened == "" {
		t.Fatal("the browser was never opened")
	}
	// The URL and fingerprint are printed so enrollment works over SSH.
	if !strings.Contains(out.String(), key.Fingerprint()) {
		t.Fatalf("output does not show the fingerprint:\n%s", out.String())
	}
	if !strings.Contains(out.String(), opened) {
		t.Fatalf("output does not print the consent URL:\n%s", out.String())
	}
}

func TestLoginTimesOutWhilePending(t *testing.T) {
	key, _ := GenerateKey()
	clk := &fakeClock{t: time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"NOT_FOUND"}}`))
	}))
	defer srv.Close()

	_, err := Login(NewClient(srv.URL), key, LoginOptions{
		AuthorizeBaseURL: "https://app.prizmal.ai",
		Interval:         time.Second,
		Timeout:          5 * time.Second,
		Sleep:            clk.sleep,
		Now:              clk.now,
		OpenBrowser:      func(string) error { return nil },
	})
	if err == nil {
		t.Fatal("Login returned no error after the timeout")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error = %q, want a timeout message", err)
	}
}

func TestLoginStopsOnReauthRequired(t *testing.T) {
	key, _ := GenerateKey()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"REAUTH_REQUIRED","message":"x"}}`))
	}))
	defer srv.Close()

	_, err := Login(NewClient(srv.URL), key, LoginOptions{
		AuthorizeBaseURL: "https://app.prizmal.ai",
		OpenBrowser:      func(string) error { return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "prizmal login") {
		t.Fatalf("error = %v, want a message that says run prizmal login", err)
	}
}

func TestAuthorizeURLTrimsTrailingSlash(t *testing.T) {
	key, _ := GenerateKey()
	if a, b := AuthorizeURL("https://app.prizmal.ai/", key, "n"), AuthorizeURL("https://app.prizmal.ai", key, "n"); a != b {
		t.Fatalf("trailing slash changed the URL:\n%s\n%s", a, b)
	}
}
