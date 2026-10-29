package kilo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

func inlineConfig(t *testing.T, model string, catalog []launch.LaunchModel) map[string]any {
	t.Helper()
	raw, err := buildInlineConfig(model, catalog)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		t.Fatal(err)
	}
	return config
}

// TestInlineConfigDeclaresTheProvider pins the provider block Kilo reads from
// KILO_CONFIG_CONTENT: the OpenAI-compatible package, the Switch endpoint, and
// the launch's model first.
func TestInlineConfigDeclaresTheProvider(t *testing.T) {
	config := inlineConfig(t, "smart", []launch.LaunchModel{{Name: "fast"}, {Name: "smart"}})

	if config["model"] != "prizmal/smart" {
		t.Fatalf("model = %v, want prizmal/smart", config["model"])
	}
	provider := config["provider"].(map[string]any)["prizmal"].(map[string]any)
	if provider["npm"] != "@ai-sdk/openai-compatible" {
		t.Fatalf("npm = %v", provider["npm"])
	}
	options := provider["options"].(map[string]any)
	if options["baseURL"] != envconfig.OpenAIBaseURL(false) {
		t.Fatalf("baseURL = %v", options["baseURL"])
	}
	models := provider["models"].(map[string]any)
	for _, name := range []string{"smart", "fast"} {
		entry, _ := models[name].(map[string]any)
		limit, _ := entry["limit"].(map[string]any)
		if limit["context"] != float64(1_000_000) {
			t.Fatalf("model %s limit = %v, want a 1000000 context", name, entry["limit"])
		}
	}
}

// TestInlineConfigRegistersAModelOutsideTheCatalog pins that a model named on
// the command line but absent from the catalog still gets an entry.
func TestInlineConfigRegistersAModelOutsideTheCatalog(t *testing.T) {
	config := inlineConfig(t, "custom", nil)
	models := config["provider"].(map[string]any)["prizmal"].(map[string]any)["models"].(map[string]any)
	if _, ok := models["custom"]; !ok {
		t.Fatalf("models = %v, want an entry for custom", models)
	}
}

// TestInlineConfigDisablesHostedWebsearch pins that Kilo's built-in websearch,
// which posts outside the Switch, is off through both config blocks.
func TestInlineConfigDisablesHostedWebsearch(t *testing.T) {
	config := inlineConfig(t, "smart", nil)
	if config["tools"].(map[string]any)["websearch"] != false {
		t.Fatalf("tools = %v", config["tools"])
	}
	if config["permission"].(map[string]any)["websearch"] != "deny" {
		t.Fatalf("permission = %v", config["permission"])
	}
}

// TestInlineConfigRequiresAModel pins the error for a launch with no model.
func TestInlineConfigRequiresAModel(t *testing.T) {
	if _, err := buildInlineConfig("", nil); err == nil {
		t.Fatal("expected an error for an empty model")
	}
}

// TestInlineConfigCarriesTheRelayPlaceholder pins that a relay launch puts the
// per-launch placeholder in the config and never the switch key.
func TestInlineConfigCarriesTheRelayPlaceholder(t *testing.T) {
	envconfig.SetRelay("http://127.0.0.1:4242", "placeholder-1")
	t.Cleanup(envconfig.ClearRelay)

	raw, err := buildInlineConfig("smart", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, `"apiKey":"placeholder-1"`) || !strings.Contains(raw, ":4242/v1") {
		t.Fatalf("config = %s, want the relay URL and placeholder", raw)
	}
}

func TestRunsHeadlessOnTheRunSubcommand(t *testing.T) {
	k := &Kilo{}
	for args, want := range map[string]bool{
		"run hello":                true,
		"--model prizmal/x run hi": true,
		"":                         false,
		"--model run":              false,
		"-- run":                   false,
	} {
		if got := k.RunsHeadless(strings.Fields(args)); got != want {
			t.Errorf("RunsHeadless(%q) = %v, want %v", args, got, want)
		}
	}
}

func TestInstallerUsesNpm(t *testing.T) {
	name, args, err := KiloInstallerCommand("linux")
	if err != nil || name != "npm" || args[len(args)-1] != "@kilocode/cli@latest" {
		t.Fatalf("install command = %s %v, %v", name, args, err)
	}
}

// TestLaunchArgsPutTheModelAfterRun pins where --model goes: before a TUI
// launch's arguments, and after the run subcommand.
func TestLaunchArgsPutTheModelAfterRun(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", "--model prizmal/m"},
		{"run hi", "run --model prizmal/m hi"},
		{"--agent x run hi", "--agent x run --model prizmal/m hi"},
		{"--agent run", "--model prizmal/m --agent run"},
	} {
		got := strings.Join(launchArgs("m", strings.Fields(tc.in)), " ")
		if got != tc.want {
			t.Errorf("launchArgs(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestEnvPointsKiloAtTheSearchTool pins that the launch hands Kilo the
// directory of the generated search tool and the variables the tool reads.
func TestEnvPointsKiloAtTheSearchTool(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	env := strings.Join((&Kilo{}).envVars("smart", "{}"), "\n")
	for _, want := range []string{"KILO_CONFIG_CONTENT={}", "KILO_CONFIG_DIR=", "PRIZMAL_OPENCODE_SEARCH_MODEL=smart"} {
		if !strings.Contains(env, want) {
			t.Errorf("env lacks %q:\n%s", want, env)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".cache", "prizmal", "kilo", "tool", "prizmal_search.ts")); err != nil {
		t.Errorf("search tool not written: %v", err)
	}
}

// TestInlineConfigOffersOnlyThePrizmalProviderAndImages pins the two fields the
// e2e baselines depend on: no network-fetched gateway models, and image input.
func TestInlineConfigOffersOnlyThePrizmalProviderAndImages(t *testing.T) {
	config := inlineConfig(t, "smart", nil)

	enabled, _ := config["enabled_providers"].([]any)
	if len(enabled) != 1 || enabled[0] != "prizmal" {
		t.Fatalf("enabled_providers = %v, want [prizmal]", config["enabled_providers"])
	}
	models := config["provider"].(map[string]any)["prizmal"].(map[string]any)["models"].(map[string]any)
	input := models["smart"].(map[string]any)["modalities"].(map[string]any)["input"]
	if got := strings.Join(toStrings(input), ","); got != "text,image" {
		t.Fatalf("modalities.input = %s, want text,image", got)
	}
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}
