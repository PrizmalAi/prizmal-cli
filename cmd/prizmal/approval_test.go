package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/config"
	"github.com/PrizmalAi/prizmal-cli/internal/device"
)

// configStub answers GET /v1/cli/config with the given status and body, and
// records every request path it sees.
func configStub(t *testing.T, status int, body string) (*httptest.Server, *[]string) {
	t.Helper()
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &paths
}

// The approval page comes from the Switch, so the CLI holds no environment
// logic. The login asks the Switch and uses what it returns.
func TestLoginAsksTheSwitchForTheApprovalPage(t *testing.T) {
	useTempHome(t)
	srv, paths := configStub(t, http.StatusOK, `{"approval_url":"https://app.example.invalid/cli/authorize"}`)

	got, err := approvalURL(device.NewClient(srv.URL))
	if err != nil || got != "https://app.example.invalid/cli/authorize" {
		t.Fatalf("approvalURL = %q, %v; want the Switch's approval_url", got, err)
	}
	if len(*paths) != 1 || (*paths)[0] != "GET /v1/cli/config" {
		t.Fatalf("requests = %v, want one GET /v1/cli/config", *paths)
	}
}

// $PRIZMAL_APP_URL no longer names the approval page. A stale value must not
// send the operator to the wrong place.
func TestApprovalURLIgnoresTheOldEnvironmentVariable(t *testing.T) {
	useTempHome(t)
	t.Setenv("PRIZMAL_APP_URL", "https://stale.example.invalid")
	srv, _ := configStub(t, http.StatusOK, `{"approval_url":"https://app.example.invalid/cli/authorize"}`)

	got, err := approvalURL(device.NewClient(srv.URL))
	if err != nil || got != "https://app.example.invalid/cli/authorize" {
		t.Fatalf("approvalURL = %q, %v; want the Switch's answer, not $PRIZMAL_APP_URL", got, err)
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
	if got, err := approvalURL(device.NewClient(srv.URL)); err != nil || strings.Contains(got, "stale") {
		t.Fatalf("approvalURL = %q, %v; the old app_url must not be used", got, err)
	}
}

// A Switch with no such route, or one that returns nothing, stops the login
// with an error that says what to update. There is no fallback page.
func TestApprovalURLErrorsWhenTheSwitchReportsNone(t *testing.T) {
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
	} {
		t.Run(tc.name, func(t *testing.T) {
			useTempHome(t)
			srv, _ := configStub(t, tc.status, tc.body)
			_, err := approvalURL(device.NewClient(srv.URL))
			if err == nil {
				t.Fatal("approvalURL returned no error")
			}
			errorMentions(t, err, "approval page", srv.URL, "needs updating")
		})
	}
}

// With no approval page, the login stops before it makes or changes a device
// key, so a failed login leaves nothing behind.
func TestLoginWithoutAnApprovalPageLeavesNoKeyBehind(t *testing.T) {
	useTempHome(t)
	srv, _ := configStub(t, http.StatusNotFound, `{}`)
	var out bytes.Buffer

	_, _, _, err := prepareLogin(device.NewClient(srv.URL), &out)
	if err == nil {
		t.Fatal("prepareLogin returned no error")
	}
	if deviceKeyExists() {
		t.Fatal("a failed login left a device key behind")
	}
}

// errorMentions fails the test when err's text lacks any of the given parts.
func errorMentions(t *testing.T, err error, parts ...string) {
	t.Helper()
	for _, want := range parts {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}
