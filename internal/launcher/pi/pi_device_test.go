package pi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/launchtest"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/selfpath"
)

// TestPiLaunchWritesExtensionAndConfig is the core of every launch: it lays
// down the extension and a sidecar config naming the Switch endpoint and the
// launch's models, and hands Pi both paths.
func TestPiLaunchWritesExtensionAndConfig(t *testing.T) {
	launchtest.WithDeviceMode(t)
	envconfig.SetBaseURL("https://switch.test")
	internaltest.SandboxedHome(t)
	t.Cleanup(func() { envconfig.SetBaseURL("") })

	l, err := piLaunchFor("probe-model", []launch.LaunchModel{{Name: "probe-model"}})
	if err != nil {
		t.Fatalf("piLaunchFor: %v", err)
	}
	t.Cleanup(l.clean)

	source, err := os.ReadFile(l.extensionPath)
	if err != nil {
		t.Fatalf("read the extension: %v", err)
	}
	if len(source) == 0 {
		t.Fatal("the extension is empty")
	}
	if !strings.Contains(string(source), "registerProvider") {
		t.Fatal("the extension does not register a provider")
	}

	data, err := os.ReadFile(l.configPath)
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
	if config.APIKey != "" {
		t.Errorf("apiKey = %q in a device-mode launch, want none: the credential is the command", config.APIKey)
	}
}

// TestPiLaunchHoldsNoCredential pins the credential-at-rest rule: the extension
// runs a command for the key in device mode and names the environment variable
// in switch-key mode, and no file the launch writes holds the key, a token or
// the credential command's output.
func TestPiLaunchHoldsNoCredential(t *testing.T) {
	for _, device := range []bool{true, false} {
		name := "switch key"
		if device {
			name = "device"
		}
		t.Run(name, func(t *testing.T) {
			if device {
				launchtest.WithDeviceMode(t)
			} else {
				envconfig.SetDeviceMode(false)
			}
			internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)
			internaltest.SandboxedHome(t)

			l, err := piLaunchFor("probe-model", []launch.LaunchModel{{Name: "probe-model"}})
			if err != nil {
				t.Fatalf("piLaunchFor: %v", err)
			}
			t.Cleanup(l.clean)

			paths, err := filepath.Glob(filepath.Join(l.dir, "*"))
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range paths {
				info, err := os.Lstat(path)
				if err != nil {
					t.Fatal(err)
				}
				if info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
					continue
				}
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				for _, secret := range []string{internaltest.FakeKeyAtRest, "pz-d-dt-test-token"} {
					if strings.Contains(string(data), secret) {
						t.Errorf("%s holds a credential", path)
					}
				}
			}
		})
	}
}

// TestPiLaunchRemovesItsDirectory checks the launch leaves nothing behind once
// Pi exits: the extension, its config and the agent directory live only for the
// session.
func TestPiLaunchRemovesItsDirectory(t *testing.T) {
	launchtest.WithDeviceMode(t)
	internaltest.SandboxedHome(t)

	l, err := piLaunchFor("probe-model", nil)
	if err != nil {
		t.Fatalf("piLaunchFor: %v", err)
	}
	if _, err := os.Stat(l.dir); err != nil {
		t.Fatalf("stat the launch directory: %v", err)
	}
	l.clean()
	if _, err := os.Stat(l.dir); !os.IsNotExist(err) {
		t.Fatalf("the launch directory survived clean: err = %v", err)
	}
}

// TestPiSwitchKeyLaunchRegistersTheSwitchProvider pins the switch-key half of
// the ephemeral contract: the launch writes a sidecar naming the key variable
// and hands Pi the extension and the config, because the extension registers
// the provider that models.json used to hold.
func TestPiSwitchKeyLaunchRegistersTheSwitchProvider(t *testing.T) {
	envconfig.SetDeviceMode(false)
	internaltest.SandboxedHome(t)

	l, err := piLaunchFor("probe-model", nil)
	if err != nil {
		t.Fatalf("piLaunchFor: %v", err)
	}
	t.Cleanup(l.clean)
	if l.extensionPath == "" || l.configPath == "" {
		t.Fatalf("launch = %+v, want an extension and a config", l)
	}
	if got := piLaunchArgs("probe-model", l, nil); !reflect.DeepEqual(got, []string{"--extension", l.extensionPath, "--provider", piProviderID, "--model", "probe-model"}) {
		t.Fatalf("piLaunchArgs = %v, want the extension flag", got)
	}

	data, err := os.ReadFile(l.configPath)
	if err != nil {
		t.Fatalf("read the sidecar: %v", err)
	}
	var config piDeviceConfig
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("unmarshal the sidecar: %v", err)
	}
	if config.APIKey != piAPIKeyReference {
		t.Fatalf("sidecar apiKey = %q, want %q", config.APIKey, piAPIKeyReference)
	}

	env := (&Pi{}).envVars(l)
	if got := launchtest.EnvValue(env, envconfig.KeyEnvVar+"="); got == "" && envconfig.APIKey() != "" {
		t.Errorf("%s missing from a switch-key launch environment", envconfig.KeyEnvVar)
	}
	if got := launchtest.EnvValue(env, piDeviceConfigEnv+"="); got != l.configPath {
		t.Errorf("%s = %q, want %q", piDeviceConfigEnv, got, l.configPath)
	}
	if got := launchtest.EnvValue(env, piCredentialCommandEnv+"="); got != "" {
		t.Errorf("%s = %q in a switch-key launch, want none", piCredentialCommandEnv, got)
	}
}

// TestPiLaunchArgsLoadTheExtension pins that a launch hands Pi the extension by
// path, ahead of the provider it selects, and that a user's own provider choice
// still wins.
func TestPiLaunchArgsLoadTheExtension(t *testing.T) {
	l := &piLaunch{extensionPath: "/tmp/prizmal-device.ts"}

	got := piLaunchArgs("probe-model", l, []string{"--print", "hi"})
	want := []string{"--extension", "/tmp/prizmal-device.ts", "--provider", piProviderID, "--model", "probe-model", "--print", "hi"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("piLaunchArgs = %v, want %v", got, want)
	}

	user := []string{"--provider", "anthropic", "--print", "hi"}
	got = piLaunchArgs("probe-model", l, user)
	want = append([]string{"--extension", "/tmp/prizmal-device.ts"}, user...)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("piLaunchArgs with a user provider = %v, want %v", got, want)
	}
}

// TestPiDeviceEnvCarriesTheCredentialCommand pins the device-mode environment:
// the credential command is this binary's helper, and the config path points at
// the sidecar the extension reads. No key variable reaches the child.
func TestPiDeviceEnvCarriesTheCredentialCommand(t *testing.T) {
	launchtest.WithDeviceMode(t)
	l := &piLaunch{configPath: "/tmp/prizmal-device.json", agentDir: "/tmp/agent", device: true}

	env := (&Pi{}).envVars(l)
	if got := launchtest.EnvValue(env, piDeviceConfigEnv+"="); got != l.configPath {
		t.Errorf("%s = %q, want %q", piDeviceConfigEnv, got, l.configPath)
	}
	command := launchtest.EnvValue(env, piCredentialCommandEnv+"=")
	if !strings.HasSuffix(command, " auth token") {
		t.Errorf("%s = %q, want a command ending in \"auth token\"", piCredentialCommandEnv, command)
	}
	if got := launchtest.EnvValue(env, envconfig.KeyEnvVar+"="); got != "" {
		t.Errorf("%s is set in a device-mode environment, want no key", envconfig.KeyEnvVar)
	}
	if got := launchtest.EnvValue(env, piAgentDirEnv+"="); got != l.agentDir {
		t.Errorf("%s = %q, want %q", piAgentDirEnv, got, l.agentDir)
	}
}

// TestPiCredentialPathDefaultsToSelfpath is the regression guard: the command
// must record selfpath.Path's answer, not os.Executable's resolved target. A
// revert to os.Executable here would restore the bug this change fixes.
func TestPiCredentialPathDefaultsToSelfpath(t *testing.T) {
	if reflect.ValueOf(piExecutablePath).Pointer() != reflect.ValueOf(selfpath.Path).Pointer() {
		t.Error("piExecutablePath does not default to selfpath.Path; a launch would record os.Executable's resolved target")
	}
}

// TestPiCredentialCommandCarriesTheTypedPath pins the wiring: the credential
// command is the resolver's path, quoted as one shell word, with the helper
// subcommand after it. A path with a space stays one word, because pi runs the
// string through a shell.
func TestPiCredentialCommandCarriesTheTypedPath(t *testing.T) {
	typed := "/opt/chain home/prizmal"
	previous := piExecutablePath
	piExecutablePath = func() (string, error) { return typed, nil }
	t.Cleanup(func() { piExecutablePath = previous })

	if got, want := piCredentialCommand(), strconv.Quote(typed)+" auth token"; got != want {
		t.Errorf("piCredentialCommand = %q, want %q", got, want)
	}
}

// TestPiDeviceConfigLeadsWithTheLaunchedModel pins the row order: Pi reads
// models[0] as the model a bare launch selects, so the launch's own model has
// to come first.
func TestPiDeviceConfigLeadsWithTheLaunchedModel(t *testing.T) {
	envconfig.SetBaseURL("")
	data := piDeviceConfigJSON("chosen", []launch.LaunchModel{{Name: "chosen"}, {Name: "other"}}, true, nil)
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
	data := piDeviceConfigJSON("only", nil, true, nil)
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

// TestPiLaunchDirectoryIsUnderTempDir checks the sidecar lands in the system
// temp directory, not in the user's home, so a launch leaves no per-session
// file in a place that survives it.
func TestPiLaunchDirectoryIsUnderTempDir(t *testing.T) {
	launchtest.WithDeviceMode(t)
	internaltest.SandboxedHome(t)
	l, err := piLaunchFor("probe-model", nil)
	if err != nil {
		t.Fatalf("piLaunchFor: %v", err)
	}
	t.Cleanup(l.clean)

	temp := os.TempDir()
	resolved, err := filepath.EvalSymlinks(l.dir)
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
	launchtest.WithDeviceMode(t)
	envconfig.SetBaseURL("https://api.example.invalid")
	t.Cleanup(func() { envconfig.SetBaseURL("") })

	l := &piLaunch{configPath: "/tmp/prizmal-device.json", agentDir: "/tmp/agent", device: true}
	env := (&Pi{}).envVars(l)
	if got := launchtest.EnvValue(env, envconfig.EnvVar+"="); got != "https://api.example.invalid" {
		t.Fatalf("%s = %q, want the launch's own Switch host", envconfig.EnvVar, got)
	}
}

// TestPiChildEnvDropsAnInheritedCredentialCommand pins the nested-launch case:
// a launch started from inside another launch inherits that launch's
// device-token command, and the extension prefers a command over the sidecar's
// switch-key reference. A switch-key or relay launch must not carry it, and a
// device launch must carry its own.
func TestPiChildEnvDropsAnInheritedCredentialCommand(t *testing.T) {
	t.Setenv(piCredentialCommandEnv, "/outer/prizmal auth token")
	envconfig.SetDeviceMode(false)

	env := (&Pi{}).childEnv(nil)
	if got := launchtest.EnvValue(env, piCredentialCommandEnv+"="); got != "" {
		t.Fatalf("%s = %q in a switch-key launch, want none", piCredentialCommandEnv, got)
	}

	launchtest.WithDeviceMode(t)
	l := &piLaunch{configPath: "/tmp/prizmal-device.json", agentDir: "/tmp/agent", device: true}
	if got := launchtest.EnvValue((&Pi{}).childEnv(l), piCredentialCommandEnv+"="); !strings.HasSuffix(got, " auth token") {
		t.Fatalf("%s = %q in a device launch, want the launch's own command", piCredentialCommandEnv, got)
	}
}
