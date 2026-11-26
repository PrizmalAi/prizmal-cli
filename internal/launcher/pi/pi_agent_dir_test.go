package pi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/launchtest"
)

// piSeedUserAgentDir writes the files a user's own pi keeps and returns their
// paths. The settings differ from anything a launch would write, so a launch
// that changed them shows it.
func piSeedUserAgentDir(t *testing.T, home string) (modelsPath, settingsPath string) {
	t.Helper()
	agentDir := filepath.Join(home, ".pi", "agent")
	for _, dir := range []string{"extensions", "skills"} {
		if err := os.MkdirAll(filepath.Join(agentDir, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	modelsPath = filepath.Join(agentDir, "models.json")
	settingsPath = filepath.Join(agentDir, "settings.json")
	internaltest.SandboxedSeed(t, home, filepath.Join(".pi", "agent", "models.json"),
		`{"providers":{"openrouter":{"baseUrl":"https://openrouter.example.invalid/v1",`+
			`"api":"openai-completions","apiKey":"$OPENROUTER_KEY","models":[{"id":"zeta"}]}}}`)
	internaltest.SandboxedSeed(t, home, filepath.Join(".pi", "agent", "settings.json"),
		`{"theme":"dark","defaultProvider":"openrouter","defaultModel":"zeta"}`)
	internaltest.SandboxedSeed(t, home, filepath.Join(".pi", "agent", "auth.json"), `{"openrouter":{"type":"api_key","key":"their-own-fake-key"}}`)
	if err := os.WriteFile(filepath.Join(agentDir, "extensions", "probe.ts"), []byte("export default function () {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return modelsPath, settingsPath
}

// TestPiLaunchAgentDirLinksWhatTheUserKeeps pins the overlay: the launch's
// agent directory exposes the user's own entries — their extensions, skills,
// their sessions — through the paths Pi already reads, so a launch changes none
// of them.
func TestPiLaunchAgentDirLinksWhatTheUserKeeps(t *testing.T) {
	launchtest.WithDeviceMode(t)
	home := internaltest.SandboxedHome(t)
	piSeedUserAgentDir(t, home)

	l, err := piLaunchFor("probe-model", []launch.LaunchModel{{Name: "probe-model"}})
	if err != nil {
		t.Fatalf("piLaunchFor: %v", err)
	}
	t.Cleanup(l.clean)

	userAgentDir := filepath.Join(home, ".pi", "agent")
	for _, name := range []string{"extensions", "skills", "auth.json"} {
		link := filepath.Join(l.agentDir, name)
		target, err := os.Readlink(link)
		if err != nil {
			t.Fatalf("%s is not a link: %v", name, err)
		}
		if want := filepath.Join(userAgentDir, name); target != want {
			t.Errorf("%s links to %q, want %q", name, target, want)
		}
	}
	for _, name := range []string{"sessions", "trust.json", "models-store.json"} {
		if _, err := os.Lstat(filepath.Join(l.agentDir, name)); err != nil {
			t.Errorf("%s missing from the launch's agent directory: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(userAgentDir, "sessions")); err != nil {
		t.Errorf("the launch did not create the user's sessions directory: %v", err)
	}
}

// TestPiLaunchAgentDirCopiesModelsWithoutThePrizmalProvider pins the copy of
// models.json: every provider the user configured stays, with its fields, and
// the prizmal entry goes, because the launch registers that provider itself and
// Pi validates the file's rows while it does.
func TestPiLaunchAgentDirCopiesModelsWithoutThePrizmalProvider(t *testing.T) {
	launchtest.WithDeviceMode(t)
	home := internaltest.SandboxedHome(t)
	modelsPath, _ := piSeedUserAgentDir(t, home)
	seed := `{"providers":{"prizmal":{"models":[{"id":"handwritten","input":["text"]}]},` +
		`"openrouter":{"baseUrl":"https://openrouter.example.invalid/v1","api":"openai-completions",` +
		`"apiKey":"$OPENROUTER_KEY","models":[{"id":"zeta"}]}}}`
	internaltest.SandboxedSeed(t, home, filepath.Join(".pi", "agent", "models.json"), seed)

	l, err := piLaunchFor("probe-model", nil)
	if err != nil {
		t.Fatalf("piLaunchFor: %v", err)
	}
	t.Cleanup(l.clean)

	link := filepath.Join(l.agentDir, piModelsName)
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("models.json is not a plain file: info = %v, err = %v", info, err)
	}
	config := parseJSON(t, readFile(t, link))
	providers, _ := config["providers"].(map[string]any)
	if _, ok := providers[piProviderID]; ok {
		t.Error("the launch's models.json still holds the prizmal provider")
	}
	openrouter, _ := providers["openrouter"].(map[string]any)
	if got, _ := openrouter["baseUrl"].(string); got != "https://openrouter.example.invalid/v1" {
		t.Errorf("openrouter baseUrl = %q, want it kept", got)
	}
	if after, err := os.ReadFile(modelsPath); err != nil || string(after) != seed {
		t.Fatalf("the user's models.json changed: %s (%v)", after, err)
	}
}

// TestPiLaunchAgentDirCopiesSettings pins the file Pi rewrites: a /model switch
// rewrites settings.json, so the launch's copy is a copy and the user's file
// does not move when Pi writes.
func TestPiLaunchAgentDirCopiesSettings(t *testing.T) {
	launchtest.WithDeviceMode(t)
	home := internaltest.SandboxedHome(t)
	_, settingsPath := piSeedUserAgentDir(t, home)
	before, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}

	l, err := piLaunchFor("probe-model", []launch.LaunchModel{{Name: "probe-model"}})
	if err != nil {
		t.Fatalf("piLaunchFor: %v", err)
	}
	t.Cleanup(l.clean)

	link := filepath.Join(l.agentDir, piSettingsName)
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("settings.json is not a plain file: info = %v, err = %v", info, err)
	}
	copied := parseJSON(t, readFile(t, link))
	if got, _ := copied["theme"].(string); got != "dark" {
		t.Fatalf("the launch's settings lost the user's own keys: %v", copied)
	}
	if got, _ := copied["defaultProvider"].(string); got != piProviderID {
		t.Errorf("defaultProvider = %q, want the launch's provider", got)
	}
	if got, _ := copied["defaultModel"].(string); got != "probe-model" {
		t.Errorf("defaultModel = %q, want the launched model", got)
	}

	// What Pi does on a /model switch: rewrite the file.
	if err := os.WriteFile(link, []byte(`{"theme":"dark","defaultProvider":"prizmal","defaultModel":"switched"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("a write Pi made reached the user's settings.json:\ngot:  %s\nwant: %s", after, before)
	}
}

// TestPiLaunchLeavesTheUserAgentDirByteIdentical pins the launch's central
// promise at the package level: building everything a launch hands Pi reads the
// user's models.json and settings.json without writing a byte to either.
func TestPiLaunchLeavesTheUserAgentDirByteIdentical(t *testing.T) {
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
			home := internaltest.SandboxedHome(t)
			modelsPath, settingsPath := piSeedUserAgentDir(t, home)
			internaltest.WithAPIKey(t, internaltest.FakeKeyAtRest)
			before := map[string][]byte{}
			for _, path := range []string{modelsPath, settingsPath} {
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				before[path] = data
			}

			l, err := piLaunchFor("probe-model", launch.LaunchModels("probe-model", piCatalog, true))
			if err != nil {
				t.Fatalf("piLaunchFor: %v", err)
			}
			t.Cleanup(l.clean)

			for _, path := range []string{modelsPath, settingsPath} {
				after, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if string(after) != string(before[path]) {
					t.Fatalf("%s changed\nbefore: %s\nafter:  %s", path, before[path], after)
				}
			}
		})
	}
}

// TestPiLaunchCarriesTheUsersModelRows pins that the extension's provider keeps
// the operator's own entries under the prizmal provider in Pi's picker, beside
// the catalog: Pi replaces a provider's model list with the extension's, and a
// launch that dropped them would hide models the user configured.
func TestPiLaunchCarriesTheUsersModelRows(t *testing.T) {
	home := internaltest.SandboxedHome(t)
	internaltest.SandboxedSeed(t, home, filepath.Join(".pi", "agent", "models.json"), `{"providers":{"prizmal":{`+
		`"baseUrl":"https://switch.example.invalid/v1","api":"openai-completions","apiKey":"$PRIZMAL_SWITCH_KEY",`+
		`"models":[`+
		`{"id":"operator-model","input":["text","image"],"contextWindow":65536,"reasoning":true},`+
		`{"id":"frontier","_launch":true},`+
		`{"note":"an entry pi does not know"}]}}}`)

	rows := piUserProviderModels(piUserAgentDir())
	if len(rows) != 1 {
		t.Fatalf("user rows = %v, want the operator's one entry", rows)
	}

	data := piDeviceConfigJSON("frontier", []launch.LaunchModel{{Name: "frontier"}, {Name: "flash"}}, true, rows)
	var config piDeviceConfig
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("unmarshal the sidecar: %v", err)
	}
	var ids []string
	for _, row := range config.Models {
		ids = append(ids, row.ID)
	}
	if want := []string{"operator-model", "frontier", "flash"}; !slices.Equal(ids, want) {
		t.Fatalf("rows = %v, want %v", ids, want)
	}
	operator := config.Models[0]
	if operator.ContextWindow != 65536 || !operator.Vision || !operator.Reasoning {
		t.Errorf("operator row lost its fields: %+v", operator)
	}
}

// TestPiStaleEntryWarning pins the repair path for an entry an older build
// wrote: a launch says the entry breaks a bare pi and names the command that
// removes it, and a clean entry or no file says nothing.
func TestPiStaleEntryWarning(t *testing.T) {
	for _, tc := range []struct {
		name     string
		models   string
		wantsTip bool
	}{
		{
			name: "an entry an older build wrote",
			models: `{"providers":{"prizmal":{"baseUrl":"https://switch.example/v1","api":"openai-completions",` +
				`"apiKey":"$PRIZMAL_SWITCH_KEY","headers":{"x-prizmal-client-version":"$PRIZMAL_CLIENT_VERSION"},` +
				`"models":[{"id":"frontier","_launch":true}]}}}`,
			wantsTip: true,
		},
		{
			name: "a persisted entry",
			models: `{"providers":{"prizmal":{"baseUrl":"https://switch.example/v1","api":"openai-completions",` +
				`"apiKey":"$PRIZMAL_SWITCH_KEY","models":[{"id":"frontier","_launch":true}]}}}`,
		},
		{name: "no file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := internaltest.SandboxedHome(t)
			if tc.models != "" {
				internaltest.SandboxedSeed(t, home, filepath.Join(".pi", "agent", "models.json"), tc.models)
			}
			var out stringsBuilder
			warnPiStaleLaunchEntry(&out)
			got := out.String()
			if tc.wantsTip {
				if !strings.Contains(got, "prizmal --restore pi") {
					t.Fatalf("warning = %q, want the restore command", got)
				}
				return
			}
			if got != "" {
				t.Fatalf("warning = %q, want none", got)
			}
		})
	}
}

// stringsBuilder is the writer the warning tests capture into.
type stringsBuilder struct{ b []byte }

func (s *stringsBuilder) Write(p []byte) (int, error) {
	s.b = append(s.b, p...)
	return len(p), nil
}

func (s *stringsBuilder) String() string { return string(s.b) }
