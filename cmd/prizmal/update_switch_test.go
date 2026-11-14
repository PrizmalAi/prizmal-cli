package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/device"
	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/stubserver"
)

// resetUpdateSwitchState clears the process-wide values the update check reads,
// so one test's flag or config cannot leak into the next.
func resetUpdateSwitchState(t *testing.T) {
	t.Helper()
	reset := func() {
		url, apiKey = "", ""
		envconfig.SetBaseURL("")
		envconfig.SetAPIKey("")
		envconfig.SetConfigBaseURL("")
		envconfig.SetConfigAPIKey("")
		envconfig.SetDeviceMode(false)
		envconfig.SetDeviceToken("")
		envconfig.ClearRelay()
	}
	reset()
	t.Cleanup(reset)
}

func writeUpdateConfig(t *testing.T, home string, cfg map[string]any) {
	t.Helper()
	dir := filepath.Join(home, ".prizmal")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// The check asks the Switch the CLI already uses, resolved the way a launch
// resolves it: the flag, then the environment, then the config file, then the
// default.
func TestUpdateSwitchURLFollowsTheLaunchPrecedence(t *testing.T) {
	home := useTempHome(t)
	resetUpdateSwitchState(t)
	t.Setenv(envconfig.EnvVar, "")

	if got := updateSwitch().BaseURL; got != envconfig.DefaultURL {
		t.Errorf("no setting: BaseURL = %q, want the default", got)
	}
	writeUpdateConfig(t, home, map[string]any{"version": 1, "base_url": "http://127.0.0.1:7001"})
	if got := updateSwitch().BaseURL; got != "http://127.0.0.1:7001" {
		t.Errorf("config only: BaseURL = %q", got)
	}
	t.Setenv(envconfig.EnvVar, "http://127.0.0.1:7002")
	if got := updateSwitch().BaseURL; got != "http://127.0.0.1:7002" {
		t.Errorf("environment over config: BaseURL = %q", got)
	}
	url = "http://127.0.0.1:7003"
	if got := updateSwitch().BaseURL; got != "http://127.0.0.1:7003" {
		t.Errorf("flag over environment: BaseURL = %q", got)
	}
}

// The credential is a switch key from the flag, the environment or the config
// file, in that order, and none when the CLI holds neither a key nor a device.
func TestUpdateSwitchCredentialIsTheSwitchKeyTheCLIHolds(t *testing.T) {
	home := useTempHome(t)
	resetUpdateSwitchState(t)
	t.Setenv(envconfig.EnvVar, "")

	if got := updateSwitch().Credential(); got != "" {
		t.Errorf("nothing held: credential = %q, want none", got)
	}
	writeUpdateConfig(t, home, map[string]any{"version": 1, "api_key": "pz-test-config-key"})
	if got := updateSwitch().Credential(); got != "pz-test-config-key" {
		t.Errorf("config key: credential = %q", got)
	}
	t.Setenv(envconfig.KeyEnvVar, "pz-test-env-key")
	if got := updateSwitch().Credential(); got != "pz-test-env-key" {
		t.Errorf("environment key over config: credential = %q", got)
	}
	apiKey = "pz-test-flag-key"
	if got := updateSwitch().Credential(); got != "pz-test-flag-key" {
		t.Errorf("flag key over environment: credential = %q", got)
	}
}

// With no switch key the credential is the device token the CLI mints from its
// device key, and the token is cached so the launch that follows reuses it.
func TestUpdateSwitchCredentialFallsBackToTheDeviceToken(t *testing.T) {
	useTempHome(t)
	resetUpdateSwitchState(t)
	sw := stubserver.NewServer(stubserver.WithDeviceApproval())
	t.Cleanup(sw.Close)
	t.Setenv(envconfig.EnvVar, sw.URL)
	if _, _, err := device.LoadOrCreateKey(); err != nil {
		t.Fatal(err)
	}

	got := updateSwitch().Credential()
	if got != stubserver.DeviceToken {
		t.Fatalf("credential = %q, want the device token", got)
	}
	if cached := loadCachedToken(); cached == nil || cached.Token != stubserver.DeviceToken {
		t.Errorf("the minted token was not cached: %+v", cached)
	}
}

// A device that cannot mint a token leaves the check anonymous. It never
// fails the launch or prints a warning of its own.
func TestUpdateSwitchCredentialIsNoneWhenTheDeviceCannotRefresh(t *testing.T) {
	useTempHome(t)
	resetUpdateSwitchState(t)
	sw := stubserver.NewServer() // an unapproved device: the refresh answers 404
	t.Cleanup(sw.Close)
	t.Setenv(envconfig.EnvVar, sw.URL)
	if _, _, err := device.LoadOrCreateKey(); err != nil {
		t.Fatal(err)
	}
	if got := updateSwitch().Credential(); got != "" {
		t.Errorf("credential = %q, want none", got)
	}
}

// A device-mode placeholder or a loopback-relay placeholder is not a credential
// the Switch accepts. The check sends the switch key the CLI holds, or mints a
// device token, and never the placeholder envconfig resolves in those modes.
func TestUpdateSwitchCredentialIgnoresAPlaceholder(t *testing.T) {
	useTempHome(t)
	resetUpdateSwitchState(t)
	t.Setenv(envconfig.EnvVar, "")
	envconfig.SetDeviceMode(true)
	envconfig.SetDeviceToken("pz-test-placeholder")
	t.Setenv(envconfig.KeyEnvVar, "pz-test-env-key")
	if got := updateSwitch().Credential(); got != "pz-test-env-key" {
		t.Errorf("device mode with a key held: credential = %q, want the switch key", got)
	}

	t.Setenv(envconfig.KeyEnvVar, "")
	envconfig.SetDeviceMode(false)
	envconfig.SetDeviceToken("")
	envconfig.SetRelay("http://127.0.0.1:1", "pz-test-relay-placeholder")
	if got := updateSwitch().Credential(); got != "" {
		t.Errorf("relay mode with nothing held: credential = %q, want none", got)
	}
	if got := updateSwitch().BaseURL; got == "http://127.0.0.1:1" {
		t.Errorf("BaseURL = the relay's address; the check asks the Switch, not the relay")
	}
}

func TestUpdateSwitchCredentialMintsTheDeviceTokenInDeviceMode(t *testing.T) {
	useTempHome(t)
	resetUpdateSwitchState(t)
	sw := stubserver.NewServer(stubserver.WithDeviceApproval())
	t.Cleanup(sw.Close)
	t.Setenv(envconfig.EnvVar, sw.URL)
	if _, _, err := device.LoadOrCreateKey(); err != nil {
		t.Fatal(err)
	}
	envconfig.SetDeviceMode(true)
	envconfig.SetDeviceToken("pz-test-placeholder")
	if got := updateSwitch().Credential(); got != stubserver.DeviceToken {
		t.Errorf("credential = %q, want the minted device token and not the placeholder", got)
	}
}

// A machine with a device key signs in as that device, and the device token
// outranks every switch key, as it does for a launch: the switch key never
// leaves the machine while the device can speak for it.
func TestUpdateSwitchCredentialPrefersTheDeviceTokenOverASwitchKey(t *testing.T) {
	home := useTempHome(t)
	resetUpdateSwitchState(t)
	sw := stubserver.NewServer(stubserver.WithDeviceApproval())
	t.Cleanup(sw.Close)
	t.Setenv(envconfig.EnvVar, sw.URL)
	writeUpdateConfig(t, home, map[string]any{"version": 1, "api_key": "pz-test-config-key"})
	t.Setenv(envconfig.KeyEnvVar, "pz-test-env-key")
	apiKey = "pz-test-flag-key"
	if _, _, err := device.LoadOrCreateKey(); err != nil {
		t.Fatal(err)
	}
	if got := updateSwitch().Credential(); got != stubserver.DeviceToken {
		t.Errorf("credential = %q, want the device token ahead of every switch key", got)
	}
}

// A device that cannot refresh leaves the switch key in place, as a launch
// falls back to it.
func TestUpdateSwitchCredentialFallsBackToTheSwitchKeyWhenTheDeviceCannotRefresh(t *testing.T) {
	useTempHome(t)
	resetUpdateSwitchState(t)
	sw := stubserver.NewServer() // an unapproved device: the refresh answers 404
	t.Cleanup(sw.Close)
	t.Setenv(envconfig.EnvVar, sw.URL)
	t.Setenv(envconfig.KeyEnvVar, "pz-test-env-key")
	if _, _, err := device.LoadOrCreateKey(); err != nil {
		t.Fatal(err)
	}
	if got := updateSwitch().Credential(); got != "pz-test-env-key" {
		t.Errorf("credential = %q, want the switch key", got)
	}
}
