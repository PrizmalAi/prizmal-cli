package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/launchtest"
)

func TestOpenCodeSupportsDeviceMode(t *testing.T) {
	if !(&OpenCode{}).SupportsDeviceMode() {
		t.Fatal("OpenCode must report device mode: its launch loads a plugin that refreshes the token")
	}
}

// In device mode the plugin is the credential, so the inline config must hold
// no apiKey: a stale device token there would be sent as the static header.
func TestOpenCodeDeviceConfigCarriesNoAPIKey(t *testing.T) {
	launchtest.WithDeviceMode(t)
	options := opencodePrizmalProvider(t, wireKeyTestModel()[0], wireKeyTestModel())["options"].(map[string]any)
	if key, present := options["apiKey"]; present {
		t.Fatalf("device-mode config carries apiKey %v; the plugin must be the only credential", key)
	}
	if options["baseURL"] == "" {
		t.Fatal("device-mode config lost its baseURL")
	}
}

func TestOpenCodeSwitchKeyConfigKeepsAPIKey(t *testing.T) {
	envconfig.SetAPIKey("sk-static")
	t.Cleanup(func() { envconfig.SetAPIKey("") })
	options := opencodePrizmalProvider(t, wireKeyTestModel()[0], wireKeyTestModel())["options"].(map[string]any)
	if options["apiKey"] != "sk-static" {
		t.Fatalf("apiKey = %v, want sk-static", options["apiKey"])
	}
}

func TestOpenCodeDeviceLaunchWritesAuthPlugin(t *testing.T) {
	home := sandboxedHome(t)
	launchtest.WithDeviceMode(t)

	env := (&OpenCode{}).envVars("wire-test-model", wireKeyTestModel())

	dir, ok := envLookup(env, "OPENCODE_CONFIG_DIR")
	if !ok {
		t.Fatal("device launch sets no OPENCODE_CONFIG_DIR; the plugin would never load")
	}
	if !strings.HasPrefix(dir, home) {
		t.Fatalf("config dir %q is outside the sandboxed home", dir)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "plugin", "prizmal_auth.ts"))
	if err != nil {
		t.Fatalf("auth plugin missing: %v", err)
	}
	source := string(raw)
	for _, want := range []string{`"chat.headers"`, `providerID !== "prizmal"`, "auth token", `"Authorization"`, "Bearer "} {
		if !strings.Contains(source, want) {
			t.Errorf("auth plugin lacks %q", want)
		}
	}
	if bin, _ := envLookup(env, openCodeAuthBinEnv); bin == "" {
		t.Errorf("%s is unset; the plugin would have to find prizmal on PATH", openCodeAuthBinEnv)
	}
}

// The plugin must leave no header, rather than throw, when the command fails.
func TestOpenCodeAuthPluginTreatsAFailedCommandAsNoHeader(t *testing.T) {
	source := openCodeAuthPluginSource()
	for _, want := range []string{".nothrow()", "exitCode", "catch"} {
		if !strings.Contains(source, want) {
			t.Errorf("auth plugin does not handle a failed command: missing %q", want)
		}
	}
	if strings.Contains(source, "output.headers[\"Authorization\"] = \"Bearer \" + token\n") &&
		!strings.Contains(source, "if (token)") {
		t.Error("auth plugin sets the header without checking for an empty token")
	}
}

func TestOpenCodeDeviceLaunchPinsTheSwitchURLForTheHelper(t *testing.T) {
	sandboxedHome(t)
	launchtest.WithDeviceMode(t)
	envconfig.SetBaseURL("https://switch.example")
	t.Cleanup(func() { envconfig.SetBaseURL("") })

	env := (&OpenCode{}).envVars("wire-test-model", wireKeyTestModel())
	if url, _ := envLookup(env, envconfig.EnvVar); url != "https://switch.example" {
		t.Fatalf("%s = %q; the helper would fall back to another host", envconfig.EnvVar, url)
	}
}

func TestOpenCodeSwitchKeyLaunchWritesNoAuthPlugin(t *testing.T) {
	sandboxedHome(t)
	o := &OpenCode{}
	dirEnv := func() string {
		d, _ := envLookup(o.envVars("wire-test-model", wireKeyTestModel()), "OPENCODE_CONFIG_DIR")
		return d
	}

	// A device launch first, which leaves the plugin behind.
	launchtest.WithDeviceMode(t)
	if dirEnv() == "" {
		t.Fatal("device launch sets no config dir")
	}
	envconfig.SetDeviceMode(false)
	envconfig.SetDeviceToken("")

	dir := dirEnv()
	if dir == "" {
		t.Fatal("switch-key launch sets no OPENCODE_CONFIG_DIR; the search tool would not load")
	}
	if _, err := os.Stat(filepath.Join(dir, "plugin", "prizmal_auth.ts")); !os.IsNotExist(err) {
		t.Fatalf("stale auth plugin survived a switch-key launch (stat err: %v)", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "tool", openCodeSearchToolID+".ts")); err != nil {
		t.Fatalf("search tool lost: %v", err)
	}
}

func TestOpenCodeDeviceInlineConfigIsValidJSON(t *testing.T) {
	launchtest.WithDeviceMode(t)
	content, err := buildInlineConfig(wireKeyTestModel()[0], wireKeyTestModel())
	if err != nil || !json.Valid([]byte(content)) {
		t.Fatalf("invalid device config: %v", err)
	}
}

// In device mode the search tool must not be handed the launch's device token:
// it would expire ten minutes in. The tool runs the auth command per call.
func TestOpenCodeDeviceLaunchPutsNoTokenInTheSearchEnv(t *testing.T) {
	sandboxedHome(t)
	launchtest.WithDeviceMode(t)
	env := (&OpenCode{}).envVars("wire-test-model", wireKeyTestModel())
	if key, _ := envLookup(env, openCodeSearchKeyEnv); key != "" {
		t.Fatalf("%s = %q in device mode; the token would expire mid-session", openCodeSearchKeyEnv, key)
	}
	if bin, _ := envLookup(env, openCodeAuthBinEnv); bin == "" {
		t.Fatalf("%s unset; the search tool cannot run the auth command", openCodeAuthBinEnv)
	}
}

func TestOpenCodeSearchToolRunsTheAuthCommandPerCall(t *testing.T) {
	source := openCodeSearchToolSource()
	for _, want := range []string{openCodeAuthBinEnv, "auth", "token"} {
		if !strings.Contains(source, want) {
			t.Errorf("search tool does not use %q", want)
		}
	}
}
