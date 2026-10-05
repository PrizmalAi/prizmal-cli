package device

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHelperReusesCachedTokenWithFiveMinutesLeft(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	// At least CacheValidMargin (5m) remains when the margin is included.
	cached := &CachedToken{Token: "pz-d-dt-cached", Expiry: now.Add(5*time.Minute + time.Second)}

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	key, _ := GenerateKey()
	got, out, err := HelperToken(NewClient(srv.URL), key, now,
		func() *CachedToken { return cached },
		nil)
	if err != nil {
		t.Fatalf("HelperToken: %v", err)
	}
	if got != cached.Token {
		t.Fatalf("token = %q, want the cached one", got)
	}
	if out != cached {
		t.Fatal("the cache was replaced")
	}
	if hits != 0 {
		t.Fatalf("refreshed %d times despite a valid cached token", hits)
	}
}

func TestHelperRefreshesWhenTokenNearlyExpired(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	cached := &CachedToken{Token: "pz-d-dt-old", Expiry: now.Add(4 * time.Minute)}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"device_token":"pz-d-dt-fresh","expires_in":600}`))
	}))
	defer srv.Close()

	key, _ := GenerateKey()
	got, ct, err := HelperToken(NewClient(srv.URL), key, now,
		func() *CachedToken { return cached },
		nil)
	if err != nil {
		t.Fatalf("HelperToken: %v", err)
	}
	if got != "pz-d-dt-fresh" {
		t.Fatalf("token = %q, want the fresh one", got)
	}
	if ct.Token != "pz-d-dt-fresh" || !ct.Expiry.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("cache = %+v, want the fresh token and expiry", ct)
	}
}

func TestHelperRateLimitedReadsParallelWinnersCache(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	// The first read finds nothing; after the sleep a parallel helper has
	// written a token that replaces it.
	var reads int
	load := func() *CachedToken {
		reads++
		if reads == 1 {
			return nil
		}
		return &CachedToken{Token: "pz-d-dt-winner", Expiry: now.Add(10 * time.Minute)}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":"CONFLICT"}}`))
	}))
	defer srv.Close()

	slept := 0
	key, _ := GenerateKey()
	got, _, err := HelperToken(NewClient(srv.URL), key, now, load, func(time.Duration) { slept++ })
	if err != nil {
		t.Fatalf("HelperToken: %v", err)
	}
	if got != "pz-d-dt-winner" {
		t.Fatalf("token = %q, want the parallel helper's token", got)
	}
	if slept != 1 {
		t.Fatalf("slept %d times, want 1", slept)
	}
}

func TestHelper503FallsBackToUnexpiredCache(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	// Less than the margin left, so the helper tries to refresh...
	cached := &CachedToken{Token: "pz-d-dt-still-good", Expiry: now.Add(2 * time.Minute)}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":"UNAVAILABLE"}}`))
	}))
	defer srv.Close()

	key, _ := GenerateKey()
	got, _, err := HelperToken(NewClient(srv.URL), key, now, func() *CachedToken { return cached }, nil)
	if err != nil {
		t.Fatalf("HelperToken: %v", err)
	}
	// ...but it has not expired, so the helper prints it rather than break.
	if got != cached.Token {
		t.Fatalf("token = %q, want the still-valid cached token", got)
	}
}

func TestHelperPropagatesReauthRequired(t *testing.T) {
	now := time.Now()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"REAUTH_REQUIRED"}}`))
	}))
	defer srv.Close()

	key, _ := GenerateKey()
	_, _, err := HelperToken(NewClient(srv.URL), key, now, func() *CachedToken { return nil }, nil)
	if err != ErrReauthRequired {
		t.Fatalf("err = %v, want ErrReauthRequired", err)
	}
}

func TestValidHelperOutput(t *testing.T) {
	if !ValidHelperOutput("pz-d-dt-abc.def") {
		t.Fatal("a normal token was refused")
	}
	if ValidHelperOutput("") {
		t.Fatal("an empty token was accepted")
	}
	if ValidHelperOutput("has\nnewline") {
		t.Fatal("a non-printable token was accepted")
	}
	if ValidHelperOutput(strings.Repeat("a", MaxHelperOutput+1)) {
		t.Fatal("a token over the 16384-character limit was accepted")
	}
	if !ValidHelperOutput(strings.Repeat("a", MaxHelperOutput)) {
		t.Fatal("a token exactly at the limit was refused")
	}
}

func TestReauthWarning(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	if got := ReauthWarning(nil, now); got != "" {
		t.Fatalf("a nil deadline warned: %q", got)
	}
	if got := ReauthWarning(ptr(now.Add(48*time.Hour)), now); got != "" {
		t.Fatalf("a deadline 48h out warned too early: %q", got)
	}
	if got := ReauthWarning(ptr(now.Add(20*time.Hour)), now); !strings.Contains(got, "prizmal login") {
		t.Fatalf("a deadline 20h out did not warn: %q", got)
	}
	if got := ReauthWarning(ptr(now.Add(90*time.Minute)), now); !strings.Contains(got, "1 hour") {
		t.Fatalf("a 90-minute deadline = %q, want an hours count", got)
	}
	// A deadline already past gets its own sentence: "expires in less than an
	// hour (already due)" contradicts itself, so the expired case says due
	// outright.
	if got := ReauthWarning(ptr(now.Add(-time.Hour)), now); !strings.Contains(got, "past its deadline") || strings.Contains(got, "expires in") {
		t.Fatalf("an expired deadline = %q, want the due sentence without the expiry phrasing", got)
	}
}

func ptr(t time.Time) *time.Time { return &t }
