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
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/registry"
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

// resetDeviceModeState clears every envconfig field enterDeviceMode touches,
// so one test's device-mode outcome cannot leak into the next.
func resetDeviceModeState(t *testing.T) {
	t.Helper()
	envconfig.SetAPIKey("")
	envconfig.SetConfigAPIKey("")
	envconfig.SetDeviceMode(false)
	envconfig.SetDeviceToken("")
	t.Cleanup(func() {
		envconfig.SetAPIKey("")
		envconfig.SetConfigAPIKey("")
		envconfig.SetDeviceMode(false)
		envconfig.SetDeviceToken("")
	})
}

// deviceLoginApplies is keyed on the harness the launch names, so its
// decisions are the registry's, not a hand-written list.
// TestDeviceModeSupport pins the same split on the launcher side.
func TestDeviceLoginAppliesByHarness(t *testing.T) {
	resetDeviceModeState(t)
	for _, tc := range []struct {
		name     string
		listFlag bool
		args     []string
		want     bool
	}{
		{"a --list has no harness behind it", true, []string{"pi"}, true},
		{"a bare --pick has no harness behind it", false, nil, true},
		{"claude refreshes its own token", false, []string{"claude"}, true},
		{"pi refreshes through its credential command", false, []string{"pi"}, true},
		{"codex refreshes its own token", false, []string{"codex"}, true},
		{"cline receives the key once", false, []string{"cline"}, false},
		{"opencode receives the key once", false, []string{"opencode"}, false},
		{"an unknown word is not a harness", false, []string{"bogus"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := deviceLoginApplies(tc.listFlag, tc.args); got != tc.want {
				t.Errorf("deviceLoginApplies(%v, %q) = %v, want %v", tc.listFlag, tc.args, got, tc.want)
			}
		})
	}
	for _, spec := range registry.ListAllIntegrationSpecs() {
		want := spec.Name == "claude" || spec.Name == "pi" || spec.Name == "codex"
		if got := deviceLoginApplies(false, []string{spec.Name}); got != want {
			t.Errorf("deviceLoginApplies for %q = %v, want %v", spec.Name, got, want)
		}
	}
}

// captureStderr runs f with os.Stderr redirected to a pipe and returns what it
// wrote, the way captureStdout reads the helper's stdout contract.
func captureStderr(t *testing.T, f func() error) (string, error) {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	runErr := f()
	_ = w.Close()
	os.Stderr = old
	data, _ := io.ReadAll(r)
	return string(data), runErr
}

// A device key that exists but cannot refresh must not abort a launch that
// already has a usable switch key: the config file's api_key, already
// resolved into envconfig before enterDeviceMode runs, is a working fallback.
// An operator hit this running a fleet of agents from a machine whose device
// key was stuck: device mode "outranks" a switch key only when the handshake
// works, not when it fails with a usable credential sitting right there.
func TestEnterDeviceModeFallsBackToConfigKeyWhenRefreshFails(t *testing.T) {
	home := useTempHome(t)
	resetDeviceModeState(t)
	writeDeviceKey(t, home)
	envconfig.SetConfigAPIKey("sk-fallback-from-config")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"UNAUTHORIZED"}}`))
	}))
	defer srv.Close()
	envconfig.SetBaseURL(srv.URL)
	t.Cleanup(func() { envconfig.SetBaseURL("") })

	stderr, err := captureStderr(t, enterDeviceMode)
	if err != nil {
		t.Fatalf("enterDeviceMode: %v, want nil (a usable fallback key is present)", err)
	}
	if envconfig.DeviceMode() {
		t.Error("device mode is on after a failed refresh with a fallback key; the launch should fall back instead")
	}
	if got := envconfig.APIKey(); got != "sk-fallback-from-config" {
		t.Fatalf("APIKey() does not resolve to the config key")
	}
	if !strings.Contains(stderr, "warning:") {
		t.Fatalf("stderr = %q, want a warning that the device key could not refresh", stderr)
	}
}

// The same fallback applies when the switch key comes from $PRIZMAL_SWITCH_KEY
// rather than the config file: enterDeviceMode must not care which non-device
// source resolved the key, only that one did.
func TestEnterDeviceModeFallsBackToEnvKeyWhenRefreshFails(t *testing.T) {
	home := useTempHome(t)
	resetDeviceModeState(t)
	writeDeviceKey(t, home)
	t.Setenv(envconfig.KeyEnvVar, "sk-fallback-from-env")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"NOT_FOUND"}}`))
	}))
	defer srv.Close()
	envconfig.SetBaseURL(srv.URL)
	t.Cleanup(func() { envconfig.SetBaseURL("") })

	if err := enterDeviceMode(); err != nil {
		t.Fatalf("enterDeviceMode: %v, want nil (a usable fallback key is present)", err)
	}
	if got := envconfig.APIKey(); got != "sk-fallback-from-env" {
		t.Fatalf("APIKey() = %q, want the env key to still resolve", got)
	}
}

// With no fallback credential, a device key that cannot refresh still hard
// stops the launch: there is nothing else to run with, so silently
// proceeding would fail at the first turn with a worse error.
func TestEnterDeviceModeHardErrorsWithNoFallback(t *testing.T) {
	home := useTempHome(t)
	resetDeviceModeState(t)
	writeDeviceKey(t, home)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"UNAUTHORIZED"}}`))
	}))
	defer srv.Close()
	envconfig.SetBaseURL(srv.URL)
	t.Cleanup(func() { envconfig.SetBaseURL("") })

	err := enterDeviceMode()
	if err == nil {
		t.Fatal("enterDeviceMode succeeded with no device token and no fallback key")
	}
	if !strings.Contains(err.Error(), "prizmal login") {
		t.Fatalf("error = %q, want it to say run prizmal login", err)
	}
	if envconfig.DeviceMode() {
		t.Error("device mode is on after a failed refresh with no token")
	}
}

// A successful refresh still enters device mode normally: the fallback path
// must not short-circuit the working case.
func TestEnterDeviceModeSucceedsSetsDeviceMode(t *testing.T) {
	home := useTempHome(t)
	resetDeviceModeState(t)
	writeDeviceKey(t, home)
	// A config key is present too, to prove the device token wins when the
	// refresh actually works, matching the documented precedence.
	envconfig.SetConfigAPIKey("sk-should-not-be-used")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"device_token": "pz-d-dt-ok", "expires_in": 600})
	}))
	defer srv.Close()
	envconfig.SetBaseURL(srv.URL)
	t.Cleanup(func() { envconfig.SetBaseURL("") })

	if err := enterDeviceMode(); err != nil {
		t.Fatalf("enterDeviceMode: %v", err)
	}
	if !envconfig.DeviceMode() {
		t.Error("device mode is off after a successful refresh")
	}
	if got := envconfig.APIKey(); got != "pz-d-dt-ok" {
		t.Fatalf("APIKey() = %q, want the fresh device token", got)
	}
}
