package pi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/launchtest"
)

// A --persist write leaves the client version out of models.json entirely. A
// persisted provider is read by a bare pi, which has no PRIZMAL_CLIENT_VERSION
// and no launch to set one, and Pi 1.6.0 refuses a request whose referenced
// variable is unset: a "$PRIZMAL_CLIENT_VERSION" header there would take every
// request down. The launch sends the version itself, from the sidecar it writes
// per session.
func TestPiPersistWritesNoClientVersionReference(t *testing.T) {
	home := internaltest.SandboxedHome(t)
	envconfig.SetClientVersion("1.4.2")
	t.Cleanup(func() { envconfig.SetClientVersion("") })
	if err := (&Pi{}).Edit([]launch.LaunchModel{{Name: "probe-model"}}); err != nil {
		t.Fatalf("Edit: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(home, piModelsPath))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), envconfig.ClientVersionEnvVar) {
		t.Errorf("models.json refers to %s: %s", envconfig.ClientVersionEnvVar, data)
	}
}

// pi refuses to send a request when the variable is unset or empty, so a launch
// still exports a non-empty value, with or without a version. An entry an older
// build left in the user's models.json may refer to it.
func TestPiAlwaysExportsANonEmptyClientVersion(t *testing.T) {
	for _, tc := range []struct{ name, set, want string }{
		{name: "a release", set: "1.4.2", want: "1.4.2"},
		{name: "no version", set: "", want: "dev-unknown"},
		{name: "a malformed version", set: "1 2", want: "dev-unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			envconfig.SetClientVersion(tc.set)
			t.Cleanup(func() { envconfig.SetClientVersion("") })

			got := internaltest.EnvValue((&Pi{}).envVars(nil), "PRIZMAL_CLIENT_VERSION=")
			if got != tc.want || got == "" {
				t.Errorf("PRIZMAL_CLIENT_VERSION = %q, want %q", got, tc.want)
			}
		})
	}
}

// Every launch writes the running version into the sidecar as a literal, so the
// request carries it whatever the user's models.json says, and exports the
// variable too.
func TestPiDeviceLaunchSendsTheClientVersion(t *testing.T) {
	launchtest.WithDeviceMode(t)
	envconfig.SetBaseURL("https://switch.test")
	envconfig.SetClientVersion("1.4.2")
	internaltest.SandboxedHome(t)
	t.Cleanup(func() {
		envconfig.SetBaseURL("")
		envconfig.SetClientVersion("")
	})

	l, err := piLaunchFor("probe-model", []launch.LaunchModel{{Name: "probe-model"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(l.clean)

	data, err := os.ReadFile(l.configPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct{ Headers map[string]string }
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if got := cfg.Headers["x-prizmal-client-version"]; got != "1.4.2" {
		t.Errorf("sidecar headers = %v, want 1.4.2", cfg.Headers)
	}
	if got := internaltest.EnvValue((&Pi{}).envVars(l), "PRIZMAL_CLIENT_VERSION="); got != "1.4.2" {
		t.Errorf("launch PRIZMAL_CLIENT_VERSION = %q, want 1.4.2", got)
	}
}

// A --persist keeps the operator's other provider headers and drops a
// client-version header an older build wrote, which is the entry that fails a
// bare pi after an upgrade.
func TestPiPersistKeepsOtherProviderHeadersAndDropsTheVersion(t *testing.T) {
	home := internaltest.SandboxedHome(t)
	internaltest.SandboxedSeed(t, home, piModelsPath, `{"providers":{"prizmal":{`+
		`"baseUrl":"https://old.example/v1","api":"openai-completions","apiKey":"$PRIZMAL_SWITCH_KEY",`+
		`"headers":{"X-Team":"blue","X-Prizmal-Client-Version":"$PRIZMAL_CLIENT_VERSION"},`+
		`"models":[{"id":"probe-model","_launch":true}]}}}`)
	if err := (&Pi{}).Edit([]launch.LaunchModel{{Name: "probe-model"}}); err != nil {
		t.Fatalf("Edit: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(home, piModelsPath))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	provider, _ := cfg["providers"].(map[string]any)["prizmal"].(map[string]any)
	headers, _ := provider["headers"].(map[string]any)
	if len(headers) != 1 || headers["X-Team"] != "blue" {
		t.Errorf("headers = %v, want only X-Team kept", headers)
	}
}

// The device-launch test overrides the process-wide Switch URL, so it must put
// the URL back for every test that runs after it.
func TestPiDeviceLaunchTestRestoresTheBaseURL(t *testing.T) {
	before := envconfig.BaseURL()
	t.Run("device launch", TestPiDeviceLaunchSendsTheClientVersion)
	if after := envconfig.BaseURL(); after != before {
		t.Errorf("base URL after the test = %q, want %q", after, before)
	}
}
