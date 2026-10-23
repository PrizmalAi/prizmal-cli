package cline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/PrizmalAi/prizmal-cli/internal/relay"
)

func TestClineDeviceLaunchUsesRelayAndRestoresDirectURL(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake cline is a shell script")
	}
	for _, tc := range []struct {
		name string
		args func(string) []string
		env  func(*testing.T, string)
	}{
		{
			name: "data-dir environment",
			args: func(string) []string { return []string{"-y", "task"} },
			env:  func(t *testing.T, dir string) { t.Setenv("CLINE_DATA_DIR", dir) },
		},
		{
			name: "data-dir argument",
			args: func(dir string) []string { return []string{"--data-dir", dir, "-y", "task"} },
			env:  func(t *testing.T, _ string) { t.Setenv("CLINE_DATA_DIR", "") },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			internaltest.SandboxedHome(t)
			internaltest.ClearCredentialEnv(t)
			t.Setenv("CLINE_DIR", "")
			dataDir := filepath.Join(t.TempDir(), "cline-data")
			tc.env(t, dataDir)

			envconfig.SetBaseURL("https://switch.example.test")
			envconfig.SetDeviceMode(true)
			envconfig.SetDeviceToken("pz-s-dt-current")
			r, err := relay.New(envconfig.UnversionedBaseURL(), func(context.Context) (string, error) {
				return "pz-s-dt-current", nil
			})
			if err != nil {
				t.Fatal(err)
			}
			envconfig.SetRelay(r.URL(), r.Placeholder())
			t.Cleanup(func() {
				envconfig.ClearRelay()
				r.Close()
				envconfig.SetBaseURL("")
				envconfig.SetDeviceMode(false)
				envconfig.SetDeviceToken("")
			})

			c := &Cline{}
			if err := c.Edit([]launch.LaunchModel{{Name: "probe-model"}}); err != nil {
				t.Fatal(err)
			}
			providersPath := clineProvidersPath(dataDir)
			statePath := clineLegacyGlobalStatePath(dataDir)
			if _, err := os.Stat(providersPath); os.IsNotExist(err) {
				if err := writeClineDataDir(dataDir, true, "probe-model"); err != nil {
					t.Fatal(err)
				}
			}
			stateBefore, err := launch.ReadJSONFile(statePath)
			if err != nil {
				t.Fatal(err)
			}
			actProviderBefore, _ := stateBefore["actModeApiProvider"].(string)
			planProviderBefore, _ := stateBefore["planModeApiProvider"].(string)
			providersBefore, err := launch.ReadJSONFile(providersPath)
			if err != nil {
				t.Fatal(err)
			}
			lastUsedBefore, _ := providersBefore["lastUsedProvider"].(string)

			captureProviders := filepath.Join(t.TempDir(), "providers.json")
			captureState := filepath.Join(t.TempDir(), "globalState.json")
			captureKey := filepath.Join(t.TempDir(), "openai-key")
			bin := t.TempDir()
			script := fmt.Sprintf("#!/bin/sh\ncp %q %q\ncp %q %q\nprintf '%%s' \"$OPENAI_API_KEY\" > %q\n",
				providersPath, captureProviders, statePath, captureState, captureKey)
			if err := os.WriteFile(filepath.Join(bin, "cline"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

			if err := c.Run("probe-model", nil, tc.args(dataDir)); err != nil {
				t.Fatal(err)
			}

			var runningProviders map[string]any
			data, err := os.ReadFile(captureProviders)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, &runningProviders); err != nil {
				t.Fatal(err)
			}
			relayBase := activeProviderBaseURL(t, runningProviders)
			if !regexp.MustCompile(`^http://(127\.0\.0\.1|localhost):[0-9]+/v1$`).MatchString(relayBase) {
				t.Fatalf("provider baseUrl during launch = %q, want the loopback relay", relayBase)
			}
			key, err := os.ReadFile(captureKey)
			if err != nil {
				t.Fatal(err)
			}
			if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(string(key)) {
				t.Fatalf("OPENAI_API_KEY during launch = %q, want a 64-character hex placeholder", key)
			}

			var runningState map[string]any
			data, err = os.ReadFile(captureState)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, &runningState); err != nil {
				t.Fatal(err)
			}
			relayHost := strings.TrimSuffix(relayBase, "/v1")
			for _, key := range []string{
				"openAiCompatibleBaseUrl",
				"actModeOpenAiCompatibleBaseUrl",
				"planModeOpenAiCompatibleBaseUrl",
			} {
				if got, _ := runningState[key].(string); got != relayHost {
					t.Errorf("%s during launch = %q, want %q", key, got, relayHost)
				}
			}
			if got, _ := runningState["actModeApiProvider"].(string); got != actProviderBefore {
				t.Errorf("actModeApiProvider during launch = %q, want %q", got, actProviderBefore)
			}
			if got, _ := runningState["planModeApiProvider"].(string); got != planProviderBefore {
				t.Errorf("planModeApiProvider during launch = %q, want %q", got, planProviderBefore)
			}

			var restored map[string]any
			data, err = os.ReadFile(providersPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, &restored); err != nil {
				t.Fatal(err)
			}
			if got := activeProviderBaseURL(t, restored); got != "https://switch.example.test/v1" {
				t.Fatalf("provider baseUrl after launch = %q, want the direct Switch URL", got)
			}
			if got, _ := restored["lastUsedProvider"].(string); got != lastUsedBefore {
				t.Errorf("lastUsedProvider after launch = %q, want %q", got, lastUsedBefore)
			}
			state, err := os.ReadFile(statePath)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(state, &runningState); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{
				"openAiCompatibleBaseUrl",
				"actModeOpenAiCompatibleBaseUrl",
				"planModeOpenAiCompatibleBaseUrl",
			} {
				if got, _ := runningState[key].(string); got != "https://switch.example.test" {
					t.Errorf("%s after launch = %q, want the direct Switch host", key, got)
				}
			}
			if got, _ := runningState["actModeApiProvider"].(string); got != actProviderBefore {
				t.Errorf("actModeApiProvider after launch = %q, want %q", got, actProviderBefore)
			}
			if got, _ := runningState["planModeApiProvider"].(string); got != planProviderBefore {
				t.Errorf("planModeApiProvider after launch = %q, want %q", got, planProviderBefore)
			}
		})
	}
}

func TestClineSupportsDeviceMode(t *testing.T) {
	if !(&Cline{}).SupportsDeviceMode() {
		t.Fatal("Cline does not support device mode")
	}
}

func activeProviderBaseURL(t *testing.T, config map[string]any) string {
	t.Helper()
	lastUsed, _ := config["lastUsedProvider"].(string)
	providers, _ := config["providers"].(map[string]any)
	provider, _ := providers[lastUsed].(map[string]any)
	settings, _ := provider["settings"].(map[string]any)
	baseURL, _ := settings["baseUrl"].(string)
	if baseURL == "" {
		t.Fatalf("providers[%q].settings.baseUrl is missing", lastUsed)
	}
	return baseURL
}
