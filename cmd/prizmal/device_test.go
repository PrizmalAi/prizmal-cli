package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PrizmalAi/prizmal-cli/internal/device"
	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
)

// captureStdout runs f with os.Stdout redirected to a pipe and returns what it
// wrote. The helper command's whole contract is what reaches stdout, so the
// test reads exactly that.
func captureStdout(t *testing.T, f func() error) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	runErr := f()
	_ = w.Close()
	os.Stdout = old
	data, _ := io.ReadAll(r)
	return string(data), runErr
}

// writeDeviceKey writes a device key into home's .prizmal directory and
// returns it.
func writeDeviceKey(t *testing.T, home string) *device.Key {
	t.Helper()
	key, err := device.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".prizmal")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := key.WriteKeyFile(dir); err != nil {
		t.Fatal(err)
	}
	return key
}

// runAuthToken runs `auth token` with the given URL flag, the way the
// apiKeyHelper string does.
func runAuthToken(t *testing.T, urlFlag string) (string, error) {
	t.Helper()
	cmds := deviceCommands()
	auth := cmds[1]
	auth.SetArgs([]string{"token", "--url", urlFlag})
	return captureStdout(t, func() error { return auth.Execute() })
}

// TestAuthTokenPrintsOneLineOnStdout is the helper's contract: Claude Code
// reads stdout as the credential, so it must carry exactly the token and
// nothing else.
func TestAuthTokenPrintsOneLineOnStdout(t *testing.T) {
	home := useTempHome(t)
	key := writeDeviceKey(t, home)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"device_token": "pz-d-dt-from-switch", "expires_in": 600})
	}))
	defer srv.Close()

	out, err := runAuthToken(t, srv.URL)
	if err != nil {
		t.Fatalf("auth token: %v", err)
	}
	if out != "pz-d-dt-from-switch\n" {
		t.Fatalf("stdout = %q, want the token and one newline", out)
	}
	if got, _ := device.LoadCachedToken(); got == nil || got.Token != "pz-d-dt-from-switch" {
		t.Fatalf("the token was not cached: %+v", got)
	}
	_ = key
}

// A cached token with enough life left is printed without a refresh.
func TestAuthTokenUsesCache(t *testing.T) {
	home := useTempHome(t)
	writeDeviceKey(t, home)
	// Prime the cache directly, far from expiry.
	if err := (&device.CachedToken{Token: "pz-d-dt-cached", Expiry: time.Now().Add(10 * time.Minute)}).Save(); err != nil {
		t.Fatal(err)
	}

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer srv.Close()

	out, err := runAuthToken(t, srv.URL)
	if err != nil {
		t.Fatalf("auth token: %v", err)
	}
	if out != "pz-d-dt-cached\n" {
		t.Fatalf("stdout = %q, want the cached token", out)
	}
	if hits != 0 {
		t.Fatalf("refreshed %d times despite a valid cache", hits)
	}
}

// With no device key the helper tells the operator exactly what to do.
func TestAuthTokenWithoutDeviceKeySaysRunLogin(t *testing.T) {
	useTempHome(t)

	_, err := runAuthToken(t, "http://127.0.0.1:0")
	if err == nil {
		t.Fatal("auth token succeeded with no device key")
	}
	if !strings.Contains(err.Error(), "prizmal login") {
		t.Fatalf("error = %q, want it to say run prizmal login", err)
	}
}

// REAUTH_REQUIRED exits with the same instruction as a missing key, because its
// fix is the same: sign in again.
func TestAuthTokenReauthRequiredSaysRunLogin(t *testing.T) {
	home := useTempHome(t)
	writeDeviceKey(t, home)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"REAUTH_REQUIRED"}}`))
	}))
	defer srv.Close()

	_, err := runAuthToken(t, srv.URL)
	if err == nil || !strings.Contains(err.Error(), "prizmal login") {
		t.Fatalf("error = %v, want it to say run prizmal login", err)
	}
}

// The consent URL for a staging switch stays on the staging app, so a login
// does not send a staging operator to the production page.
func TestAppBaseURLFollowsSwitchHost(t *testing.T) {
	t.Setenv("PRIZMAL_SWITCH_URL", "")
	t.Setenv("PRIZMAL_APP_URL", "")
	envconfig.SetBaseURL("https://api.staging.prizmal.ai")
	defer envconfig.SetBaseURL("")
	if got := appBaseURL(); got != "https://app.staging.prizmal.ai" {
		t.Fatalf("appBaseURL(staging) = %q", got)
	}
	envconfig.SetBaseURL("https://api.prizmal.ai")
	if got := appBaseURL(); got != "https://app.prizmal.ai" {
		t.Fatalf("appBaseURL(prod) = %q", got)
	}
	// An explicit override wins.
	t.Setenv("PRIZMAL_APP_URL", "https://app.example.test/")
	if got := appBaseURL(); got != "https://app.example.test" {
		t.Fatalf("appBaseURL(override) = %q", got)
	}
}
