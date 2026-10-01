package envconfig

import "testing"

// resetKeys clears every key source for one test.
func resetKeys(t *testing.T) {
	t.Helper()
	t.Setenv(KeyEnvVar, "")
	SetAPIKey("")
	SetConfigAPIKey("")
	t.Cleanup(func() {
		SetAPIKey("")
		SetConfigAPIKey("")
	})
}

// The key resolves in the order the URL does: --api-key, then
// $PRIZMAL_SWITCH_KEY, then the config file, and APIKeySource names the
// source that won.
func TestAPIKeyPrecedence(t *testing.T) {
	cases := []struct {
		name              string
		flag, env, config string
		wantKey           string
		wantSource        KeySource
	}{
		{name: "nothing set", wantSource: KeySourceNone},
		{name: "config only", config: "sk-config", wantKey: "sk-config", wantSource: KeySourceConfig},
		{name: "env only", env: "sk-env", wantKey: "sk-env", wantSource: KeySourceEnv},
		{name: "env outranks config", env: "sk-env", config: "sk-config", wantKey: "sk-env", wantSource: KeySourceEnv},
		{name: "flag outranks env and config", flag: "sk-flag", env: "sk-env", config: "sk-config", wantKey: "sk-flag", wantSource: KeySourceFlag},
		{name: "env equal to config still names env", env: "sk-same", config: "sk-same", wantKey: "sk-same", wantSource: KeySourceEnv},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetKeys(t)
			t.Setenv(KeyEnvVar, tc.env)
			SetAPIKey(tc.flag)
			SetConfigAPIKey(tc.config)
			if got := APIKey(); got != tc.wantKey {
				t.Errorf("APIKey() returned the wrong key for %s", tc.name)
			}
			if got := APIKeySource(); got != tc.wantSource {
				t.Errorf("APIKeySource() = %q, want %q", got, tc.wantSource)
			}
		})
	}
}

// TestDeviceModeOutranksSwitchKeySources pins the device-mode precedence: the
// enrolled device's token is the credential, and a switch key exported in the
// shell (or the config file) does not take its place. That is what makes the
// launcher's device-mode env filtering coherent: the child gets the device
// token, not a key the operator forgot was exported.
func TestDeviceModeOutranksSwitchKeySources(t *testing.T) {
	t.Setenv(KeyEnvVar, "sk-from-env")
	SetAPIKey("sk-from-flag")
	SetConfigAPIKey("sk-from-config")
	SetDeviceMode(true)
	SetDeviceToken("pz-s-dt-a-token")
	t.Cleanup(func() {
		SetAPIKey("")
		SetConfigAPIKey("")
		SetDeviceMode(false)
		SetDeviceToken("")
	})

	if got, src := APIKey(), APIKeySource(); got != "pz-s-dt-a-token" || src != KeySourceDevice {
		t.Fatalf("device mode key = %q from %q, want the device token from %q", got, src, KeySourceDevice)
	}

	// With no token yet, device mode reports no credential rather than
	// falling through to a switch key.
	SetDeviceToken("")
	if got, src := APIKey(), APIKeySource(); got != "" || src != KeySourceNone {
		t.Fatalf("device mode with no token = %q from %q, want empty", got, src)
	}
}
