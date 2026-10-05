package launch

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
)

// resetAPIKey clears the process-wide key override and env var so each test
// starts with an empty provider key.
func resetAPIKey(t *testing.T) {
	t.Helper()
	envconfig.SetAPIKey("")
	t.Setenv(envconfig.KeyEnvVar, "")
}

// envValue returns the value of the var named after "prefix=" in env, or "".
func envValue(env []string, prefix string) string {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, prefix); ok {
			return v
		}
	}
	return ""
}

// envNames returns every variable name in an environment.
func envNames(env []string) []string {
	names := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		names = append(names, name)
	}
	return names
}

func TestClaudeEnvVarsCarryAPIKey(t *testing.T) {
	resetAPIKey(t)
	envconfig.SetAPIKey("sk-test-key")
	t.Cleanup(func() { envconfig.SetAPIKey("") })

	env := (&Claude{}).envVars()

	if got := envValue(env, "ANTHROPIC_AUTH_TOKEN="); got != "sk-test-key" {
		t.Fatalf("ANTHROPIC_AUTH_TOKEN = %q, want sk-test-key", got)
	}
	// The console-key variable is emptied rather than set: with both set,
	// Claude Code warns that auth may not work, and an inherited shell
	// export would otherwise ride along as an x-api-key header.
	if got := envValue(env, "ANTHROPIC_API_KEY="); got != "" {
		t.Fatalf("ANTHROPIC_API_KEY = %q, want empty", got)
	}
	if !strings.Contains(strings.Join(env, "\n"), "ANTHROPIC_API_KEY=") {
		t.Fatalf("ANTHROPIC_API_KEY is absent from the child env, so a shell export would be inherited: %v", env)
	}
}

func TestClaudeEnvVarsEmptyAPIKeyIsVerbatimEmpty(t *testing.T) {
	resetAPIKey(t)

	env := (&Claude{}).envVars()

	if got := envValue(env, "ANTHROPIC_API_KEY="); got != "" {
		t.Fatalf("ANTHROPIC_API_KEY = %q, want empty", got)
	}
	if got := envValue(env, "ANTHROPIC_AUTH_TOKEN="); got != "" {
		t.Fatalf("ANTHROPIC_AUTH_TOKEN = %q, want empty", got)
	}

	// The old placeholder must never reappear.
	for _, bad := range []string{"harness-launch", "ollama", "ollama-local"} {
		if strings.Contains(strings.Join(env, "\n"), bad) {
			t.Fatalf("placeholder %q leaked into env: %v", bad, env)
		}
	}
}

// TestClaudeEnvVarsEnableToolSearch pins the tool-search-on launch. Claude Code runs tool
// search (tool definitions withheld from the system prompt, loaded on demand
// through tool_reference blocks) by default only against first-party Anthropic
// hosts. Pointing ANTHROPIC_BASE_URL at the Switch turns it off, so every
// launch pays for the full tool list in every request. The Switch already
// parses and forwards tool_reference blocks, so the override is safe and
// belongs on every launch.
func TestClaudeEnvVarsEnableToolSearch(t *testing.T) {
	resetAPIKey(t)

	env := (&Claude{}).envVars()

	if got := envValue(env, "ENABLE_TOOL_SEARCH="); got != "true" {
		t.Fatalf("ENABLE_TOOL_SEARCH = %q, want true", got)
	}
}

// Every launch opts out of claude.ai cloud connectors.
//
// The variable is the explicit opt-out. Claude Code reads it before it checks
// whether the launch's auth source takes precedence over a stored claude.ai
// login, so a launch that sets it never reaches the branch that prints the
// startup warning about the Switch credential. The variable sits in envVars,
// which every launch sets, so the opt-out also holds on a launch that names no
// model and gets no inline settings JSON at all.
//
// It gates the auto-fetch only. A server named through --mcp-config, the
// settings mcpServers block, .mcp.json or the SDK keeps the normal MCP trust
// flow, so this is not an MCP switch.
func TestClaudeEnvVarsDisableClaudeAiConnectors(t *testing.T) {
	resetAPIKey(t)

	env := (&Claude{}).envVars()

	if got := envValue(env, "ENABLE_CLAUDEAI_MCP_SERVERS="); got != "false" {
		t.Fatalf("ENABLE_CLAUDEAI_MCP_SERVERS = %q, want false", got)
	}
}

// Gateway discovery is off, and the variable is not even present. The inline
// settings JSON already carries the active model and every picker row, so
// discovery would only add a second list that can disagree with it, at the cost
// of a request and through a claude- filter the settings path does not need.
func TestClaudeEnvVarsDoNotEnableGatewayModelDiscovery(t *testing.T) {
	resetAPIKey(t)

	env := strings.Join((&Claude{}).envVars(), "\n")
	if strings.Contains(env, "CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY") {
		t.Fatalf("gateway discovery variable is present; the settings JSON defines every row:\n%s", env)
	}
}

// envVars sets no model variable. claudeChildEnv adds the two a launch sets,
// once it knows the pinned model and the subagent model.
func TestClaudeEnvVarsSetNoModelVariables(t *testing.T) {
	resetAPIKey(t)

	env := strings.Join((&Claude{}).envVars(), "\n")

	for _, name := range []string{
		"ANTHROPIC_DEFAULT_OPUS_MODEL",
		"ANTHROPIC_DEFAULT_SONNET_MODEL",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL",
		"ANTHROPIC_DEFAULT_FABLE_MODEL",
		"CLAUDE_CODE_SUBAGENT_MODEL",
		"ANTHROPIC_MODEL",
	} {
		if strings.Contains(env, name+"=") {
			t.Errorf("%s is set by envVars; the inline settings JSON carries the model", name)
		}
	}
}

// TestClaudeChildEnvDropsInheritedModelVars is the acceptance criterion that no
// inherited model env var reaches the child. A value exported in the operator's
// shell for Anthropic itself must not ride along and re-route a launch the
// operator aimed at the Switch.
//
// ANTHROPIC_AUTH_TOKEN is in the drop list but is not checked here: the
// switch-key launch sets it to the key, so it is legitimately present. Its
// inherited value is covered by TestClaudeChildEnvAuthTokenIsTheLaunchValueNotTheInheritedOne.
func TestClaudeChildEnvDropsInheritedModelVars(t *testing.T) {
	resetAPIKey(t)
	for _, name := range claudeInheritedModelVars {
		t.Setenv(name, "inherited-should-not-survive")
	}

	env := claudeChildEnv("", nil)

	for _, name := range claudeInheritedModelVars {
		if name == "ANTHROPIC_AUTH_TOKEN" {
			continue
		}
		if strings.Contains(strings.Join(env, "\n"), name+"=") {
			t.Errorf("%s reached the child environment", name)
		}
	}
}

// TestClaudeChildEnvAuthTokenIsTheLaunchValueNotTheInheritedOne pins the
// switch-key path: ANTHROPIC_AUTH_TOKEN carries the launch's key, and an
// inherited value of the same name never survives. The launch value must be
// non-empty here, or a regression that drops it is indistinguishable from one
// that keeps an empty entry.
func TestClaudeChildEnvAuthTokenIsTheLaunchValueNotTheInheritedOne(t *testing.T) {
	resetAPIKey(t)
	envconfig.SetAPIKey("sk-launch-key")
	t.Cleanup(func() { envconfig.SetAPIKey("") })
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "inherited-should-not-survive")
	envconfig.SetDeviceMode(false)

	env := claudeChildEnv("", nil)

	if got := envValue(env, "ANTHROPIC_AUTH_TOKEN="); got != "sk-launch-key" {
		t.Fatalf("ANTHROPIC_AUTH_TOKEN = %q, want the launch key, never the inherited one", got)
	}
}

// TestClaudeChildEnvDeviceModeHasNoAuthToken pins the device-mode credential
// channel: ANTHROPIC_AUTH_TOKEN is absent from the child environment from every
// source, the helper TTL is set, and the console key stays empty. Claude Code
// treats ANTHROPIC_AUTH_TOKEN as a fixed credential it never refreshes, so
// leaving it set — even to a device token — would pin the session to the
// launch-time token and stop the apiKeyHelper from refreshing it.
func TestClaudeChildEnvDeviceModeHasNoAuthToken(t *testing.T) {
	resetAPIKey(t)
	envconfig.SetAPIKey("sk-should-not-appear")
	envconfig.SetDeviceMode(true)
	t.Cleanup(func() {
		envconfig.SetAPIKey("")
		envconfig.SetDeviceMode(false)
	})
	// An operator who exported ANTHROPIC_AUTH_TOKEN in their shell must not
	// pass it through either.
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "inherited-should-not-survive")

	env := claudeChildEnv("", nil)

	if strings.Contains(strings.Join(env, "\n"), "ANTHROPIC_AUTH_TOKEN=") {
		t.Fatalf("ANTHROPIC_AUTH_TOKEN is present in device mode:\n%v", env)
	}
	if got := envValue(env, "ANTHROPIC_API_KEY="); got != "" {
		t.Fatalf("ANTHROPIC_API_KEY = %q, want empty in device mode", got)
	}
	if got := envValue(env, "CLAUDE_CODE_API_KEY_HELPER_TTL_MS="); got != strconv.Itoa(claudeHelperTTLMs) {
		t.Fatalf("CLAUDE_CODE_API_KEY_HELPER_TTL_MS = %q, want %d", got, claudeHelperTTLMs)
	}
}

// TestDeviceSettingsJSONAddsHelperToExistingSettings verifies the apiKeyHelper
// is merged into the launch settings without dropping the model, the overrides
// or the picker rows the launch already built.
func TestDeviceSettingsJSONAddsHelperToExistingSettings(t *testing.T) {
	settings, err := deviceSettingsJSON(`{"model":"m[1m]","modelPicker":{"replaceBuiltInOptions":true}}`)
	if err != nil {
		t.Fatalf("deviceSettingsJSON: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(settings), &got); err != nil {
		t.Fatalf("result is not JSON: %v", err)
	}
	if got["model"] != "m[1m]" {
		t.Errorf("model was dropped: %v", got["model"])
	}
	if _, ok := got["modelPicker"]; !ok {
		t.Error("modelPicker was dropped")
	}
	helper, _ := got["apiKeyHelper"].(string)
	if !strings.HasSuffix(helper, " auth token") {
		t.Errorf("apiKeyHelper = %q, want it to end with the helper subcommand", helper)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(helper, strconv.Quote(exe)) {
		t.Errorf("apiKeyHelper = %q, want the quoted executable %q first", helper, exe)
	}
}

// TestDeviceSettingsJSONKeepsTheOverrideKeyOrder verifies the device merge
// leaves the modelOverrides object's key order untouched. Claude Code names a
// model by alias as the first key, in object order, that maps to it, so a
// resorted object makes a tier alias run as the oldest model mapped to it.
func TestDeviceSettingsJSONKeepsTheOverrideKeyOrder(t *testing.T) {
	settings, err := claudeSettingsJSON("claude-tier-haiku", nil)
	if err != nil {
		t.Fatalf("claudeSettingsJSON: %v", err)
	}
	merged, err := deviceSettingsJSON(settings)
	if err != nil {
		t.Fatalf("deviceSettingsJSON: %v", err)
	}
	var before, after struct {
		ModelOverrides json.RawMessage `json:"modelOverrides"`
	}
	if err := json.Unmarshal([]byte(settings), &before); err != nil {
		t.Fatalf("settings is not JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(merged), &after); err != nil {
		t.Fatalf("merged settings is not JSON: %v", err)
	}
	if string(before.ModelOverrides) != string(after.ModelOverrides) {
		t.Fatalf("modelOverrides key order changed through the device merge\nbefore: %s\nafter:  %s", before.ModelOverrides, after.ModelOverrides)
	}
}

// An inherited variable must not appear twice, which would make the child's
// value depend on which duplicate the OS reads first.
func TestClaudeChildEnvKeepsOneEntryPerName(t *testing.T) {
	resetAPIKey(t)
	t.Setenv("ANTHROPIC_BASE_URL", "https://inherited.example.test")
	envconfig.SetBaseURL("https://switch.example.test")
	t.Cleanup(func() { envconfig.SetBaseURL("") })

	env := claudeChildEnv("", nil)

	count := 0
	for _, name := range envNames(env) {
		if name == "ANTHROPIC_BASE_URL" || name == "ANTHROPIC_AUTH_TOKEN" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("expected ANTHROPIC_BASE_URL and ANTHROPIC_AUTH_TOKEN exactly once each, got %d entries: %v", count, envNames(env))
	}
	if got := envValue(env, "ANTHROPIC_BASE_URL="); got != "https://switch.example.test" {
		t.Fatalf("ANTHROPIC_BASE_URL = %q, want the launch value to win over the inherited one", got)
	}
}

// CLAUDE_CODE_SUBAGENT_MODEL is set exactly when a dedicated subagent model was
// picked, and omitted otherwise so subagents inherit the session model.
func TestClaudeChildEnvSetsSubagentModelOnlyWhenPicked(t *testing.T) {
	resetAPIKey(t)
	SetSubagentModel("")
	t.Cleanup(func() { SetSubagentModel("") })

	if env := claudeChildEnv("", nil); strings.Contains(strings.Join(env, "\n"), "CLAUDE_CODE_SUBAGENT_MODEL=") {
		t.Fatalf("CLAUDE_CODE_SUBAGENT_MODEL is set with no subagent model picked:\n%v", env)
	}

	// Subagents produce most of the token traffic, so this variable is the one
	// channel that lets them run something cheaper than the parent.
	SetSubagentModel("cheap-model")
	env := claudeChildEnv("", nil)
	if got := envValue(env, "CLAUDE_CODE_SUBAGENT_MODEL="); got != "cheap-model[1m]" {
		t.Fatalf("CLAUDE_CODE_SUBAGENT_MODEL = %q, want cheap-model[1m]", got)
	}
}

// ANTHROPIC_DEFAULT_OPUS_MODEL carries the launch's pinned model. Claude Code
// reads it for the /model picker's Default row, which otherwise names the
// Opus model from its own catalog whatever the launch routes to. An inherited
// value must not survive: the launch value is the only copy the child sees.
func TestClaudeChildEnvSetsOpusModelToPinnedModel(t *testing.T) {
	resetAPIKey(t)
	t.Setenv("ANTHROPIC_DEFAULT_OPUS_MODEL", "inherited-should-not-survive")

	env := claudeChildEnv("smart", nil)
	if got := envValue(env, "ANTHROPIC_DEFAULT_OPUS_MODEL="); got != "smart[1m]" {
		t.Fatalf("ANTHROPIC_DEFAULT_OPUS_MODEL = %q, want smart[1m]", got)
	}
	count := 0
	for _, name := range envNames(env) {
		if name == "ANTHROPIC_DEFAULT_OPUS_MODEL" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("ANTHROPIC_DEFAULT_OPUS_MODEL appears %d times, want once: %v", count, envNames(env))
	}

	if env := claudeChildEnv("", nil); strings.Contains(strings.Join(env, "\n"), "ANTHROPIC_DEFAULT_OPUS_MODEL=") {
		t.Fatalf("ANTHROPIC_DEFAULT_OPUS_MODEL is set with no pinned model:\n%v", env)
	}
}

func TestClaudeBaseURLTrailingV1(t *testing.T) {
	for _, tc := range []struct {
		name string
		host string
		want string
	}{
		{"no suffix passes through", "https://switch.example.com", "https://switch.example.com"},
		{"trailing v1 is stripped", "https://switch.example.com/v1", "https://switch.example.com"},
		{"only one v1 is stripped", "https://switch.example.com/v1/v1", "https://switch.example.com/v1"},
		{"v1 elsewhere in the path stays", "https://switch.example.com/v1/models", "https://switch.example.com/v1/models"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := claudeBaseURL(tc.host); got != tc.want {
				t.Fatalf("claudeBaseURL(%q) = %q, want %q", tc.host, got, tc.want)
			}
		})
	}
}

func TestClaudeEnvVarsBaseURLDropsTrailingV1(t *testing.T) {
	resetAPIKey(t)
	envconfig.SetBaseURL("https://switch.example.com/v1")
	t.Cleanup(func() { envconfig.SetBaseURL("") })

	env := (&Claude{}).envVars()

	if got := envValue(env, "ANTHROPIC_BASE_URL="); got != "https://switch.example.com" {
		t.Fatalf("ANTHROPIC_BASE_URL = %q, want https://switch.example.com", got)
	}
}

// claudeModelName always ends with exactly one [1m], whatever the switch sent.
// The suffix is a client-side budgeting instruction: without it Claude Code
// assumes a 200k window and compacts a long session early.
func TestClaudeModelNameCarriesItsTiersSuffix(t *testing.T) {
	for _, tc := range []struct {
		name  string
		model string
		want  string
	}{
		{"bare name gets the suffix", "some-model", "some-model[1m]"},
		{"one suffix is not doubled", "some-model[1m]", "some-model[1m]"},
		{"two suffixes collapse to one", "some-model[1m][1m]", "some-model[1m]"},
		{"empty name stays empty", "", ""},
		// The Switch serves every tier with a 1M window, haiku included.
		{"a haiku name gets the suffix", "claude-tier-haiku", "claude-tier-haiku[1m]"},
		{"a haiku name keeps one suffix", "claude-tier-haiku[1m][1m]", "claude-tier-haiku[1m]"},
		{"the other tiers keep the suffix", "claude-tier-sonnet", "claude-tier-sonnet[1m]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := claudeModelName(tc.model); got != tc.want {
				t.Fatalf("claudeModelName(%q) = %q, want %q", tc.model, got, tc.want)
			}
		})
	}
}

// A launch states the model in two places that must agree: the inline settings
// JSON, which the child reads as its configuration, and the --model flag, which
// outranks it. Both carry the same [1m] spelling, so whichever one Claude Code
// resolves the model from, the 1M window holds.
func TestClaudeArgsCarryInlineSettings(t *testing.T) {
	settings, err := claudeSettingsJSON("some-model", []ModelRow{{Label: "some-model", Model: "some-model"}})
	if err != nil {
		t.Fatalf("claudeSettingsJSON: %v", err)
	}
	args := (&Claude{}).args("some-model", nil, settings, []string{"--verbose"})

	want := []string{"--settings", settings, "--model", "some-model[1m]", "--verbose"}
	if strings.Join(args, " ") != strings.Join(want, " ") {
		t.Fatalf("args = %v, want %v", args, want)
	}

	// The flag's value and the settings model are the same name, so the flag
	// cannot silently replace the settings model with a different one.
	var flagModel string
	for i, a := range args {
		if a == "--model" && i+1 < len(args) {
			flagModel = args[i+1]
		}
	}
	if got := claudeModelName("some-model"); flagModel != got {
		t.Fatalf("--model value = %q, want %q (the settings model's spelling)", flagModel, got)
	}

	if args := (&Claude{}).args("", nil, "", []string{"--verbose"}); strings.Join(args, " ") != "--verbose" {
		t.Fatalf("args with no settings and no model = %v, want [--verbose]", args)
	}
}

// prizmal owns the model decision, so the launcher states it on the command
// line rather than patching a value the operator typed. The appended --model
// carries the [1m] suffix, which is what buys the 1M-token window; a bare value
// would outrank the settings JSON's model and drop the suffix, and the session
// would budget the 200k unknown-model window and warn.
func TestClaudeArgsStateTheModelThemselves(t *testing.T) {
	for _, tc := range []struct {
		name     string
		model    string
		settings string
		extra    []string
		want     []string
	}{
		{
			name:  "the model is appended with its suffix",
			model: "smart",
			want:  []string{"--model", "smart[1m]"},
		},
		{
			name:  "the suffix is not doubled",
			model: "smart[1m]",
			want:  []string{"--model", "smart[1m]"},
		},
		{
			name:     "the settings JSON comes first",
			model:    "smart",
			settings: "{}",
			want:     []string{"--settings", "{}", "--model", "smart[1m]"},
		},
		{
			name:  "harness arguments follow, and -m is permission mode",
			model: "smart",
			extra: []string{"--resume", "abc", "-m", "plan"},
			want:  []string{"--model", "smart[1m]", "--resume", "abc", "-m", "plan"},
		},
		{
			name:  "a haiku model carries the suffix, like its settings model",
			model: "claude-tier-haiku",
			want:  []string{"--model", "claude-tier-haiku[1m]"},
		},
		{
			name:  "no model appends nothing",
			model: "",
			extra: []string{"--verbose"},
			want:  []string{"--verbose"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := (&Claude{}).args(tc.model, nil, tc.settings, tc.extra)
			if strings.Join(got, " ") != strings.Join(tc.want, " ") {
				t.Fatalf("args(%q, %q, %v) = %v, want %v", tc.model, tc.settings, tc.extra, got, tc.want)
			}
		})
	}
}

// args returns a new slice, so the caller's slice is never rewritten behind its
// back.
func TestClaudeArgsDoNotMutateTheCallersSlice(t *testing.T) {
	extra := []string{"--resume", "abc"}
	_ = (&Claude{}).args("smart", nil, "", extra)
	if strings.Join(extra, " ") != "--resume abc" {
		t.Fatalf("the caller's slice was rewritten: %v", extra)
	}
}

// The settings JSON carries the pinned model, the tier remaps, and the picker
// rows, with the model's [1m] suffix applied and the rows' labels bare.
func TestClaudeSettingsJSONShape(t *testing.T) {
	rows := ModelRows([]LaunchModel{{Name: "claude-tier-haiku"}, {Name: "claude-tier-sonnet"}})
	settings, err := claudeSettingsJSON("claude-tier-haiku", rows)
	if err != nil {
		t.Fatalf("claudeSettingsJSON: %v", err)
	}

	var got struct {
		Model          string            `json:"model"`
		ModelOverrides map[string]string `json:"modelOverrides"`
		ModelPicker    struct {
			ReplaceBuiltInOptions bool `json:"replaceBuiltInOptions"`
			Options               []struct {
				Label       string `json:"label"`
				Model       string `json:"model"`
				BehavesAs   string `json:"behavesAs"`
				Description string `json:"description"`
			} `json:"options"`
		} `json:"modelPicker"`
	}
	if err := json.Unmarshal([]byte(settings), &got); err != nil {
		t.Fatalf("settings JSON does not parse: %v\n%s", err, settings)
	}

	if got.Model != "claude-tier-haiku[1m]" {
		t.Errorf("model = %q, want claude-tier-haiku[1m]", got.Model)
	}
	if !got.ModelPicker.ReplaceBuiltInOptions {
		t.Error("replaceBuiltInOptions is false; built-in rows would appear beside the tenant's")
	}
	if len(got.ModelPicker.Options) != 2 {
		t.Fatalf("picker options = %d, want 2: %s", len(got.ModelPicker.Options), settings)
	}
	if got.ModelPicker.Options[0].Label != "tier-haiku" {
		t.Errorf("option label = %q, want tier-haiku (the suffix is stripped for display)", got.ModelPicker.Options[0].Label)
	}
	if got.ModelPicker.Options[0].Model != "claude-tier-haiku[1m]" {
		t.Errorf("option model = %q, want claude-tier-haiku[1m]", got.ModelPicker.Options[0].Model)
	}
	if got.ModelPicker.Options[0].BehavesAs != "claude-sonnet-5" {
		t.Errorf("option behavesAs = %q, want claude-sonnet-5", got.ModelPicker.Options[0].BehavesAs)
	}
	if got.ModelPicker.Options[0].Description != "Haiku tier" {
		t.Errorf("option description = %q, want Haiku tier", got.ModelPicker.Options[0].Description)
	}
	if got.ModelPicker.Options[1].Model != "claude-tier-sonnet[1m]" {
		t.Errorf("option model = %q, want claude-tier-sonnet[1m]", got.ModelPicker.Options[1].Model)
	}
	if got.ModelOverrides["claude-opus-5"] != "claude-tier-opus[1m]" {
		t.Errorf("modelOverrides[claude-opus-5] = %q, want claude-tier-opus[1m]", got.ModelOverrides["claude-opus-5"])
	}
}

// TestClaudeModelOverridesKeysAreCanonicalIds pins the exact override keys.
//
// A key that is not a first-party id the running build knows is silently
// ignored, so the mapping is only as good as the spellings. Haiku 4.5 shipped
// as a named snapshot. Claude Code's own id for that tier is the dated
// claude-haiku-4-5-20251001, and an override keyed on the undated
// claude-haiku-4-5 does nothing: the wire then carries the dated id rather
// than the tenant's alias. The same rule bites the variant spellings the
// catalog never gave a first-party id (claude-haiku-4-5-20251001-v1,
// claude-fable-5-mythos-5) and the mythos family (claude-mythos-5,
// claude-mythos-5-1), so none appears here. The haiku ids are absent for
// another reason, which TestClaudeModelOverridesCarryNoHaikuKey states.
func TestClaudeModelOverridesKeysAreCanonicalIds(t *testing.T) {
	wantKeys := []string{
		"claude-opus-5-5",
		"claude-opus-5",
		"claude-opus-4-8",
		"claude-opus-4-7",
		"claude-opus-4-6",
		"claude-opus-4-5-20251101",
		"claude-opus-4-20250514",
		"claude-opus-4-1-20250805",
		"claude-sonnet-5-5",
		"claude-sonnet-5",
		"claude-sonnet-4-6",
		"claude-sonnet-4-5-20250929",
		"claude-sonnet-4-20250514",
		"claude-3-7-sonnet-20250219",
		"claude-3-5-sonnet-20241022",
		"claude-fable-5-1",
		"claude-fable-5",
	}
	overrides := claudeModelOverrides()

	if len(overrides) != len(wantKeys) {
		t.Fatalf("modelOverrides has %d entries, want %d: %v", len(overrides), len(wantKeys), wantKeys)
	}
	for _, key := range wantKeys {
		if _, ok := overrides[key]; !ok {
			t.Errorf("modelOverrides is missing key %q; without it the tier routes to Claude Code's built-in id", key)
		}
	}
	for key := range overrides {
		found := false
		for _, want := range wantKeys {
			if key == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("modelOverrides has unexpected key %q", key)
		}
	}
}

// Claude Code identifies a model whose name equals a modelOverrides value as
// the first key, in the settings' own key order, that maps to it and that the
// running build knows. A tier alias the launch runs or offers as a row is such
// a value, so the first key for each tier decides which model's profile the
// session runs on. Sorted keys would put the oldest id first: the haiku alias
// would run as the retired Claude 3.5 Haiku, and opus as Opus 4.1.
func TestClaudeModelOverridesNameEachTiersProfileFirst(t *testing.T) {
	settings, err := claudeSettingsJSON("smart", nil)
	if err != nil {
		t.Fatalf("claudeSettingsJSON: %v", err)
	}
	var raw struct {
		ModelOverrides json.RawMessage `json:"modelOverrides"`
	}
	if err := json.Unmarshal([]byte(settings), &raw); err != nil {
		t.Fatalf("settings JSON does not parse: %v\n%s", err, settings)
	}

	dec := json.NewDecoder(bytes.NewReader(raw.ModelOverrides))
	if _, err := dec.Token(); err != nil {
		t.Fatalf("modelOverrides is not an object: %v", err)
	}
	firstKey := map[string]string{}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		var value string
		if err := dec.Decode(&value); err != nil {
			t.Fatal(err)
		}
		if _, seen := firstKey[value]; !seen {
			firstKey[value] = key.(string)
		}
	}

	for tier, profile := range tierProfiles {
		alias := claudeModelName(claudeTierModel(tier))
		if !profileInLineage(tier) {
			// The session on this alias must miss the overrides, so that
			// Claude Code reads the row's behavesAs instead.
			if got, ok := firstKey[alias]; ok {
				t.Errorf("modelOverrides maps %q to %s, so a session on the alias runs as %s instead of %s", got, alias, got, profile.behavesAs)
			}
			continue
		}
		if got := firstKey[alias]; got != profile.behavesAs {
			t.Errorf("first modelOverrides key for %s = %q, want %q, the id its rows behave as", alias, got, profile.behavesAs)
		}
	}
}

// The ordered form the launch sends carries the same entries as the mapping,
// each key once. A profile id outside its tier's lineage would add a key
// mapped to an empty model, and a lineage listing an id twice would repeat a
// JSON key.
func TestClaudeOrderedModelOverridesMatchTheMapping(t *testing.T) {
	want := claudeModelOverrides()
	ordered := claudeOrderedModelOverrides()

	seen := map[string]bool{}
	for _, entry := range ordered {
		if seen[entry.key] {
			t.Errorf("key %q is emitted twice", entry.key)
		}
		seen[entry.key] = true
		if entry.value == "" || entry.value != want[entry.key] {
			t.Errorf("modelOverrides[%q] = %q, want %q", entry.key, entry.value, want[entry.key])
		}
	}
	if len(ordered) != len(want) {
		t.Errorf("emitted %d keys, want %d", len(ordered), len(want))
	}
}

// The tier overrides are spelled exactly as the pinned model and the rows are.
//
// A request that resolves through a tier short name takes the modelOverrides
// value. Without the [1m] suffix it budgets a 200k window and compacts a long
// session early.
func TestClaudeModelOverridesCarryTheirTiersSuffix(t *testing.T) {
	overrides := claudeModelOverrides()

	for key, value := range overrides {
		if !strings.HasSuffix(value, oneMillionSuffix) {
			t.Errorf("modelOverrides[%q] = %q, want it to end in %s", key, value, oneMillionSuffix)
		}
		if strings.Count(value, oneMillionSuffix) != 1 {
			t.Errorf("modelOverrides[%q] = %q, want exactly one %s", key, value, oneMillionSuffix)
		}
	}
}

// The overrides carry no haiku id. Claude Code resolves a model whose name
// equals an override value, with or without [1m], to that value's key before
// it reads the row's behavesAs. A haiku key would make the haiku-tier session
// run as Haiku 4.5, which Claude Code refuses auto mode to, and would drop
// the tier's /model row, whose 1M window Haiku 4.5 lacks.
func TestClaudeModelOverridesCarryNoHaikuKey(t *testing.T) {
	for key, value := range claudeModelOverrides() {
		if strings.Contains(key, "haiku") {
			t.Errorf("modelOverrides[%q] = %q, and a haiku key resolves the haiku-tier session to a haiku model", key, value)
		}
	}
}

// A row's behavesAs names the model whose client-side handling Claude Code
// applies to the row, and it is never the id the row sends. Two rows with
// different profiles still carry their own model ids, which are what route.
func TestClaudeSettingsRowModelsAreNotTheirProfiles(t *testing.T) {
	rows := ModelRows([]LaunchModel{{Name: "claude-tier-opus"}, {Name: "cheap-model"}})
	settings, err := claudeSettingsJSON("cheap-model", rows)
	if err != nil {
		t.Fatalf("claudeSettingsJSON: %v", err)
	}

	var got struct {
		Model       string `json:"model"`
		ModelPicker struct {
			Options []struct {
				Model     string `json:"model"`
				BehavesAs string `json:"behavesAs"`
			} `json:"options"`
		} `json:"modelPicker"`
	}
	if err := json.Unmarshal([]byte(settings), &got); err != nil {
		t.Fatalf("settings JSON does not parse: %v\n%s", err, settings)
	}

	// The session's model is the one the launch chose, whatever the rows show.
	if got.Model != "cheap-model[1m]" {
		t.Errorf("pinned model = %q, want cheap-model[1m]", got.Model)
	}

	want := map[string]string{"claude-tier-opus": "claude-opus-5", "cheap-model": ""}
	if len(got.ModelPicker.Options) != len(want) {
		t.Fatalf("picker options = %d, want %d: %s", len(got.ModelPicker.Options), len(want), settings)
	}
	for _, option := range got.ModelPicker.Options {
		model := strings.TrimSuffix(option.Model, oneMillionSuffix)
		tier, ok := want[model]
		if !ok {
			t.Errorf("row sends model %q, which is not one of the catalog's ids", option.Model)
			continue
		}
		if option.BehavesAs != tier {
			t.Errorf("row %q behavesAs = %q, want %q", model, option.BehavesAs, tier)
		}
		// The profile shapes the client. It must never be what the row sends.
		if option.BehavesAs != "" && option.BehavesAs == model {
			t.Errorf("row sends its profile %q as its model, so the profile would steer routing", model)
		}
	}
}

// A row with no inferred tier carries no behavesAs field at all, rather than an
// empty string the harness would have to interpret.
func TestClaudeSettingsRowsOmitAnUnknownTier(t *testing.T) {
	settings, err := claudeSettingsJSON("gpt-oss:20b", []ModelRow{{Label: "gpt-oss:20b", Model: "gpt-oss:20b"}})
	if err != nil {
		t.Fatalf("claudeSettingsJSON: %v", err)
	}
	if strings.Contains(settings, "behavesAs") {
		t.Fatalf("settings carry a behavesAs for a name with no tier word:\n%s", settings)
	}
}

// A launch that names no model builds no settings, rather than an empty object
// Claude Code would have to interpret.
func TestClaudeSettingsJSONEmptyWithoutAModel(t *testing.T) {
	settings, err := claudeSettingsJSON("", nil)
	if err != nil {
		t.Fatalf("claudeSettingsJSON: %v", err)
	}
	if settings != "" {
		t.Fatalf("settings = %q, want empty", settings)
	}
}

// The settings JSON is a command-line argument, so it is scoped to the launched
// process. It must never be written to a file under ~/.claude.
func TestClaudeLaunchWritesNoSettingsFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if _, err := claudeSettingsJSON("some-model", []ModelRow{{Label: "some-model", Model: "some-model"}}); err != nil {
		t.Fatalf("claudeSettingsJSON: %v", err)
	}

	for _, path := range []string{".claude", filepath.Join(".claude", "settings.json")} {
		if _, err := os.Stat(filepath.Join(home, path)); err == nil {
			t.Errorf("the launch wrote %s; the settings JSON is process-scoped", path)
		}
	}
}

// The launch leaves CLAUDE_CODE_MODEL_CAPABILITIES to the operator: an
// exported value reaches Claude Code as it was, and the launch adds no entry.
func TestClaudeChildEnvPassesCapabilitiesThrough(t *testing.T) {
	resetAPIKey(t)
	const inherited = "my-model=effort"
	t.Setenv("CLAUDE_CODE_MODEL_CAPABILITIES", inherited)

	env := claudeChildEnv("claude-row-a", []ModelRow{{Label: "a", Model: "claude-row-a"}})

	if got := envValue(env, "CLAUDE_CODE_MODEL_CAPABILITIES="); got != inherited {
		t.Fatalf("CLAUDE_CODE_MODEL_CAPABILITIES = %q, want %q", got, inherited)
	}
}

// A row the Switch tags haiku carries [1m], like every other tier. The Switch
// serves every tier with a 1M window.
func TestClaudeModelPickerGivesAHaikuTaggedRowTheSuffix(t *testing.T) {
	picker := claudeModelPicker(ModelRows([]LaunchModel{{Name: "smart", Tier: "haiku"}, {Name: "flash", Tier: "opus"}}))
	options := picker["options"].([]any)
	if got := options[0].(map[string]any)["model"]; got != "smart[1m]" {
		t.Errorf("haiku-tagged row model = %v, want smart[1m]", got)
	}
	if got := options[1].(map[string]any)["model"]; got != "flash[1m]" {
		t.Errorf("opus-tagged row model = %v, want flash[1m]", got)
	}
}

// The Switch's tier decides a row's window, whatever its name says.
func TestClaudeRowTakesItsWindowFromTheSwitchsTier(t *testing.T) {
	rows := ModelRows([]LaunchModel{{Name: "team-haiku-blend", Tier: "opus"}})
	if got := claudeRowModelName(rows[0]); got != "team-haiku-blend[1m]" {
		t.Fatalf("opus-tagged row named haiku = %q, want team-haiku-blend[1m]", got)
	}
}

// A config launched by its own name runs as its own picker row does: a
// haiku-tagged config carries the same [1m] in the settings model, the
// --model flag, and the Opus-tier variable, or Claude Code runs a model no
// row matches.
func TestClaudeLaunchSpellsATieredConfigAsItsRow(t *testing.T) {
	rows := ModelRows([]LaunchModel{{Name: "flash", Tier: "haiku"}})
	named := claudeLaunchModelName("flash", rows)
	if named != "flash[1m]" {
		t.Fatalf("launch name = %q, want flash[1m]", named)
	}
	settings, err := claudeSettingsJSON("flash", rows)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(settings, `"model":"flash[1m]"`) {
		t.Errorf("settings = %s, want the model spelled flash[1m]", settings)
	}
	if args := (&Claude{}).args("flash", rows, "", nil); strings.Join(args, " ") != "--model flash[1m]" {
		t.Errorf("args = %v, want --model flash[1m]", args)
	}
	if !slices.Contains(claudeChildEnv("flash", rows), "ANTHROPIC_DEFAULT_OPUS_MODEL=flash[1m]") {
		t.Errorf("child env lacks ANTHROPIC_DEFAULT_OPUS_MODEL=flash[1m]")
	}
}

// TestEnsureHelperBaseURLPinsTheResolvedHost fixes the environment the
// apiKeyHelper runs under. The helper is a separate prizmal process that
// resolves the URL from scratch, so a launch aimed at a non-default host by
// --url would otherwise let the helper fall back to the production default and
// mint a token the launch's Switch cannot use.
func TestEnsureHelperBaseURLPinsTheResolvedHost(t *testing.T) {
	env := []string{"PATH=/usr/bin", "ANTHROPIC_BASE_URL=https://api.staging.prizmal.ai"}

	got := ensureHelperBaseURL(env, "https://api.staging.prizmal.ai")
	if v := envValue(got, envconfig.EnvVar+"="); v != "https://api.staging.prizmal.ai" {
		t.Fatalf("%s = %q, want the resolved host pinned for the helper", envconfig.EnvVar, v)
	}

	// An operator's own exported URL is already the host the child resolves, so
	// the pin does not add a second entry.
	withEnv := append([]string{envconfig.EnvVar + "=https://operator.example.test"}, env...)
	got2 := ensureHelperBaseURL(withEnv, "https://api.staging.prizmal.ai")
	count := 0
	for _, name := range envNames(got2) {
		if name == envconfig.EnvVar {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("%s appears %d times, want the operator's single entry", envconfig.EnvVar, count)
	}
}

// TestClaudeChildEnvNeverInheritsAnthropicAuthTokenOutsideDeviceMode pins the
// switch-key path's precedence: the launch's own ANTHROPIC_AUTH_TOKEN wins and
// the operator's exported one never rides along. That was true before device
// mode existed (the launch value is a "fixed name", so the inherited copy is
// skipped) and stays true now. No operator-facing switch key travels in this
// variable, so dropping the inherited copy costs no credential.
func TestClaudeChildEnvNeverInheritsAnthropicAuthTokenOutsideDeviceMode(t *testing.T) {
	resetAPIKey(t)
	envconfig.SetDeviceMode(false)
	t.Cleanup(func() { envconfig.SetDeviceMode(false) })
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "inherited-shell-token")

	// With an explicit switch key, the child carries it, never the inherited one.
	envconfig.SetAPIKey("sk-explicit")
	t.Cleanup(func() { envconfig.SetAPIKey("") })
	if got := envValue(claudeChildEnv("", nil), "ANTHROPIC_AUTH_TOKEN="); got != "sk-explicit" {
		t.Fatalf("ANTHROPIC_AUTH_TOKEN = %q, want the explicit key, never the inherited one", got)
	}

	// With no key from any prizmal source, the variable is empty exactly as
	// before device mode: prizmal reads its key from $PRIZMAL_SWITCH_KEY, the
	// flag, or the config file, never from ANTHROPIC_AUTH_TOKEN.
	envconfig.SetAPIKey("")
	if got := envValue(claudeChildEnv("", nil), "ANTHROPIC_AUTH_TOKEN="); got != "" {
		t.Fatalf("ANTHROPIC_AUTH_TOKEN = %q, want empty when no prizmal key is configured", got)
	}
}
