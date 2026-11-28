package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PrizmalAi/prizmal-cli/internal/config"
	"github.com/PrizmalAi/prizmal-cli/internal/device"
	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	launcher "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/registry"
	"github.com/PrizmalAi/prizmal-cli/internal/stubserver"
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

// A running session cannot start over when the 7-day sign-in deadline passes,
// so the helper does what the launcher does at launch: with a switch key in
// hand, it prints that key and Claude Code keeps working. The warning goes to
// stderr, where the helper's diagnostics belong.
func TestAuthTokenFallsBackToSwitchKeyWhenReauthRequired(t *testing.T) {
	home := useTempHome(t)
	writeDeviceKey(t, home)
	t.Setenv(envconfig.KeyEnvVar, "sk-fallback-from-env")
	envconfig.SetAPIKey("")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"REAUTH_REQUIRED"}}`))
	}))
	defer srv.Close()

	out, err := runAuthToken(t, srv.URL)
	if err != nil {
		t.Fatalf("auth token: %v, want the switch key as the fallback", err)
	}
	if strings.TrimSpace(out) != "sk-fallback-from-env" {
		t.Fatalf("stdout = %q, want the switch key and nothing else", out)
	}
}

// The fallback never invents a credential: with no switch key anywhere, the
// helper still fails and says to sign in.
func TestAuthTokenFallbackNeedsASwitchKey(t *testing.T) {
	home := useTempHome(t)
	writeDeviceKey(t, home)
	t.Setenv(envconfig.KeyEnvVar, "")
	envconfig.SetAPIKey("")
	envconfig.SetConfigAPIKey("")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"REAUTH_REQUIRED"}}`))
	}))
	defer srv.Close()

	out, err := runAuthToken(t, srv.URL)
	if err == nil || !strings.Contains(err.Error(), "prizmal login") || strings.TrimSpace(out) != "" {
		t.Fatalf("stdout = %q, err = %v; want no output and a run-prizmal-login error", out, err)
	}
}

// A revoked device's helper run ends with the same next step as every other
// refusal the CLI can fix: run prizmal login, which makes a new key.
func TestAuthTokenRevokedDeviceSaysRunLogin(t *testing.T) {
	home := useTempHome(t)
	writeDeviceKey(t, home)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"DEVICE_REVOKED"}}`))
	}))
	defer srv.Close()

	_, err := runAuthToken(t, srv.URL)
	if err == nil || !strings.Contains(err.Error(), "revoked") || !strings.Contains(err.Error(), "prizmal login") {
		t.Fatalf("error = %v, want it to say the device was revoked and to run prizmal login", err)
	}
}

// resetAppURLState clears the envconfig URL fields a login reads, so a config
// URL one test loads cannot leak into the next.
func resetAppURLState(t *testing.T) {
	t.Helper()
	envconfig.SetBaseURL("")
	envconfig.SetConfigBaseURL("")
	t.Cleanup(func() {
		envconfig.SetBaseURL("")
		envconfig.SetConfigBaseURL("")
	})
}

// A config file that exists but cannot be parsed must stop the login rather
// than let it fall back to the production default and approve a
// non-production device there.
func TestLoginRefusesUnreadableConfig(t *testing.T) {
	home := useTempHome(t)
	t.Setenv(envconfig.EnvVar, "")
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

// The deadline warning prints once device mode is on and the deadline is under
// a day away, and stays silent for a deadline further out or for a member with
// none.
func TestWarnReauthDeadline(t *testing.T) {
	useTempHome(t)
	resetDeviceModeState(t)
	now := time.Now()

	save := func(by *time.Time) {
		t.Helper()
		if err := (&device.CachedToken{Token: "pz-d-x", Expiry: now.Add(10 * time.Minute), ReauthBy: by}).Save(); err != nil {
			t.Fatal(err)
		}
	}
	warn := func() string {
		var b strings.Builder
		warnReauthDeadline(&b, now)
		return b.String()
	}

	soon := now.Add(5 * time.Hour)
	save(&soon)
	if got := warn(); got != "" {
		t.Fatalf("warning printed with device mode off: %q", got)
	}

	envconfig.SetDeviceMode(true)
	if got := warn(); !strings.Contains(got, "prizmal login") || !strings.Contains(got, soon.UTC().Format("2006-01-02 15:04 UTC")) {
		t.Fatalf("warning = %q, want the deadline and the login command", got)
	}
	far := now.Add(72 * time.Hour)
	save(&far)
	if got := warn(); got != "" {
		t.Fatalf("warning printed 3 days out: %q", got)
	}
	past := now.Add(-time.Hour)
	save(&past)
	if got := warn(); !strings.Contains(got, "past its deadline") {
		t.Fatalf("warning = %q, want the past-deadline sentence", got)
	}
	save(nil)
	if got := warn(); got != "" {
		t.Fatalf("warning printed for a member with no deadline: %q", got)
	}
}

// Only the sign-in deadline falls back. A device the admin revoked answers
// 401 NOT_AUTHORIZED, and the helper must fail rather than put the static key
// in its place.
func TestAuthTokenDoesNotFallBackForARevokedDevice(t *testing.T) {
	home := useTempHome(t)
	writeDeviceKey(t, home)
	t.Setenv(envconfig.KeyEnvVar, "sk-fallback-from-env")
	envconfig.SetAPIKey("")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"UNAUTHORIZED"}}`))
	}))
	defer srv.Close()

	out, err := runAuthToken(t, srv.URL)
	if err == nil || strings.Contains(out, "sk-fallback") {
		t.Fatalf("stdout = %q, err = %v; want a failure and no switch key on stdout", out, err)
	}
}

// A switch key that is not one printable ASCII line cannot go to Claude Code,
// so the helper fails with the login instruction instead of printing it.
func TestAuthTokenFallbackRejectsAnUnprintableKey(t *testing.T) {
	home := useTempHome(t)
	writeDeviceKey(t, home)
	t.Setenv(envconfig.KeyEnvVar, "sk-bad\nkey")
	envconfig.SetAPIKey("")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"REAUTH_REQUIRED"}}`))
	}))
	defer srv.Close()

	out, err := runAuthToken(t, srv.URL)
	if err == nil || strings.TrimSpace(out) != "" {
		t.Fatalf("stdout = %q, err = %v; want a login error and no output", out, err)
	}
}

// useExpiredSwitch points the launch at a stub switch that refuses the first
// refresh as an expired sign-in, as the Switch does once the deadline passed,
// and approves every refresh after it, as it does once the browser approval
// lands.
func useExpiredSwitch(t *testing.T) string {
	t.Helper()
	srv := stubserver.NewServer(stubserver.WithDeviceReauthFirst(), stubserver.WithDeviceApproval())
	t.Cleanup(srv.Close)
	envconfig.SetBaseURL(srv.URL)
	t.Cleanup(func() { envconfig.SetBaseURL("") })
	return srv.URL
}

// noPicker fails the test when the launch shows a picker it must not show.
func noPicker(t *testing.T) menuPicker {
	return func(heading string, _ []launcher.Option) (string, error) {
		t.Errorf("the %q picker was shown", heading)
		return "", launcher.ErrCancelled
	}
}

// noBrowser fails the test when the launch opens a browser sign-in it must not.
func noBrowser(t *testing.T) func() error {
	return func() error {
		t.Error("the browser sign-in ran")
		return nil
	}
}

// On a terminal, an expired sign-in shows the first-run Sign in picker instead
// of stopping the launch. The browser row re-approves the same device, and the
// launch goes on in device mode with the token the retried refresh returns. A
// switch key that is already set does not skip the picker: it adds a third row,
// and the browser row still wins device mode back.
func TestEnterDeviceModeExpiredSignInShowsSignInPicker(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configKey string
	}{
		{"no switch key set", ""},
		{"a switch key in the config file", "sk-fallback-from-config"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := useTempHome(t)
			resetDeviceModeState(t)
			writeDeviceKey(t, home)
			envconfig.SetConfigAPIKey(tc.configKey)
			useExpiredSwitch(t)
			pick, seen := pickerReturns("browser")

			var signedIn bool
			stderr, err := captureStderr(t, func() error {
				return enterDeviceModeWith(true, pick, scriptedReader(""), func() error {
					signedIn = true
					return nil
				})
			})
			if err != nil {
				t.Fatalf("enterDeviceMode: %v", err)
			}
			if !strings.Contains(stderr, "sign-in expired") {
				t.Errorf("stderr = %q, want it to say the sign-in expired", stderr)
			}
			if seen.heading != firstRunSignInHeading {
				t.Errorf("picker heading = %q, want %q", seen.heading, firstRunSignInHeading)
			}
			first := firstRunSignInOptions()
			wantRows := len(first)
			if tc.configKey != "" {
				wantRows++
			}
			if len(seen.options) != wantRows || !reflect.DeepEqual(seen.options[:len(first)], first) {
				t.Fatalf("picker rows = %+v, want the first-run rows plus one per switch key set", seen.options)
			}
			if tc.configKey != "" {
				extra := seen.options[len(first)]
				if extra.Value != currentKeyOption || !strings.Contains(extra.Description, string(envconfig.KeySourceConfig)) {
					t.Errorf("third row = %+v, want the key already set, naming %s", extra, envconfig.KeySourceConfig)
				}
			}
			if !signedIn {
				t.Fatal("choosing the browser row did not run the browser sign-in")
			}
			if !envconfig.DeviceMode() {
				t.Error("device mode is off after the re-approval")
			}
			if got := envconfig.APIKey(); got != stubserver.DeviceToken {
				t.Fatalf("APIKey() = %q, want the token the retried refresh returned", got)
			}
		})
	}
}

// A switch key already set carries the launch in two cases: on a terminal when
// the operator picks its row, and with no terminal after one warning, as
// before. Neither opens a browser. Each path prints its own line and never the
// other's: a terminal says the sign-in expired and prints no fallback warning,
// and no terminal prints the warning and never says it showed a picker.
func TestEnterDeviceModeExpiredSignInRunsOnTheSwitchKeyAlreadySet(t *testing.T) {
	for _, tc := range []struct {
		name        string
		interactive bool
		pick        func(t *testing.T) menuPicker
		wantStderr  string
		notStderr   string
	}{
		{"picked on a terminal", true, func(*testing.T) menuPicker { p, _ := pickerReturns(currentKeyOption); return p },
			"This device's sign-in expired.", "warning:"},
		{"no terminal", false, noPicker,
			"warning: device key could not refresh", "This device's sign-in expired."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := useTempHome(t)
			resetDeviceModeState(t)
			writeDeviceKey(t, home)
			t.Setenv(envconfig.KeyEnvVar, "sk-fallback-from-env")
			useExpiredSwitch(t)

			stderr, err := captureStderr(t, func() error {
				return enterDeviceModeWith(tc.interactive, tc.pick(t), scriptedReader(""), noBrowser(t))
			})
			if err != nil {
				t.Fatalf("enterDeviceMode: %v", err)
			}
			if !strings.Contains(stderr, tc.wantStderr) {
				t.Errorf("stderr = %q, want %q", stderr, tc.wantStderr)
			}
			if strings.Contains(stderr, tc.notStderr) {
				t.Errorf("stderr = %q, must not contain %q", stderr, tc.notStderr)
			}
			if envconfig.DeviceMode() {
				t.Error("device mode is on, want the launch on the switch key")
			}
			if got := envconfig.APIKey(); got != "sk-fallback-from-env" {
				t.Fatalf("APIKey() = %q, want the env key", got)
			}
		})
	}
}

// The Paste a key row saves the key the way the first run does and launches on
// it. A config file that already exists keeps its other fields.
func TestEnterDeviceModeExpiredSignInPastedKeyCarriesTheLaunch(t *testing.T) {
	home := useTempHome(t)
	resetDeviceModeState(t)
	writeDeviceKey(t, home)
	url := useExpiredSwitch(t)
	if err := (&config.Config{BaseURL: url, DefaultModel: "smart"}).Save(); err != nil {
		t.Fatal(err)
	}
	pick, _ := pickerReturns("key")

	if err := enterDeviceModeWith(true, pick, scriptedReader("sk-pasted\n"), noBrowser(t)); err != nil {
		t.Fatalf("enterDeviceMode: %v", err)
	}
	if envconfig.DeviceMode() {
		t.Error("device mode is on, want the launch on the pasted key")
	}
	if got := envconfig.APIKey(); got != "sk-pasted" {
		t.Fatalf("APIKey() = %q, want the pasted key", got)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load the config file: %v", err)
	}
	if got, _ := cfg.APIKey.Resolve(); got != "sk-pasted" {
		t.Errorf("config api_key = %q, want the pasted key", got)
	}
	if cfg.BaseURL != url || cfg.DefaultModel != "smart" {
		t.Errorf("config = %+v, want base_url and default_model kept", cfg)
	}
}

// The launch stops with the expired sign-in message when nobody chooses a
// credential: an empty answer at the key prompt, a backed-out picker even with
// a switch key at hand, and no terminal with no switch key. None of them
// writes a config file.
func TestEnterDeviceModeExpiredSignInStops(t *testing.T) {
	for _, tc := range []struct {
		name        string
		interactive bool
		configKey   string
		pick        func(t *testing.T) menuPicker
		answer      string
	}{
		{"empty paste", true, "", func(*testing.T) menuPicker { p, _ := pickerReturns("key"); return p }, "\n"},
		{"picker backed out", true, "sk-fallback-from-config", func(*testing.T) menuPicker { return menuCancelled }, ""},
		{"no terminal and no switch key", false, "", noPicker, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := useTempHome(t)
			resetDeviceModeState(t)
			writeDeviceKey(t, home)
			envconfig.SetConfigAPIKey(tc.configKey)
			useExpiredSwitch(t)

			err := enterDeviceModeWith(tc.interactive, tc.pick(t), scriptedReader(tc.answer), noBrowser(t))
			if err == nil || err.Error() != expiredSignInMessage {
				t.Fatalf("enterDeviceMode: %v, want %q", err, expiredSignInMessage)
			}
			if envconfig.DeviceMode() {
				t.Error("device mode is on after the launch stopped")
			}
			if configFileExists(t, home) {
				t.Error("the stopped launch wrote a config file")
			}
		})
	}
}

// Only an expired sign-in opens the picker. Another refusal keeps today's
// error even on a terminal, because signing in again would not fix it.
func TestEnterDeviceModeOtherRefusalsNeverPrompt(t *testing.T) {
	home := useTempHome(t)
	resetDeviceModeState(t)
	writeDeviceKey(t, home)
	srv := stubserver.NewServer(stubserver.WithDeviceRevokedFirst())
	t.Cleanup(srv.Close)
	envconfig.SetBaseURL(srv.URL)
	t.Cleanup(func() { envconfig.SetBaseURL("") })

	err := enterDeviceModeWith(true, noPicker(t), scriptedReader(""), noBrowser(t))
	if err == nil || !strings.Contains(err.Error(), "revoked") {
		t.Fatalf("enterDeviceMode: %v, want the revoked-device error", err)
	}
}
