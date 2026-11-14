package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/config"
	"github.com/PrizmalAi/prizmal-cli/internal/device"
)

// configStub answers GET /v1/cli/config with the given status and body, and
// records every request it sees. The returned function reads the log under the
// lock the handler writes it with.
func configStub(t *testing.T, status int, body string) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), paths...)
	}
}

// The approval page comes from the Switch, so the CLI holds no environment
// logic. The login asks the Switch and uses what it returns.
func TestLoginAsksTheSwitchForTheApprovalPage(t *testing.T) {
	useTempHome(t)
	srv, paths := configStub(t, http.StatusOK, `{"approval_url":"https://app.example.invalid/cli/authorize"}`)

	got := approvalURL(device.NewClient(srv.URL), &bytes.Buffer{})
	if got != "https://app.example.invalid/cli/authorize" {
		t.Fatalf("approvalURL = %q; want the Switch's approval_url", got)
	}
	if got := paths(); len(got) != 1 || got[0] != "GET /v1/cli/config" {
		t.Fatalf("requests = %v, want one GET /v1/cli/config", got)
	}
}

// $PRIZMAL_APP_URL no longer names the approval page. A stale value must not
// send the operator to the wrong place.
func TestApprovalURLIgnoresTheOldEnvironmentVariable(t *testing.T) {
	useTempHome(t)
	t.Setenv("PRIZMAL_APP_URL", "https://stale.example.invalid")
	srv, _ := configStub(t, http.StatusOK, `{"approval_url":"https://app.example.invalid/cli/authorize"}`)

	got := approvalURL(device.NewClient(srv.URL), &bytes.Buffer{})
	if got != "https://app.example.invalid/cli/authorize" {
		t.Fatalf("approvalURL = %q; want the Switch's answer, not $PRIZMAL_APP_URL", got)
	}
}

// A config file written for an older CLI still has app_url. It loads, and the
// field changes nothing.
func TestOldConfigFileWithAppURLStillLoadsAndIsIgnored(t *testing.T) {
	home := useTempHome(t)
	resetAppURLState(t)
	dir := filepath.Join(home, ".prizmal")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeJSONFile(t, filepath.Join(dir, "config.json"), map[string]any{
		"version": 1, "base_url": "https://api.example.invalid", "app_url": "https://stale.example.invalid",
	})

	if err := applyURLPrecedence("", ""); err != nil {
		t.Fatalf("applyURLPrecedence: %v", err)
	}
	cfg, err := config.Load()
	if err != nil || cfg.BaseURL != "https://api.example.invalid" {
		t.Fatalf("config = %+v, %v; want the file's base_url read", cfg, err)
	}
	srv, _ := configStub(t, http.StatusOK, `{"approval_url":"https://app.example.invalid/cli/authorize"}`)
	if got := approvalURL(device.NewClient(srv.URL), &bytes.Buffer{}); strings.Contains(got, "stale") {
		t.Fatalf("approvalURL = %q; the old app_url must not be used", got)
	}
}

// When the Switch cannot report an approval page, the login does not stop: it
// uses the default page and says so. That covers a Switch with no such route, an
// empty or invalid answer, a failing Switch and one that cannot be reached.
func TestApprovalURLFallsBackToTheDefault(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"no route", http.StatusNotFound, `{"error":{"message":"not found"}}`},
		{"key required", http.StatusUnauthorized, `{"error":{"message":"API key is required."}}`},
		{"empty field", http.StatusOK, `{"approval_url":""}`},
		{"no field", http.StatusOK, `{}`},
		{"not a url", http.StatusOK, `{"approval_url":"app.example.invalid"}`},
		{"not json", http.StatusOK, `<html>`},
		{"server error", http.StatusInternalServerError, `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			useTempHome(t)
			srv, _ := configStub(t, tc.status, tc.body)
			var out bytes.Buffer
			if got := approvalURL(device.NewClient(srv.URL), &out); got != defaultApprovalURL {
				t.Fatalf("approvalURL = %q, want the default %q", got, defaultApprovalURL)
			}
			if !strings.Contains(out.String(), "default approval page") {
				t.Fatalf("output %q must say the default page is in use", out.String())
			}
		})
	}
}

// A Switch that cannot be reached at all falls back too.
func TestApprovalURLFallsBackWhenTheSwitchIsUnreachable(t *testing.T) {
	useTempHome(t)
	srv, _ := configStub(t, http.StatusOK, `{}`)
	url := srv.URL
	srv.Close()

	var out bytes.Buffer
	if got := approvalURL(device.NewClient(url), &out); got != defaultApprovalURL {
		t.Fatalf("approvalURL = %q, want the default %q", got, defaultApprovalURL)
	}
	if !strings.Contains(out.String(), "default approval page") {
		t.Fatalf("output %q must say the default page is in use", out.String())
	}
}

// A valid answer wins over the default, and prints nothing about a fallback.
func TestApprovalURLPrefersTheSwitchsAnswer(t *testing.T) {
	useTempHome(t)
	srv, _ := configStub(t, http.StatusOK, `{"approval_url":"https://app.example.invalid/cli/authorize"}`)
	var out bytes.Buffer
	if got := approvalURL(device.NewClient(srv.URL), &out); got != "https://app.example.invalid/cli/authorize" {
		t.Fatalf("approvalURL = %q, want the Switch's answer", got)
	}
	if out.Len() != 0 {
		t.Fatalf("output %q, want nothing when the Switch answers", out.String())
	}
}

// With no approval page from the Switch, the login still gets a key and goes on.
func TestLoginWithoutAnApprovalPageStillMakesAKey(t *testing.T) {
	useTempHome(t)
	answerPrompts(t, false, "")
	srv, _ := configStub(t, http.StatusNotFound, `{}`)
	var out bytes.Buffer

	appURL, key, created, _, err := prepareLogin(device.NewClient(srv.URL), &out)
	if err != nil || key == nil || !created || appURL != defaultApprovalURL {
		t.Fatalf("prepareLogin = %q, %v, %v, %v; want the default page and a new key", appURL, key != nil, created, err)
	}
}
