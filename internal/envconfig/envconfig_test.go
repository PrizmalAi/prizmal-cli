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
