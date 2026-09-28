package main

import (
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
)

// resetCredentialSources clears every source APIKey() consults so tests
// start from a known-empty state. Never log the values.
func resetCredentialSources(t *testing.T) {
	t.Helper()
	t.Setenv(envconfig.KeyEnvVar, "")
	envconfig.SetAPIKey("")
	envconfig.SetConfigAPIKey("")
	t.Cleanup(func() {
		envconfig.SetAPIKey("")
		envconfig.SetConfigAPIKey("")
	})
}

// TestAPIKeySourceClassifies pins the source line: each of the four cases is
// classified correctly. One value at a time, from a known-clean state.
func TestAPIKeySourceClassifies(t *testing.T) {
	cases := []struct {
		name    string
		envKey  string
		flagKey string
		runE    func(t *testing.T)
		want    KeySource
		wantStr string
	}{
		{
			name: "no sources yields none",
			runE: func(t *testing.T) {},
			want: KeySourceNone, wantStr: "none (unauthenticated)",
		},
		{
			name:   "env var only",
			envKey: "sk-env-only",
			runE:   func(t *testing.T) {},
			want:   KeySourceEnv, wantStr: "$PRIZMAL_SWITCH_KEY",
		},
		{
			name:    "flag beats everything",
			envKey:  "sk-env",
			flagKey: "sk-flag",
			runE:    func(t *testing.T) {},
			want:    KeySourceFlag, wantStr: "--api-key",
		},
		{
			name: "config when flag and env are unset",
			runE: func(t *testing.T) { envconfig.SetConfigAPIKey("sk-from-config") },
			want: KeySourceConfig, wantStr: "config file (~/.prizmal/config.json)",
		},
		{
			name:   "env outranks config",
			envKey: "sk-env",
			runE:   func(t *testing.T) { envconfig.SetConfigAPIKey("sk-from-config") },
			want:   KeySourceEnv, wantStr: "$PRIZMAL_SWITCH_KEY",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			resetCredentialSources(t)
			if tc.envKey != "" {
				t.Setenv(envconfig.KeyEnvVar, tc.envKey)
				// main.go calls SetAPIKey(flagValue) unconditionally; with no
				// config/flag, the env var is what APIKey() resolves.
			}
			if tc.flagKey != "" {
				envconfig.SetAPIKey(tc.flagKey)
			}
			tc.runE(t)
			got := apiKeySource()
			if got != tc.want {
				t.Errorf("apiKeySource() = %q, want %q", got, tc.want)
			}
			if tc.wantStr != "" && string(got) != tc.wantStr {
				t.Errorf("source name = %q, want %q", got, tc.wantStr)
			}
		})
	}
}

// TestAnnounceKeySourceNeverContainsValue is the disclosure pin: the
// announcement line names the source and must never contain the key value
// itself, not for any source.
func TestAnnounceKeySourceNeverContainsValue(t *testing.T) {
	keys := map[KeySource]string{
		KeySourceFlag:   "sk-flag-secret-0001",
		KeySourceEnv:    "sk-env-secret-0002",
		KeySourceConfig: "sk-config-secret-0003",
	}
	for source, value := range keys {
		t.Run(string(source), func(t *testing.T) {
			resetCredentialSources(t)
			switch source {
			case KeySourceFlag:
				envconfig.SetAPIKey(value)
			case KeySourceEnv:
				t.Setenv(envconfig.KeyEnvVar, value)
			case KeySourceConfig:
				envconfig.SetConfigAPIKey(value)
			}
			var sb strings.Builder
			announceKeySource(&sb)
			out := sb.String()
			if !strings.HasPrefix(out, "using api key from: ") {
				t.Fatalf("announcement has wrong shape: %q", out)
			}
			if strings.Contains(out, value) {
				t.Fatalf("announcement for %s must never contain the key value", source)
			}
			if strings.Contains(out, "sk-") {
				t.Fatalf("announcement for %s contains a key-shaped token: %q", source, out)
			}
			// And the refusal path, separately, also never leaks.
			if err := requireCredentialForRemote(); err != nil && strings.Contains(err.Error(), value) {
				t.Fatalf("refusal message leaked the key value for %s", source)
			}
		})
	}
}

// TestAnnounceKeySourceNoneCase pins the none wording and, for a remote
// target, that a refusal follows with all three sources named.
func TestAnnounceKeySourceNoneCase(t *testing.T) {
	resetCredentialSources(t)
	var sb strings.Builder
	announceKeySource(&sb)
	if !strings.Contains(sb.String(), "none") {
		t.Errorf("none case not announced: %q", sb.String())
	}

	t.Run("refusal names all three key sources", func(t *testing.T) {
		resetCredentialSources(t)
		envconfig.SetBaseURL("https://switch.example.test")
		t.Cleanup(func() { envconfig.SetBaseURL("") })
		err := requireCredentialForRemote()
		if err == nil {
			t.Fatal("expected a refusal for a keyless remote launch")
		}
		msg := err.Error()
		for _, want := range []string{"--api-key", "config file", "PRIZMAL_SWITCH_KEY"} {
			if !strings.Contains(msg, want) {
				t.Errorf("refusal message must name %q\nmessage: %s", want, msg)
			}
		}
		if strings.Contains(msg, "sk-") {
			t.Errorf("refusal message contains a key-shaped token\nmessage: %s", msg)
		}
		// The gate guards a listing as well as a launch, so the wording must
		// not promise a launch: a reader of `prizmal --list` told "refusing to
		// launch" is told about something that was never going to happen.
		if strings.Contains(msg, "launch") {
			t.Errorf("refusal wording names a launch, but it also guards a listing\nmessage: %s", msg)
		}
	})
}

// TestUnauthenticatedRemoteRefused pins the launch gate: a launch aimed at a
// remote host with no credential must be refused loudly; loopback stays a
// supported unauthenticated mode, and --allow-unauthenticated is the escape
// hatch.
func TestUnauthenticatedRemoteRefused(t *testing.T) {
	remoteURL := "https://switch.example.test"

	defer func() {
		envconfig.SetBaseURL("")
		envconfig.SetAPIKey("")
	}()

	t.Run("remote URL with no key is refused", func(t *testing.T) {
		resetCredentialSources(t)
		envconfig.SetBaseURL(remoteURL)
		err := requireCredentialForRemote()
		if err == nil {
			t.Fatal("remote launch with no credential must be refused, but was allowed")
		}
		for _, want := range []string{"--api-key", "PRIZMAL_SWITCH_KEY"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("refusal message must name %q\nmessage: %s", want, err.Error())
			}
		}
	})

	t.Run("loopback URL with no key is allowed", func(t *testing.T) {
		resetCredentialSources(t)
		envconfig.SetBaseURL("http://127.0.0.1:8080")
		if err := requireCredentialForRemote(); err != nil {
			t.Errorf("loopback launch with no key must stay supported: %v", err)
		}
	})

	t.Run("localhost URL with no key is allowed", func(t *testing.T) {
		resetCredentialSources(t)
		envconfig.SetBaseURL("http://localhost:8080")
		if err := requireCredentialForRemote(); err != nil {
			t.Errorf("localhost launch with no key must stay supported: %v", err)
		}
	})

	t.Run("remote URL with a key is allowed", func(t *testing.T) {
		resetCredentialSources(t)
		envconfig.SetBaseURL(remoteURL)
		envconfig.SetAPIKey("sk-any")
		if err := requireCredentialForRemote(); err != nil {
			t.Errorf("remote launch with a credential must not be refused: %v", err)
		}
	})

	t.Run("remote URL with no key but escape hatch allowed", func(t *testing.T) {
		resetCredentialSources(t)
		envconfig.SetBaseURL(remoteURL)
		allowUnauthenticated = true
		t.Cleanup(func() { allowUnauthenticated = false })
		if err := requireCredentialForRemote(); err != nil {
			t.Errorf("--allow-unauthenticated must let a keyless remote launch proceed: %v", err)
		}
	})
}
