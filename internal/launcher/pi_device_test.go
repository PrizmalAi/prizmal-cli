package launch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
)

// withDeviceMode turns device mode on for one test and restores the previous
// state, so a launch test cannot leak the mode into the next one.
func withDeviceMode(t *testing.T) {
	t.Helper()
	previous := envconfig.DeviceMode()
	envconfig.SetDeviceMode(true)
	envconfig.SetDeviceToken("pz-d-dt-test-token")
	t.Cleanup(func() {
		envconfig.SetDeviceMode(previous)
		envconfig.SetDeviceToken("")
	})
}

// TestPiDeviceLaunchWritesExtensionAndConfig is the core of the device-mode
// launch: it lays down the extension and a sidecar config naming the Switch
// endpoint and the launch's models, and hands Pi both paths.
func TestPiDeviceLaunchWritesExtensionAndConfig(t *testing.T) {
	withDeviceMode(t)
	envconfig.SetBaseURL("https://switch.test")

	launch, err := piDeviceLaunchFor("probe-model", []LaunchModel{{Name: "probe-model"}})
	if err != nil {
		t.Fatalf("piDeviceLaunchFor: %v", err)
	}
	t.Cleanup(launch.clean)

	source, err := os.ReadFile(launch.extensionPath)
	if err != nil {
		t.Fatalf("read the extension: %v", err)
	}
	if len(source) == 0 {
		t.Fatal("the extension is empty")
	}
	if !strings.Contains(string(source), "registerProvider") {
		t.Fatal("the extension does not register a provider")
	}

	data, err := os.ReadFile(launch.configPath)
	if err != nil {
		t.Fatalf("read the extension config: %v", err)
	}
	var config piDeviceConfig
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("unmarshal the extension config: %v", err)
	}
	if config.ProviderID != piProviderID {
		t.Errorf("providerID = %q, want %q", config.ProviderID, piProviderID)
	}
	if want := "https://switch.test/v1"; config.BaseURL != want {
		t.Errorf("baseURL = %q, want %q", config.BaseURL, want)
	}
	if len(config.Models) != 1 || config.Models[0].ID != "probe-model" {
		t.Errorf("models = %+v, want one probe-model", config.Models)
	}
}

// TestPiDeviceLaunchHoldsNoCredential pins the credential-at-rest rule for the
// device launch: the extension runs a command for the key, and neither file it
// writes holds key material or a token.
func TestPiDeviceLaunchHoldsNoCredential(t *testing.T) {
	withDeviceMode(t)

	launch, err := piDeviceLaunchFor("probe-model", []LaunchModel{{Name: "probe-model"}})
	if err != nil {
		t.Fatalf("piDeviceLaunchFor: %v", err)
	}
	t.Cleanup(launch.clean)

	for _, path := range []string{launch.extensionPath, launch.configPath} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if strings.Contains(string(data), "pz-d-dt-test-token") {
			t.Errorf("%s holds the device token", path)
		}
	}
}

// TestPiDeviceLaunchRemovesItsDirectory checks the launch leaves nothing
// behind once Pi exits: the extension and config live only for the session.
func TestPiDeviceLaunchRemovesItsDirectory(t *testing.T) {
	withDeviceMode(t)

	launch, err := piDeviceLaunchFor("probe-model", nil)
	if err != nil {
		t.Fatalf("piDeviceLaunchFor: %v", err)
	}
	if _, err := os.Stat(launch.dir); err != nil {
		t.Fatalf("stat the launch directory: %v", err)
	}
	launch.clean()
	if _, err := os.Stat(launch.dir); !os.IsNotExist(err) {
		t.Fatalf("the launch directory survived clean: err = %v", err)
	}
}

// TestPiDeviceLaunchAbsentOutsideDeviceMode pins that a switch-key launch
// writes nothing and passes no extension: the provider entry Edit wrote and
// the key in the environment are the whole configuration.
func TestPiDeviceLaunchAbsentOutsideDeviceMode(t *testing.T) {
	envconfig.SetDeviceMode(false)

	launch, err := piDeviceLaunchFor("probe-model", nil)
	if err != nil {
		t.Fatalf("piDeviceLaunchFor: %v", err)
	}
	if launch != nil {
		t.Fatalf("a switch-key launch wrote a device extension: %+v", launch)
	}
	if got := piLaunchArgs("probe-model", launch, nil); !reflect.DeepEqual(got, []string{"--provider", piProviderID, "--model", "probe-model"}) {
		t.Fatalf("piLaunchArgs = %v, want no extension flag", got)
	}
}

// TestPiDeviceLaunchArgsLoadTheExtension pins that a device-mode launch hands
// Pi the extension by path, ahead of the provider it selects, and that a
// user's own provider choice still wins.
func TestPiDeviceLaunchArgsLoadTheExtension(t *testing.T) {
	extension := &piDeviceLaunch{extensionPath: "/tmp/prizmal-device.ts"}

	got := piLaunchArgs("probe-model", extension, []string{"--print", "hi"})
	want := []string{"--extension", "/tmp/prizmal-device.ts", "--provider", piProviderID, "--model", "probe-model", "--print", "hi"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("piLaunchArgs = %v, want %v", got, want)
	}

	user := []string{"--provider", "anthropic", "--print", "hi"}
	got = piLaunchArgs("probe-model", extension, user)
	want = append([]string{"--extension", "/tmp/prizmal-device.ts"}, user...)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("piLaunchArgs with a user provider = %v, want %v", got, want)
	}
}

// TestPiDeviceEnvCarriesTheCredentialCommand pins the device-mode environment:
// the credential command is this binary's helper, and the config path points
// at the sidecar the extension reads. No key variable reaches the child.
func TestPiDeviceEnvCarriesTheCredentialCommand(t *testing.T) {
	withDeviceMode(t)
	extension := &piDeviceLaunch{configPath: "/tmp/prizmal-device.json"}

	env := (&Pi{}).envVars(extension)
	if got := envValue(env, piDeviceConfigEnv+"="); got != extension.configPath {
		t.Errorf("%s = %q, want %q", piDeviceConfigEnv, got, extension.configPath)
	}
	command := envValue(env, piCredentialCommandEnv+"=")
	if !strings.HasSuffix(command, " auth token") {
		t.Errorf("%s = %q, want a command ending in \"auth token\"", piCredentialCommandEnv, command)
	}
	if got := envValue(env, envconfig.KeyEnvVar+"="); got != "" {
		t.Errorf("%s = %q, want no key in a device-mode environment", envconfig.KeyEnvVar, got)
	}
}

// TestPiDeviceConfigLeadsWithTheLaunchedModel pins the row order: Pi reads
// models[0] as the model a bare launch selects, so the launch's own model has
// to come first, as it does in the models.json Edit writes.
func TestPiDeviceConfigLeadsWithTheLaunchedModel(t *testing.T) {
	envconfig.SetBaseURL("")
	data := piDeviceConfigJSON("chosen", []LaunchModel{{Name: "chosen"}, {Name: "other"}})
	var config piDeviceConfig
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(config.Models) < 2 || config.Models[0].ID != "chosen" {
		t.Fatalf("models = %+v, want chosen first", config.Models)
	}
}

// TestPiDeviceConfigCarriesOneRowWithoutModels covers a launch that resolved a
// model but no catalog rows: the extension still gets that model, so Pi has
// something to select rather than an empty provider.
func TestPiDeviceConfigCarriesOneRowWithoutModels(t *testing.T) {
	data := piDeviceConfigJSON("only", nil)
	var config piDeviceConfig
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(config.Models) != 1 || config.Models[0].ID != "only" {
		t.Fatalf("models = %+v, want only", config.Models)
	}
}

// TestPiDeviceExtensionIsEmbedded checks the extension ships inside the
// binary: the launch writes the embedded source, so the file on disk is the
// one compiled in, with no install step in the path.
func TestPiDeviceExtensionIsEmbedded(t *testing.T) {
	if len(piDeviceExtensionSource) == 0 {
		t.Fatal("the embedded pi extension is empty")
	}
	if !strings.Contains(string(piDeviceExtensionSource), "registerProvider") {
		t.Fatal("the embedded source does not register a provider")
	}
}

// TestPiDeviceConfigPathUnderTempDir checks the sidecar lands in the system
// temp directory, not in the user's home, so a launch leaves no per-session
// file in a place that survives it.
func TestPiDeviceConfigPathUnderTempDir(t *testing.T) {
	withDeviceMode(t)
	launch, err := piDeviceLaunchFor("probe-model", nil)
	if err != nil {
		t.Fatalf("piDeviceLaunchFor: %v", err)
	}
	t.Cleanup(launch.clean)

	temp := os.TempDir()
	resolved, err := filepath.EvalSymlinks(launch.dir)
	if err != nil {
		t.Fatalf("resolve the launch directory: %v", err)
	}
	tempResolved, err := filepath.EvalSymlinks(temp)
	if err != nil {
		t.Fatalf("resolve the temp directory: %v", err)
	}
	if !strings.HasPrefix(resolved, tempResolved) {
		t.Fatalf("launch directory %q is not under the temp directory %q", resolved, tempResolved)
	}
}

// TestPiDeviceEnvPinsTheSwitchHost pins the endpoint for the credential
// command. The command is its own prizmal process and resolves the host from
// scratch, so a launch aimed at a non-default host must hand it that host; a
// command allowed to fall back to production would mint a token the launch's
// own Switch rejects.
func TestPiDeviceEnvPinsTheSwitchHost(t *testing.T) {
	withDeviceMode(t)
	envconfig.SetBaseURL("https://api.staging.prizmal.ai")
	t.Cleanup(func() { envconfig.SetBaseURL("") })

	extension := &piDeviceLaunch{configPath: "/tmp/prizmal-device.json"}
	env := (&Pi{}).envVars(extension)
	if got := envValue(env, envconfig.EnvVar+"="); got != "https://api.staging.prizmal.ai" {
		t.Fatalf("%s = %q, want the launch's own Switch host", envconfig.EnvVar, got)
	}
}
