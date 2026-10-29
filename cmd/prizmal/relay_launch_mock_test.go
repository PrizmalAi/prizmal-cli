package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/launcher/mockbin"
	"github.com/PrizmalAi/prizmal-cli/internal/stubserver"
)

func TestEveryHarnessUsesRelayOnlyWhenRequestedExceptCline(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess test in short mode")
	}

	prizmalBin := filepath.Join(t.TempDir(), "prizmal")
	if runtime.GOOS == "windows" {
		prizmalBin += ".exe"
	}
	build := exec.Command("go", "build", "-o", prizmalBin, ".")
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build prizmal: %v\n%s", err, out)
	}
	mockBin := buildMockBinary(t)

	for _, expectation := range allHarnessExpectations() {
		expectation := expectation
		for _, mode := range []struct {
			name string
			flag bool
		}{
			{name: "relay", flag: true},
			{name: "direct", flag: false},
		} {
			mode := mode
			t.Run(expectation.name+"/"+mode.name, func(t *testing.T) {
				home, expDir, stubURL := setupMockEnv(t, func() string { return mockBin }, expectation)
				capture := map[string]string{}
				switch expectation.name {
				case "cline":
					capture[filepath.Join(home, ".cline", "data", "settings", "providers.json")] = "cline.providers.json"
					capture[filepath.Join(home, ".cline", "data", "globalState.json")] = "cline.globalState.json"
				case "pi":
					capture[filepath.Join(home, ".pi", "agent", "models.json")] = "pi.models.json"
				}
				if len(capture) > 0 {
					addMockCaptureFiles(t, expDir, capture)
				}

				flags := []string{"--yes", "--model", mockLaunchModel}
				if mode.flag {
					flags = append(flags, "--relay")
				}
				exitCode, stdout, stderr := runPrizmalLaunch(t, prizmalBin, home, expDir, expectation.name, flags, nil)
				if exitCode != 0 {
					t.Fatalf("prizmal %s exited %d\nstdout: %s\nstderr: %s", expectation.name, exitCode, stdout, stderr)
				}

				envDump, args := readMockInvocation(t, expDir, expectation.name)
				if mode.flag || expectation.name == "cline" || expectation.name == "kilo" {
					sourceAt := strings.Index(stderr, "using api key from:")
					relayAt := strings.Index(stderr, "relaying through ")
					if sourceAt < 0 || relayAt < 0 || sourceAt > relayAt {
						t.Fatalf("the real credential source was not announced before the relay:\n%s", stderr)
					}
					relayURL := relayURLFromStderr(t, stderr)
					relayHost := strings.TrimSuffix(relayURL, "/v1")
					childHost := strings.Replace(relayHost, "127.0.0.1", "localhost", 1)
					assertHarnessRelayWiring(t, expectation.name, home, expDir, envDump, args, childHost, true)
				} else {
					if strings.Contains(stderr, "relaying through ") {
						t.Fatalf("a direct launch announced a relay:\n%s", stderr)
					}
					assertHarnessRelayWiring(t, expectation.name, home, expDir, envDump, args, stubEndpointName(stubURL), false)
				}
			})
		}
	}
}

func TestRelayDoesNotBypassTheMissingCredentialGate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess test in short mode")
	}
	prizmalBin := buildPrizmalForMock(t)
	mockBin := buildMockBinary(t)
	expectation := harnessExpectation{
		name:         "claude",
		binaryNames:  []string{"claude"},
		requiredEnv:  []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN"},
		forbiddenEnv: []string{"ANTHROPIC_API_KEY"},
	}
	home, expDir, _ := setupMockEnv(t, func() string { return mockBin }, expectation)
	configPath := filepath.Join(home, ".prizmal", "config.json")
	config := map[string]any{
		"version":       1,
		"default_model": mockLaunchModel,
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	exitCode, _, stderr := runPrizmalLaunch(
		t,
		prizmalBin,
		home,
		expDir,
		"claude",
		[]string{"--yes", "--model", mockLaunchModel, "--relay", "--url", "http://127.0.0.2:1"},
		nil,
	)
	if exitCode == 0 || !strings.Contains(stderr, "refusing an unauthenticated request") {
		t.Fatalf("prizmal --relay without a credential exited %d, stderr %q", exitCode, stderr)
	}
	if strings.Contains(stderr, "relaying through ") {
		t.Fatalf("the credential gate ran after the relay started:\n%s", stderr)
	}
	if _, err := os.Stat(filepath.Join(expDir, "claude.called.args")); !os.IsNotExist(err) {
		t.Fatalf("the harness ran despite the missing credential (stat err = %v)", err)
	}
}

func addMockCaptureFiles(t *testing.T, expDir string, capture map[string]string) {
	t.Helper()
	path := filepath.Join(expDir, "mock.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var expectations mockbin.Expectations
	if err := json.Unmarshal(data, &expectations); err != nil {
		t.Fatal(err)
	}
	expectations.CaptureFiles = capture
	data, err = json.Marshal(expectations)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readMockInvocation(t *testing.T, expDir, harness string) (envDump, args string) {
	t.Helper()
	envData, err := os.ReadFile(filepath.Join(expDir, harness+".called.env"))
	if err != nil {
		t.Fatalf("read %s env dump: %v", harness, err)
	}
	argsData, err := os.ReadFile(filepath.Join(expDir, harness+".called.args"))
	if err != nil {
		t.Fatalf("read %s args dump: %v", harness, err)
	}
	return string(envData), strings.TrimSpace(string(argsData))
}

func relayURLFromStderr(t *testing.T, stderr string) string {
	t.Helper()
	match := regexp.MustCompile(`relaying through (http://127\.0\.0\.1:[0-9]+/v1);`).FindStringSubmatch(stderr)
	if len(match) != 2 {
		t.Fatalf("launch did not announce its relay endpoint:\n%s", stderr)
	}
	return match[1]
}

func assertHarnessRelayWiring(
	t *testing.T,
	harness, home, expDir, envDump, args, switchBase string,
	relayMode bool,
) {
	t.Helper()
	wantOpenAIBase := switchBase + "/v1"
	wantKey := stubserver.StubKey

	keyEnv := ""
	switch harness {
	case "claude":
		if got := envDumpValue(envDump, "ANTHROPIC_BASE_URL"); got != switchBase {
			t.Errorf("ANTHROPIC_BASE_URL = %q, want %q", got, switchBase)
		}
		keyEnv = "ANTHROPIC_AUTH_TOKEN"
	case "codex":
		keyEnv = "OPENAI_API_KEY"
		if !strings.Contains(args, `model_providers.prizmal.base_url="`+wantOpenAIBase+`/"`) {
			t.Errorf("Codex provider base URL is not %q in args %q", wantOpenAIBase+"/", args)
		}
	case "pi":
		keyEnv = "PRIZMAL_SWITCH_KEY"
		var config struct {
			Providers map[string]struct {
				BaseURL string `json:"baseUrl"`
				APIKey  string `json:"apiKey"`
			} `json:"providers"`
		}
		readMarkerJSON(t, expDir, "pi.models.json", &config)
		if got := config.Providers["prizmal"].BaseURL; got != strings.TrimSuffix(wantOpenAIBase, "/") {
			t.Errorf("Pi provider baseUrl = %q, want %q", got, strings.TrimSuffix(wantOpenAIBase, "/"))
		}
		if got := config.Providers["prizmal"].APIKey; got != "$PRIZMAL_SWITCH_KEY" {
			t.Errorf("Pi provider apiKey = %q, want the environment reference", got)
		}
	case "opencode":
		var config struct {
			Provider map[string]struct {
				Options struct {
					BaseURL string `json:"baseURL"`
					APIKey  string `json:"apiKey"`
				} `json:"options"`
			} `json:"provider"`
		}
		if err := json.Unmarshal([]byte(envDumpValue(envDump, "OPENCODE_CONFIG_CONTENT")), &config); err != nil {
			t.Fatalf("parse OpenCode inline config: %v", err)
		}
		options := config.Provider["prizmal"].Options
		if got := options.BaseURL; got != strings.TrimSuffix(wantOpenAIBase, "/") {
			t.Errorf("OpenCode baseURL = %q, want %q", got, strings.TrimSuffix(wantOpenAIBase, "/"))
		}
		if relayMode {
			assertPlaceholder(t, options.APIKey)
		} else if options.APIKey != wantKey {
			t.Errorf("OpenCode apiKey = %q, want the switch key", options.APIKey)
		}
	case "kilo":
		var config struct {
			Provider map[string]struct {
				Options struct {
					BaseURL string `json:"baseURL"`
					APIKey  string `json:"apiKey"`
				} `json:"options"`
			} `json:"provider"`
		}
		if err := json.Unmarshal([]byte(envDumpValue(envDump, "KILO_CONFIG_CONTENT")), &config); err != nil {
			t.Fatalf("parse Kilo inline config: %v", err)
		}
		options := config.Provider["prizmal"].Options
		if got := options.BaseURL; got != strings.TrimSuffix(wantOpenAIBase, "/") {
			t.Errorf("Kilo baseURL = %q, want %q", got, strings.TrimSuffix(wantOpenAIBase, "/"))
		}
		assertPlaceholder(t, options.APIKey)
	case "cline":
		keyEnv = "OPENAI_API_KEY"
		var providers map[string]any
		readMarkerJSON(t, expDir, "cline.providers.json", &providers)
		lastUsed, _ := providers["lastUsedProvider"].(string)
		all, _ := providers["providers"].(map[string]any)
		provider, _ := all[lastUsed].(map[string]any)
		settings, _ := provider["settings"].(map[string]any)
		if got, _ := settings["baseUrl"].(string); got != strings.TrimSuffix(wantOpenAIBase, "/") {
			t.Errorf("Cline provider baseUrl = %q, want %q", got, strings.TrimSuffix(wantOpenAIBase, "/"))
		}
	}

	if keyEnv != "" {
		got := envDumpValue(envDump, keyEnv)
		if relayMode {
			assertPlaceholder(t, got)
		} else if got != wantKey {
			t.Errorf("%s = %q, want the switch key", keyEnv, got)
		}
	}
}

func assertPlaceholder(t *testing.T, value string) {
	t.Helper()
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(value) {
		t.Fatalf("harness credential is not a 64-character hex placeholder")
	}
}

func readMarkerJSON(t *testing.T, dir, name string, target any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
}
