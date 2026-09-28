package launch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
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
// model env var reaches the child. A value exported in the operator's shell for
// Anthropic itself must not ride along and re-route a launch the operator aimed
// at the Switch.
func TestClaudeChildEnvDropsInheritedModelVars(t *testing.T) {
	resetAPIKey(t)
	for _, name := range claudeInheritedModelVars {
		t.Setenv(name, "inherited-should-not-survive")
	}

	env := claudeChildEnv("", nil)

	for _, name := range claudeInheritedModelVars {
		if strings.Contains(strings.Join(env, "\n"), name+"=") {
			t.Errorf("%s reached the child environment", name)
		}
	}
	if got := envValue(env, "ANTHROPIC_AUTH_TOKEN="); got != "" {
		t.Errorf("ANTHROPIC_AUTH_TOKEN = %q, want the empty launch value", got)
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
func TestClaudeModelNameEndsWithExactlyOneSuffix(t *testing.T) {
	for _, tc := range []struct {
		name  string
		model string
		want  string
	}{
		{"bare name gets the suffix", "some-model", "some-model[1m]"},
		{"one suffix is not doubled", "some-model[1m]", "some-model[1m]"},
		{"two suffixes collapse to one", "some-model[1m][1m]", "some-model[1m]"},
		{"empty name stays empty", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := claudeModelName(tc.model); got != tc.want {
				t.Fatalf("claudeModelName(%q) = %q, want %q", tc.model, got, tc.want)
			}
		})
	}
}

// A launch passes the model through the inline settings JSON rather than the
// --model flag, so a value set in the operator's settings.json or shell cannot
// outrank it.
func TestClaudeArgsCarryInlineSettings(t *testing.T) {
	settings, err := claudeSettingsJSON("some-model", []ModelRow{{Label: "some-model", Model: "some-model"}})
	if err != nil {
		t.Fatalf("claudeSettingsJSON: %v", err)
	}
	args := (&Claude{}).args(settings, []string{"--verbose"})

	want := []string{"--settings", settings, "--verbose"}
	if strings.Join(args, " ") != strings.Join(want, " ") {
		t.Fatalf("args = %v, want %v", args, want)
	}
	if strings.Contains(strings.Join(args, " "), "--model") {
		t.Fatalf("args carry --model; the settings JSON pins the model instead: %v", args)
	}

	if args := (&Claude{}).args("", []string{"--verbose"}); strings.Join(args, " ") != "--verbose" {
		t.Fatalf("args with no settings = %v, want [--verbose]", args)
	}
}

// The settings JSON carries the pinned model, the tier remaps, and the picker
// rows, with the model's [1m] suffix applied and the rows' labels bare.
func TestClaudeSettingsJSONShape(t *testing.T) {
	rows := []ModelRow{
		{Label: "tier-haiku", Model: "claude-tier-haiku", BehavesAs: "haiku"},
		{Label: "tier-sonnet", Model: "claude-tier-sonnet", BehavesAs: "sonnet"},
	}
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
				Label     string `json:"label"`
				Model     string `json:"model"`
				BehavesAs string `json:"behavesAs"`
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
	if got.ModelPicker.Options[0].BehavesAs != "haiku" {
		t.Errorf("option behavesAs = %q, want haiku", got.ModelPicker.Options[0].BehavesAs)
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
// claude-mythos-5-1), so none appears here.
func TestClaudeModelOverridesKeysAreCanonicalIds(t *testing.T) {
	wantKeys := []string{
		"claude-opus-5",
		"claude-opus-4-8",
		"claude-opus-4-7",
		"claude-opus-4-6",
		"claude-opus-4-5-20251101",
		"claude-opus-4-20250514",
		"claude-opus-4-1-20250805",
		"claude-sonnet-5",
		"claude-sonnet-4-6",
		"claude-sonnet-4-5-20250929",
		"claude-sonnet-4-20250514",
		"claude-3-7-sonnet-20250219",
		"claude-3-5-sonnet-20241022",
		"claude-haiku-4-5-20251001",
		"claude-3-5-haiku-20241022",
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

// The tier overrides carry the [1m] suffix too.
//
// A tier name without the suffix budgets a 200k window and compacts a long
// session early, which is the outcome the suffix exists to prevent. A request
// that resolves through a tier short name takes the modelOverrides value, so
// that value needs the suffix exactly as the pinned model and the rows do.
func TestClaudeModelOverridesCarryTheSuffix(t *testing.T) {
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

// A row's behavesAs is a display hint and never the id the row sends. Two rows
// showing different tiers still carry their own model ids, and a row whose
// behavesAs equals a tier word still sends the model it was built from.
func TestClaudeSettingsRowModelsAreNotTierHints(t *testing.T) {
	rows := []ModelRow{
		{Label: "tier-opus", Model: "claude-tier-opus", BehavesAs: "opus"},
		{Label: "cheap-model", Model: "cheap-model"},
	}
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

	want := map[string]string{"claude-tier-opus": "opus", "cheap-model": ""}
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
		// The hint describes the row. It must never be what the row sends.
		if option.BehavesAs != "" && option.BehavesAs == model {
			t.Errorf("row sends its tier hint %q as its model, so the hint would steer routing", model)
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

// capabilityEntries splits a CLAUDE_CODE_MODEL_CAPABILITIES value into its
// ;-separated entries.
func capabilityEntries(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(value, ";")
}

// launchEnvWithCapabilities builds the child environment for model and rows,
// with subagent as the picked subagent model and inherited as the operator's
// exported CLAUDE_CODE_MODEL_CAPABILITIES.
func launchEnvWithCapabilities(t *testing.T, subagent, inherited, model string, rows []ModelRow) []string {
	t.Helper()
	resetAPIKey(t)
	SetSubagentModel(subagent)
	t.Cleanup(func() { SetSubagentModel("") })
	t.Setenv(claudeCapabilitiesVar, inherited)
	return claudeChildEnv(model, rows)
}

// Claude Code keeps WebSearch's required tool choice only for a model it knows
// accepts thinking turned off. A Prizmal name is missing from its built-in list,
// so without this entry WebSearch sends tool_choice auto and the model may
// answer in prose.
func TestClaudeChildEnvMarksTheLaunchedModelAsAcceptingDisabledThinking(t *testing.T) {
	env := launchEnvWithCapabilities(t, "", "", "claude-ollama-elecnix-sdlc-china-1[1m]", nil)

	entries := capabilityEntries(envValue(env, claudeCapabilitiesVar+"="))
	want := "claude-ollama-elecnix-sdlc-china-1=-rejects_disabled_thinking"
	if !slices.Contains(entries, want) {
		t.Fatalf("%s entries = %q, want one entry %q", claudeCapabilitiesVar, entries, want)
	}
}

// Every name the launch hands Claude Code gets an entry: the subagent model,
// each /model row and each tier remap target. A session switched to another
// row, or a request resolved through a tier id, keeps the required tool choice.
func TestClaudeChildEnvMarksEveryLaunchedModelName(t *testing.T) {
	rows := []ModelRow{
		{Label: "a", Model: "claude-row-a"},
		{Label: "b", Model: "claude-row-b[1m]"},
	}
	env := launchEnvWithCapabilities(t, "cheap-model", "", "claude-row-a", rows)

	entries := capabilityEntries(envValue(env, claudeCapabilitiesVar+"="))
	wantNames := []string{"claude-row-a", "cheap-model", "claude-row-b"}
	for tier := range claudeFamilyIDs {
		wantNames = append(wantNames, claudeTierModel(tier))
	}
	for _, name := range wantNames {
		if !slices.Contains(entries, name+"=-rejects_disabled_thinking") {
			t.Errorf("no entry for %s in %q", name, entries)
		}
	}
	// Exact names only: a wildcard would reach models this launch never picked.
	for _, entry := range entries {
		pattern, _, _ := strings.Cut(entry, "=")
		if strings.HasSuffix(pattern, "*") || strings.Contains(pattern, oneMillionSuffix) {
			t.Errorf("entry %q is not an exact bare model name", entry)
		}
	}
	if len(entries) != len(wantNames) {
		t.Errorf("got %d entries, want %d, one per distinct name: %q", len(entries), len(wantNames), entries)
	}
}

// An operator's own overrides survive, and the launch entries come after them.
// Claude Code applies every matching entry in order, so the last match for a
// capability sets its value and the launch entry takes effect.
func TestClaudeChildEnvAppendsToAnInheritedCapabilitiesValue(t *testing.T) {
	env := launchEnvWithCapabilities(t, "", "my-model=effort;claude-x=rejects_disabled_thinking", "claude-x", nil)

	got := envValue(env, claudeCapabilitiesVar+"=")
	want := "my-model=effort;claude-x=rejects_disabled_thinking;claude-x=-rejects_disabled_thinking;"
	if !strings.HasPrefix(got, want) {
		t.Fatalf("%s = %q, want it to start with %q", claudeCapabilitiesVar, got, want)
	}
	count := 0
	for _, name := range envNames(env) {
		if name == claudeCapabilitiesVar {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("%s appears %d times, want once", claudeCapabilitiesVar, count)
	}
}
