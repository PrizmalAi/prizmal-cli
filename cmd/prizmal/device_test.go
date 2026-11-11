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

// resetAppURLState clears the envconfig URL fields appBaseURL reads, so a
// config URL one test loads cannot leak into the next.
func resetAppURLState(t *testing.T) {
	t.Helper()
	envconfig.SetBaseURL("")
	envconfig.SetConfigBaseURL("")
	envconfig.SetConfigAppURL("")
	t.Cleanup(func() {
		envconfig.SetBaseURL("")
		envconfig.SetConfigBaseURL("")
		envconfig.SetConfigAppURL("")
	})
}

// The consent URL comes from $PRIZMAL_APP_URL, then the config file's app_url,
// then the production app. Only the production switch has a default; the app
// origin cannot be derived from any other switch host.
func TestAppBaseURL(t *testing.T) {
	t.Setenv("PRIZMAL_SWITCH_URL", "")
	t.Setenv("PRIZMAL_APP_URL", "")
	resetAppURLState(t)

	envconfig.SetBaseURL("https://api.prizmal.ai")
	got, err := appBaseURL()
	if err != nil || got != "https://app.prizmal.ai" {
		t.Fatalf("appBaseURL(production) = %q, %v; want the production app", got, err)
	}

	envconfig.SetConfigBaseURL("https://api.example.test")
	envconfig.SetConfigAppURL("https://app.example.test/")
	envconfig.SetBaseURL("https://api.example.test")
	got, err = appBaseURL()
	if err != nil || got != "https://app.example.test" {
		t.Fatalf("appBaseURL(config app_url) = %q, %v; want the config app", got, err)
	}

	// A capitalised production host still reaches the production app.
	envconfig.SetBaseURL("https://API.prizmal.ai")
	got, err = appBaseURL()
	if err != nil || got != "https://app.prizmal.ai" {
		t.Fatalf("appBaseURL(capitalised production) = %q, %v; want the production app", got, err)
	}

	// Relay mode changes the URL a launch uses, not the switch the config names,
	// so the paired app still answers.
	envconfig.SetRelay("http://127.0.0.1:54321", "placeholder")
	t.Cleanup(envconfig.ClearRelay)
	envconfig.SetConfigBaseURL("https://api.example.test")
	envconfig.SetConfigAppURL("https://app.example.test")
	envconfig.SetBaseURL("https://api.example.test")
	got, err = appBaseURL()
	if err != nil || got != "https://app.example.test" {
		t.Fatalf("appBaseURL(relay mode) = %q, %v; want the paired config app", got, err)
	}

	t.Setenv("PRIZMAL_APP_URL", "https://app.example.invalid/")
	got, err = appBaseURL()
	if err != nil || got != "https://app.example.invalid" {
		t.Fatalf("appBaseURL($PRIZMAL_APP_URL) = %q, %v; want the environment app", got, err)
	}
}

// A non-production switch with no app URL must stop the login and name the
// setting, not approve the device on the production page.
func TestAppBaseURLRequiresAppURLForNonProductionSwitch(t *testing.T) {
	t.Setenv("PRIZMAL_SWITCH_URL", "")
	t.Setenv("PRIZMAL_APP_URL", "")
	resetAppURLState(t)
	envconfig.SetBaseURL("https://api.example.test")

	got, err := appBaseURL()
	if err == nil {
		t.Fatalf("appBaseURL = %q, want an error for a non-production switch with no app URL", got)
	}
	for _, want := range []string{"app_url", "$PRIZMAL_APP_URL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to name %s", err, want)
		}
	}
}

// A --url or $PRIZMAL_SWITCH_URL override replaces the configured switch, so
// the configured consent app no longer belongs to it. Login must stop instead of
// pointing the override's operator at the configured app.
func TestAppBaseURLIgnoresConfigAppForAnOverriddenSwitch(t *testing.T) {
	t.Setenv(envconfig.EnvVar, "")
	t.Setenv("PRIZMAL_APP_URL", "")
	resetAppURLState(t)
	envconfig.SetConfigBaseURL("https://api.example.test")
	envconfig.SetConfigAppURL("https://app.example.test")
	envconfig.SetBaseURL("https://api.other.example")

	if _, err := appBaseURL(); err == nil {
		t.Fatal("appBaseURL used the configured app for a switch the override replaced")
	}

	// The pairing needs a switch in the config file. An app_url on its own is
	// still not a pairing for a switch that another source resolved.
	resetAppURLState(t)
	envconfig.SetConfigAppURL("https://app.example.test")
	envconfig.SetBaseURL("https://api.example.test")
	if _, err := appBaseURL(); err == nil {
		t.Fatal("appBaseURL used an app_url with no config base_url for an overridden switch")
	}

	// The same holds when $PRIZMAL_SWITCH_URL is the source that chose the
	// switch.
	resetAppURLState(t)
	envconfig.SetConfigAppURL("https://app.example.test")
	t.Setenv(envconfig.EnvVar, "https://api.example.test")
	if _, err := appBaseURL(); err == nil {
		t.Fatal("appBaseURL used the configured app for a switch the environment replaced")
	}
}

// The config file's app_url drives the consent page, so a non-production machine
// configured only through ~/.prizmal/config.json signs in on its app.
func TestLoginConsentAppFollowsConfigAppURL(t *testing.T) {
	home := useTempHome(t)
	t.Setenv(envconfig.EnvVar, "")
	t.Setenv("PRIZMAL_APP_URL", "")
	resetAppURLState(t)

	dir := filepath.Join(home, ".prizmal")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeJSONFile(t, filepath.Join(dir, "config.json"), map[string]any{
		"version":  1,
		"base_url": "https://api.example.test",
		"app_url":  "https://app.example.test",
	})

	if err := applyURLPrecedence("", ""); err != nil {
		t.Fatalf("applyURLPrecedence: %v", err)
	}
	if got := envconfig.BaseURL(); got != "https://api.example.test" {
		t.Fatalf("BaseURL() = %q, want the config file's switch URL", got)
	}
	got, err := appBaseURL()
	if err != nil || got != "https://app.example.test" {
		t.Fatalf("appBaseURL() = %q, %v; want the config file's app_url", got, err)
	}
}

// A config file naming a non-production switch and no app URL reaches the same
// error, so the login names what the file is missing instead of guessing.
func TestLoginRequiresAppURLForConfiguredNonProductionSwitch(t *testing.T) {
	home := useTempHome(t)
	t.Setenv(envconfig.EnvVar, "")
	t.Setenv("PRIZMAL_APP_URL", "")
	resetAppURLState(t)

	dir := filepath.Join(home, ".prizmal")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeJSONFile(t, filepath.Join(dir, "config.json"), map[string]any{
		"version": 1, "base_url": "https://api.example.test",
	})

	if err := applyURLPrecedence("", ""); err != nil {
		t.Fatalf("applyURLPrecedence: %v", err)
	}
	if _, err := appBaseURL(); err == nil {
		t.Fatal("appBaseURL found an app URL for a non-production switch with none configured")
	}
}

// A config file that exists but cannot be parsed must stop the login rather
// than let it fall back to the production default and approve a
// non-production device there.
func TestLoginRefusesUnreadableConfig(t *testing.T) {
	home := useTempHome(t)
	t.Setenv(envconfig.EnvVar, "")
	t.Setenv("PRIZMAL_APP_URL", "")
	resetAppURLState(t)

	dir := filepath.Join(home, ".prizmal")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := applyURLPrecedence("", ""); err == nil {
		t.Fatal("applyURLPrecedence accepted a malformed config file")
	}
}

// The apiKeyHelper runs with no --url, so it must take the switch endpoint from
// the config file.
func TestAuthTokenUsesConfigBaseURL(t *testing.T) {
	home := useTempHome(t)
	writeDeviceKey(t, home)
	t.Setenv(envconfig.EnvVar, "")
	resetAppURLState(t)

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"device_token": "pz-d-dt-from-config", "expires_in": 600})
	}))
	defer srv.Close()

	writeJSONFile(t, filepath.Join(home, ".prizmal", "config.json"), map[string]any{
		"version": 1, "base_url": srv.URL,
	})

	cmds := deviceCommands()
	auth := cmds[1]
	auth.SetArgs([]string{"token"})
	out, err := captureStdout(t, func() error { return auth.Execute() })
	if err != nil {
		t.Fatalf("auth token: %v", err)
	}
	if out != "pz-d-dt-from-config\n" {
		t.Fatalf("stdout = %q, want the token from the config's switch", out)
	}
	if gotPath != "/v1/cli/token" {
		t.Fatalf("request path = %q, want the switch refresh path", gotPath)
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
		{"cline refreshes through its relay", false, []string{"cline"}, true},
		{"opencode refreshes through its plugin", false, []string{"opencode"}, true},
		{"kilo refreshes through its relay", false, []string{"kilo"}, true},
		{"an unknown word is not a harness", false, []string{"bogus"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := deviceLoginApplies(tc.listFlag, tc.args); got != tc.want {
				t.Errorf("deviceLoginApplies(%v, %q) = %v, want %v", tc.listFlag, tc.args, got, tc.want)
			}
		})
	}
	for _, spec := range registry.ListAllIntegrationSpecs() {
		want := spec.Name == "claude" || spec.Name == "pi" || spec.Name == "codex" ||
			spec.Name == "opencode" || spec.Name == "cline" || spec.Name == "kilo"
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

// The approval page and the switch must share a parent domain. An approval
// recorded on one deployment's page never reaches a switch checking another, so
// the login would only time out. The parent is the host without its first label.
func TestAppBaseURLRefusesDifferentParentDomains(t *testing.T) {
	t.Setenv("PRIZMAL_SWITCH_URL", "")
	t.Setenv("PRIZMAL_APP_URL", "")
	resetAppURLState(t)

	// A matching pair passes.
	envconfig.SetConfigBaseURL("https://api.one.example.invalid")
	envconfig.SetConfigAppURL("https://app.one.example.invalid")
	envconfig.SetBaseURL("https://api.one.example.invalid")
	if got, err := appBaseURL(); err != nil || got != "https://app.one.example.invalid" {
		t.Fatalf("appBaseURL(matching pair) = %q, %v; want the app", got, err)
	}

	// A stale $PRIZMAL_APP_URL fails and names both hosts and the variable.
	envconfig.SetConfigBaseURL("")
	envconfig.SetConfigAppURL("")
	envconfig.SetBaseURL("https://api.one.example.invalid")
	t.Setenv("PRIZMAL_APP_URL", "https://app.two.example.invalid")
	_, err := appBaseURL()
	if err == nil {
		t.Fatal("appBaseURL(app on another parent domain) returned no error")
	}
	errorMentions(t, err, "app.two.example.invalid", "api.one.example.invalid", "$PRIZMAL_APP_URL")

	// The same mismatch from the config file names app_url instead.
	t.Setenv("PRIZMAL_APP_URL", "")
	envconfig.SetConfigBaseURL("https://api.one.example.invalid")
	envconfig.SetConfigAppURL("https://app.two.example.invalid")
	envconfig.SetBaseURL("https://api.one.example.invalid")
	_, err = appBaseURL()
	if err == nil {
		t.Fatal("appBaseURL(config app_url on another parent domain) returned no error")
	}
	errorMentions(t, err, "app.two.example.invalid", "api.one.example.invalid", "app_url")
	if strings.Contains(err.Error(), "$PRIZMAL_APP_URL") {
		t.Errorf("error %q blames $PRIZMAL_APP_URL, which is unset", err)
	}

	// Two-label hosts under one top-level domain are different deployments.
	envconfig.SetConfigBaseURL("")
	envconfig.SetConfigAppURL("")
	envconfig.SetBaseURL("https://one.invalid")
	t.Setenv("PRIZMAL_APP_URL", "https://two.invalid")
	if _, err := appBaseURL(); err == nil {
		t.Fatal("appBaseURL(two-label hosts under one top-level domain) returned no error")
	}
	// A two-label switch still pairs with an app one label below it.
	t.Setenv("PRIZMAL_APP_URL", "https://app.one.invalid")
	if _, err := appBaseURL(); err != nil {
		t.Fatalf("appBaseURL(two-label switch, app below it) = %v; want it to pass", err)
	}

	// A host one level deeper has a different parent, so it is refused too.
	envconfig.SetConfigBaseURL("")
	envconfig.SetConfigAppURL("")
	envconfig.SetBaseURL("https://api.one.example.invalid")
	t.Setenv("PRIZMAL_APP_URL", "https://app.sub.one.example.invalid")
	if _, err := appBaseURL(); err == nil {
		t.Fatal("appBaseURL(app one level deeper) returned no error")
	}
}

// A local or bare switch has no parent domain to compare, so it passes with any
// app and local development keeps working.
func TestAppBaseURLAcceptsHostsWithoutAParentDomain(t *testing.T) {
	t.Setenv("PRIZMAL_SWITCH_URL", "")
	resetAppURLState(t)
	for _, sw := range []string{"http://127.0.0.1:8080", "http://localhost:8080", "http://[::1]:8080", "http://switch:8080"} {
		envconfig.SetBaseURL(sw)
		for _, app := range []string{"https://app.one.example.invalid", "http://localhost:3000"} {
			t.Setenv("PRIZMAL_APP_URL", app)
			if got, err := appBaseURL(); err != nil || got != app {
				t.Errorf("appBaseURL(switch %s, app %s) = %q, %v; want it to pass", sw, app, got, err)
			}
		}
	}
	// And a bare or local app passes against a switch with a parent domain.
	envconfig.SetBaseURL("https://api.one.example.invalid")
	t.Setenv("PRIZMAL_APP_URL", "http://localhost:3000")
	if _, err := appBaseURL(); err != nil {
		t.Errorf("appBaseURL(local app) = %v; want it to pass", err)
	}
}

// A host written as a fully qualified name, with a trailing dot, is the same
// host, so it cannot slip past the check.
func TestParentDomainIgnoresTrailingDotAndCase(t *testing.T) {
	cases := map[string]string{
		"https://app.two.example.invalid./": "two.example.invalid",
		"https://API.One.Example.Invalid":   "one.example.invalid",
		"http://localhost:3000":             "",
		"http://127.0.0.1:8080":             "",
		"http://[::1]:8080":                 "",
		"http://switch":                     "",
		// A two-label host is its own parent: its real parent is the bare
		// top-level domain, which every such host shares.
		"https://one.invalid": "one.invalid",
	}
	for url, want := range cases {
		if got := parentDomain(url); got != want {
			t.Errorf("parentDomain(%q) = %q, want %q", url, got, want)
		}
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
