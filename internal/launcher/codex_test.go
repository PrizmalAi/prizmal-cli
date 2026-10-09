package launch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/model"
)

// sandboxCodexHome points HOME and USERPROFILE at a temp dir so config writes
// never touch a real user config, and returns the dir. It also puts a codex
// on PATH that prints a bundled catalog, because a launch reads Codex's system
// prompt from the binary and CI runners have none installed.
func sandboxCodexHome(t *testing.T) string {
	t.Helper()
	fakeCodexBundle(t, fakeCodexCatalog)
	d := t.TempDir()
	t.Setenv("HOME", d)
	t.Setenv("USERPROFILE", d)
	return d
}

// TestCodexProfileConfigSourcesKeyFromEnvNotDisk pins the credential channel
// for codex: the generated profile names the environment variable codex must
// read (env_key), and carries no literal api_key. prizmal sets that variable
// on the child at exec, so the key lives for one process instead of forever.
func TestCodexProfileConfigSourcesKeyFromEnvNotDisk(t *testing.T) {
	sandboxCodexHome(t)
	envconfig.SetAPIKey("not-a-real-key-codex-probe")
	t.Cleanup(func() { envconfig.SetAPIKey("") })

	profilePath := filepath.Join(t.TempDir(), "prizmal.config.toml")
	if err := writeCodexNamedProfileConfig(profilePath, codexProfileName, "gpt-test", "", ""); err != nil {
		t.Fatalf("writeCodexNamedProfileConfig: %v", err)
	}

	data, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	text := string(data)
	parsed, err := codexParseConfig(text)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	if got, ok := parsed.String("model_providers", codexProfileName, "env_key"); !ok || got != "OPENAI_API_KEY" {
		t.Fatalf("env_key = %q (ok=%v), want OPENAI_API_KEY", got, ok)
	}
	if got, ok := parsed.String("model_providers", codexProfileName, "api_key"); ok {
		t.Fatalf("api_key = %q is present; the profile must carry no credential at rest", got)
	}
	if strings.Contains(text, "not-a-real-key-codex-probe") {
		t.Fatalf("the provider key appears in the generated profile:\n%s", text)
	}
}

// TestCodexProfileConfigNeverWritesPlaceholderKey guards against regressing to
// the old placeholder credential, and against api_key coming back at all.
func TestCodexProfileConfigNeverWritesPlaceholderKey(t *testing.T) {
	sandboxCodexHome(t)
	envconfig.SetAPIKey("")
	t.Cleanup(func() { envconfig.SetAPIKey("") })

	profilePath := filepath.Join(t.TempDir(), "prizmal.config.toml")
	if err := writeCodexNamedProfileConfig(profilePath, codexProfileName, "gpt-test", "", ""); err != nil {
		t.Fatalf("writeCodexNamedProfileConfig: %v", err)
	}
	data, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	text := string(data)

	for _, bad := range []string{
		"api_key",
		"OPENAI_API_KEY=harness-launch",
		"OPENAI_API_KEY=ollama",
	} {
		if strings.Contains(text, bad) {
			t.Fatalf("found %q in config:\n%s", bad, text)
		}
	}
}

// codexContextWindow pulls context_window off a codex catalog entry.
func codexContextWindow(t *testing.T, entry map[string]any) int {
	t.Helper()
	got, ok := entry["context_window"].(int)
	if !ok {
		t.Fatalf("codex entry lacks an int context_window: %v", entry["context_window"])
	}
	return got
}

// The declared window is the documented fallback for every model, because
// GET /v1/models carries no context length: the Switch sends id,
// input_modalities, tier and description, and no writer in the tree ever fills
// LaunchModel.ContextLength or LaunchModel.Details. Pinning it here means a
// writer added later without a wire from the catalog fails here instead of
// leaving a session to compact at 90% of a window nobody chose.
func TestCodexEntryDeclaresTheDocumentedContextWindow(t *testing.T) {
	entry := buildCodexModelEntry(LaunchModel{
		Name:         "smart",
		Capabilities: []model.Capability{model.CapabilityVision, model.CapabilityCompletion},
	})

	if got := codexContextWindow(t, entry); got != 1_000_000 {
		t.Fatalf("context_window = %d, want 1000000: every model the Switch serves has a 1M window", got)
	}
}

// $HARNESS_CONTEXT_LENGTH is the only way an operator can state a real window,
// so it has to win over the fallback for a model whose window the catalog
// cannot describe.
func TestCodexEntryHonoursTheOperatorStatedContextWindow(t *testing.T) {
	t.Setenv("HARNESS_CONTEXT_LENGTH", "1000000")

	entry := buildCodexModelEntry(LaunchModel{Name: "smart"})

	if got := codexContextWindow(t, entry); got != 1_000_000 {
		t.Fatalf("context_window = %d, want the operator's 1000000", got)
	}
}

// An unset or unparseable $HARNESS_CONTEXT_LENGTH must fall back rather than
// declare a zero window, which codex reads as no window at all.
func TestCodexEntryIgnoresAnUnusableContextWindowOverride(t *testing.T) {
	t.Setenv("HARNESS_CONTEXT_LENGTH", "not-a-number")

	entry := buildCodexModelEntry(LaunchModel{Name: "smart"})

	if got := codexContextWindow(t, entry); got != codexFallbackContextWindow {
		t.Fatalf("context_window = %d, want the fallback %d for an unparseable override", got, codexFallbackContextWindow)
	}
}

// Tool output is truncated in tokens, matching what codex's own catalog
// declares for every model it ships. Codex reads a bytes-mode limit as bytes,
// so a mode of "bytes" cut every tool's output at 10000 bytes — a quarter of
// the budget the vendor default gives it — on a branch that could never select
// anything else.
func TestCodexEntryTruncatesToolOutputInTokens(t *testing.T) {
	policy, ok := buildCodexModelEntry(LaunchModel{Name: "smart"})["truncation_policy"].(map[string]any)
	if !ok {
		t.Fatal("codex entry lacks a truncation_policy map")
	}

	if mode := policy["mode"]; mode != "tokens" {
		t.Fatalf("truncation mode = %v, want tokens", mode)
	}
	if limit := policy["limit"]; limit != 10000 {
		t.Fatalf("truncation limit = %v, want 10000", limit)
	}
}

// base_instructions stays on the entry even though it is empty, which is not
// the same as leaving it out: codex accepts a present empty template and runs
// the session with it, while the catalog decoder rejects a model that carries
// neither base_instructions nor model_messages.instructions_template. The key
// cannot be dropped until the entry carries a real template.
func TestCodexEntryKeepsTheExplicitBaseInstructionsKey(t *testing.T) {
	entry := buildCodexModelEntry(LaunchModel{Name: "smart"})

	instructions, ok := entry["base_instructions"]
	if !ok {
		t.Fatal("codex entry omits base_instructions; codex rejects a model with no instruction template at all")
	}
	if instructions != "" {
		t.Fatalf("base_instructions = %v, want the empty template the entry has always written", instructions)
	}
}

// fakeCodexBundle puts a codex on PATH whose `debug models --bundled` prints
// catalog, and returns nothing else: the launch reads Codex's own prompt from
// the installed binary, so tests stand in for the binary.
func fakeCodexBundle(t *testing.T, catalog string) {
	t.Helper()
	dir := t.TempDir()
	data := filepath.Join(dir, "bundle.json")
	if err := os.WriteFile(data, []byte(catalog), 0o644); err != nil {
		t.Fatal(err)
	}
	name, script := "codex", "#!/bin/sh\nif [ \"$1 $2 $3\" = \"debug models --bundled\" ]; then cat '"+data+"'; exit 0; fi\nexit 1\n"
	if runtime.GOOS == "windows" {
		name, script = "codex.bat", "@echo off\r\ntype \""+data+"\"\r\n"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

const fakeCodexCatalog = `{"models":[
 {"slug":"hidden","visibility":"hide","priority":0,"base_instructions":"hidden prompt","model_messages":{"instructions_template":"hidden prompt"}},
 {"slug":"second","visibility":"list","priority":2,"base_instructions":"second prompt","model_messages":{"instructions_template":"second prompt"}},
 {"slug":"first","visibility":"list","priority":1,"base_instructions":"first prompt","model_messages":{"instructions_template":"first prompt","permissions":{"a":"b"}}}
]}`

// TestCodexCatalogCarriesCodexsOwnSystemPrompt pins the fix for a launch that
// sent Codex's request with empty instructions: the catalog entry holds the
// prompt of the installed Codex's default model, whole.
func TestCodexCatalogCarriesCodexsOwnSystemPrompt(t *testing.T) {
	fakeCodexBundle(t, fakeCodexCatalog)
	path := filepath.Join(t.TempDir(), "catalog.json")
	if err := writeCodexModelCatalog(path, []LaunchModel{{Name: "prizmal-flash"}}); err != nil {
		t.Fatalf("writeCodexModelCatalog: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Models []struct {
			Slug          string `json:"slug"`
			BaseInst      string `json:"base_instructions"`
			ModelMessages struct {
				Template    string         `json:"instructions_template"`
				Permissions map[string]any `json:"permissions"`
			} `json:"model_messages"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) != 1 {
		t.Fatalf("got %d catalog entries, want 1", len(catalog.Models))
	}
	m := catalog.Models[0]
	if m.Slug != "prizmal-flash" {
		t.Errorf("slug = %q", m.Slug)
	}
	if m.BaseInst != "first prompt" || m.ModelMessages.Template != "first prompt" {
		t.Errorf("prompt = %q / %q, want the lowest-priority listed model's prompt", m.BaseInst, m.ModelMessages.Template)
	}
	if m.ModelMessages.Permissions["a"] != "b" {
		t.Errorf("model_messages was not copied whole: %v", m.ModelMessages.Permissions)
	}
}

// TestCodexCatalogFailsWhenThePromptIsUnreadable keeps a launch from falling
// back to an empty prompt, which is the defect this guards against.
func TestCodexCatalogFailsWhenThePromptIsUnreadable(t *testing.T) {
	for name, catalog := range map[string]string{
		"not json":      `nope`,
		"no entries":    `{"models":[]}`,
		"empty prompts": `{"models":[{"slug":"x","visibility":"list","priority":1,"base_instructions":""}]}`,
		"only hidden":   `{"models":[{"slug":"x","visibility":"hide","priority":1,"base_instructions":"p"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			fakeCodexBundle(t, catalog)
			path := filepath.Join(t.TempDir(), "catalog.json")
			if err := writeCodexModelCatalog(path, []LaunchModel{{Name: "prizmal-flash"}}); err == nil {
				t.Fatal("want an error, got none")
			}
			if _, err := os.Stat(path); err == nil {
				t.Fatal("a catalog was written without a prompt")
			}
		})
	}
}

// TestCodexCatalogListsTheTenantCatalogWithEffortLevels pins what Codex's
// /model picker shows: every catalog model with its description, the launched
// model first, and the effort levels that turn on the effort picker.
func TestCodexCatalogListsTheTenantCatalogWithEffortLevels(t *testing.T) {
	fakeCodexBundle(t, fakeCodexCatalog)
	catalog := []LaunchModel{
		{Name: "prizmal-core", Description: "Core"},
		{Name: "prizmal-flash", Description: "Fast"},
		{Name: "prizmal-frontier", Description: "Best"},
		{Name: "claude-tier-opus", Description: "alias row"},
		{Name: "folded", FoldedInto: "claude-tier-opus"},
	}
	path := filepath.Join(t.TempDir(), "catalog.json")
	if err := writeCodexModelCatalog(path, codexCatalogModels("prizmal-flash", catalog)); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	var got struct {
		Models []struct {
			Slug        string `json:"slug"`
			Description string `json:"description"`
			Priority    int    `json:"priority"`
			Default     string `json:"default_reasoning_level"`
			Levels      []struct {
				Effort string `json:"effort"`
			} `json:"supported_reasoning_levels"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	var slugs []string
	for i, m := range got.Models {
		slugs = append(slugs, m.Slug)
		if m.Priority != i {
			t.Errorf("%s priority = %d, want %d", m.Slug, m.Priority, i)
		}
		if len(m.Levels) < 2 || m.Default == "" {
			t.Errorf("%s has no effort levels: %+v", m.Slug, m)
		}
	}
	if strings.Join(slugs, ",") != "prizmal-flash,prizmal-core,prizmal-frontier,folded" {
		t.Fatalf("slugs = %v, want the launched model first, then the rest, without Claude tier alias rows", slugs)
	}
	if got.Models[0].Description != "Fast" {
		t.Errorf("description = %q, want the catalog's text", got.Models[0].Description)
	}
}

// TestCodexCatalogDeclaresTheWindowTheSwitchPublishes pins the 1M window: an
// id the Switch decorates with [1m] becomes a bare slug whose entry declares
// 1000000, and an undecorated id keeps the fallback window.
func TestCodexCatalogDeclaresTheWindowTheSwitchPublishes(t *testing.T) {
	fakeCodexBundle(t, fakeCodexCatalog)
	t.Setenv("HARNESS_CONTEXT_LENGTH", "")
	body := `{"data":[{"id":"prizmal-flash[1m]"},{"id":"prizmal-core"}]}`
	catalog, err := parseSwitchCatalog([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	models := LaunchModels("prizmal-flash", catalog, true)
	path := filepath.Join(t.TempDir(), "catalog.json")
	if err := writeCodexModelCatalog(path, codexCatalogModels("prizmal-flash", models)); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	var got struct {
		Models []struct {
			Slug   string `json:"slug"`
			Window int    `json:"context_window"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"prizmal-flash": 1_000_000, "prizmal-core": codexFallbackContextWindow}
	if len(got.Models) != 2 {
		t.Fatalf("entries = %+v", got.Models)
	}
	for _, m := range got.Models {
		if want[m.Slug] != m.Window {
			t.Errorf("%s context_window = %d, want %d", m.Slug, m.Window, want[m.Slug])
		}
	}
}

// TestCodexDeviceModeOverridesUseCommandAuth pins the device-mode credential
// channel: the provider runs `prizmal auth token` for its bearer token, and
// declares no env_key, which Codex rejects next to a command.
func TestCodexDeviceModeOverridesUseCommandAuth(t *testing.T) {
	withDeviceMode(t)
	overrides := strings.Join(codexManagedConfigOverrides(""), "\n")

	for _, want := range []string{
		"model_providers.prizmal.auth.command=",
		`model_providers.prizmal.auth.args=["auth", "token"]`,
		"model_providers.prizmal.auth.refresh_interval_ms=240000",
	} {
		if !strings.Contains(overrides, want) {
			t.Errorf("overrides lack %q:\n%s", want, overrides)
		}
	}
	if strings.Contains(overrides, "env_key") {
		t.Errorf("device-mode overrides name env_key:\n%s", overrides)
	}
}

func TestCodexKeyModeOverridesKeepEnvKeyAndNoAuth(t *testing.T) {
	envconfig.SetDeviceMode(false)
	overrides := strings.Join(codexManagedConfigOverrides(""), "\n")
	if !strings.Contains(overrides, `env_key="OPENAI_API_KEY"`) {
		t.Errorf("key-mode overrides lack env_key:\n%s", overrides)
	}
	if strings.Contains(overrides, ".auth.") {
		t.Errorf("key-mode overrides carry command auth:\n%s", overrides)
	}
}

// TestCodexDeviceModeProfileHasCommandAuthAndNoEnvKey: the profile file and
// the -c overrides merge key by key, so a profile that kept env_key would
// combine with the command and fail Codex's config validation.
func TestCodexDeviceModeProfileHasCommandAuthAndNoEnvKey(t *testing.T) {
	sandboxCodexHome(t)
	withDeviceMode(t)
	profilePath := filepath.Join(t.TempDir(), "prizmal.config.toml")
	if err := writeCodexNamedProfileConfig(profilePath, codexProfileName, "gpt-test", "", ""); err != nil {
		t.Fatalf("writeCodexNamedProfileConfig: %v", err)
	}
	data, _ := os.ReadFile(profilePath)
	parsed, err := codexParseConfig(string(data))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, ok := parsed.String("model_providers", codexProfileName, "env_key"); ok {
		t.Errorf("profile keeps env_key in device mode:\n%s", data)
	}
	if got, ok := parsed.String("model_providers", codexProfileName, "auth", "command"); !ok || !filepath.IsAbs(got) {
		t.Errorf("auth.command = %q (ok=%v), want an absolute executable path:\n%s", got, ok, data)
	}
}

// TestCodexDeviceModeChildEnvCarriesNoCredential: the switch key and the
// device token stay out of Codex's environment, and an OPENAI_API_KEY the
// operator exported for OpenAI itself does not reach the Switch.
func TestCodexDeviceModeChildEnvCarriesNoCredential(t *testing.T) {
	withDeviceMode(t)
	envconfig.SetAPIKey("switch-key-probe")
	t.Cleanup(func() { envconfig.SetAPIKey("") })

	envconfig.SetBaseURL("https://switch.example")
	t.Cleanup(func() { envconfig.SetBaseURL("") })
	// The operator's own OPENAI_API_KEY is what must not reach the Switch.
	t.Setenv("OPENAI_API_KEY", "operator-openai-key")

	env := codexChildEnv()
	joined := strings.Join(env, "\n")
	for _, secret := range []string{"switch-key-probe", "pz-d-dt-test-token", "operator-openai-key"} {
		if strings.Contains(joined, secret) {
			t.Errorf("child env leaks %q:\n%s", secret, joined)
		}
	}
	if !strings.Contains(joined, envconfig.EnvVar+"=https://switch.example") {
		t.Errorf("child env does not pin the Switch URL for the helper:\n%s", joined)
	}
}

func TestCodexKeyModeChildEnvCarriesSwitchKey(t *testing.T) {
	envconfig.SetDeviceMode(false)
	envconfig.SetAPIKey("switch-key-probe")
	t.Cleanup(func() { envconfig.SetAPIKey("") })
	env := codexChildEnv()
	if !strings.Contains(strings.Join(env, "\n"), "OPENAI_API_KEY=switch-key-probe") {
		t.Errorf("key-mode env lacks the switch key: %v", env)
	}
}
