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

const wantVersionRef = "$PRIZMAL_CLIENT_VERSION"

// models.json names the variable, so the file stays right when the CLI upgrades.
func TestPiModelsJSONNamesTheClientVersionVariable(t *testing.T) {
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
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	provider := cfg["providers"].(map[string]any)["prizmal"].(map[string]any)
	headers, _ := provider["headers"].(map[string]any)
	if got := headers["x-prizmal-client-version"]; got != wantVersionRef {
		t.Errorf("headers = %v, want x-prizmal-client-version %s", headers, wantVersionRef)
	}
	if raw := string(data); strings.Contains(raw, "1.4.2") {
		t.Errorf("models.json contains the version itself: %s", raw)
	}
}

// pi refuses to send a request when the variable is unset or empty, so the
// launch always exports a non-empty value, with or without a version.
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

// A device-mode launch carries no models.json reference: the extension
// registers the provider, so it takes the header from the sidecar config, and
// the variable is exported too.
func TestPiDeviceLaunchSendsTheClientVersion(t *testing.T) {
	launchtest.WithDeviceMode(t)
	envconfig.SetBaseURL("https://switch.test")
	envconfig.SetClientVersion("1.4.2")
	t.Cleanup(func() { envconfig.SetClientVersion("") })

	l, err := piDeviceLaunchFor("probe-model", []launch.LaunchModel{{Name: "probe-model"}})
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
		t.Errorf("device launch PRIZMAL_CLIENT_VERSION = %q, want 1.4.2", got)
	}
}

// Edit re-asserts the version reference on a provider entry that exists, keeps
// the operator's other headers, and replaces a client-version header of theirs.
func TestPiEditKeepsOtherProviderHeaders(t *testing.T) {
	home := internaltest.SandboxedHome(t)
	internaltest.SandboxedSeed(t, home, piModelsPath, `{"providers":{"prizmal":{`+
		`"baseUrl":"https://old.example/v1","api":"openai-completions","apiKey":"$PRIZMAL_SWITCH_KEY",`+
		`"headers":{"X-Team":"blue","X-Prizmal-Client-Version":"spoofed"},`+
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
	headers, _ := cfg["providers"].(map[string]any)["prizmal"].(map[string]any)["headers"].(map[string]any)
	want := map[string]any{"X-Team": "blue", "x-prizmal-client-version": wantVersionRef}
	if len(headers) != len(want) || headers["X-Team"] != "blue" || headers["x-prizmal-client-version"] != wantVersionRef {
		t.Errorf("headers = %v, want %v", headers, want)
	}
}
