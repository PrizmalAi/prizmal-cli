package launch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
)

// Claude implements Runner for Claude Code integration.
type Claude struct{}

func (c *Claude) String() string { return "Claude Code" }

// ShowsModelList reports that a launch hands Claude Code the whole catalog
// rather than the one model it runs, because Claude Code displays the tenant's
// models as its own /model rows.
//
// Runners without this method are given only the model they launch. pi and
// cline read models[0] as the model to select and write into their config, so
// widening their list would silently change what a bare launch selects.
func (c *Claude) ShowsModelList() bool { return true }

// OwnsModelFlag reports that prizmal owns Claude Code's model decision. A
// --model the operator types after the integration name is consumed by the CLI
// as prizmal's own flag: the launch states the model itself, and forwarding the
// operator's value let Claude Code's flag outrank the settings prizmal wrote,
// which is how a launch lost its 1M window and warned that the model was
// unknown.
func (c *Claude) OwnsModelFlag() bool { return true }

// args builds the command line for a Claude Code launch.
//
// The launch states the model in two places that must agree. The inline
// settings JSON is the child's configuration; the --model flag outranks it and
// replaces the whole string. Both spell the model as its own picker row does,
// with [1m] when its tier has a 1M window, so whichever one Claude Code
// resolves the model from, the session runs as that row.
//
// The flag is appended here rather than taken from the caller's arguments.
// prizmal owns the model decision, so the harness receives it as a value
// prizmal produced, never as one the operator typed. A caller-supplied --model
// is consumed by the CLI before this point.
func (c *Claude) args(model string, rows []ModelRow, settingsJSON string, extra []string) []string {
	var args []string
	if settingsJSON != "" {
		args = append(args, "--settings", settingsJSON)
	}
	if named := claudeLaunchModelName(model, rows); named != "" {
		args = append(args, "--model", named)
	}
	return append(args, extra...)
}

func (c *Claude) findPath() (string, error) {
	if p, err := exec.LookPath("claude"); err == nil {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	name := "claude"
	if runtime.GOOS == "windows" {
		name = "claude.exe"
	}
	for _, fallback := range []string{
		filepath.Join(home, ".local", "bin", name),
		filepath.Join(home, ".claude", "local", name),
	} {
		if _, err := os.Stat(fallback); err == nil {
			return fallback, nil
		}
	}
	return "", fmt.Errorf("claude binary not found")
}

func (c *Claude) Run(model string, models []LaunchModel, args []string) error {
	claudePath, err := ensureClaudeInstalled()
	if err != nil {
		return err
	}

	rows := ModelRows(models)
	settings, err := claudeSettingsJSON(model, rows)
	if err != nil {
		return err
	}

	cmd := exec.Command(claudePath, c.args(model, rows, settings, args)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	cmd.Env = claudeChildEnv(model, rows)
	return cmd.Run()
}

// envVars is the environment Claude Code is launched with, before the
// inherited variables are merged in. It carries the endpoint, the credential
// and behavior switches only. The model arrives in the inline --settings JSON,
// so nothing here names a model.
func (c *Claude) envVars() []string {
	return []string{
		// Claude Code appends /v1 to ANTHROPIC_BASE_URL itself, so the
		// endpoint goes in without it; envconfig owns that spelling for every
		// caller, and envconfig.UnversionedBaseURL explains what breaks if a
		// base configured with the suffix keeps it.
		"ANTHROPIC_BASE_URL=" + envconfig.UnversionedBaseURL(),
		// The switch key travels as ANTHROPIC_AUTH_TOKEN, Claude Code's
		// gateway credential (Authorization: Bearer), and nothing else.
		// ANTHROPIC_API_KEY is its Anthropic-console key: set alongside the
		// token it draws a "Both ... set · auth may not work as expected"
		// warning, and on its own it asks for approval before the first
		// interactive turn. It is emptied, not left alone, so a key exported
		// in the operator's shell for Anthropic itself never reaches the
		// Switch as an x-api-key header.
		"ANTHROPIC_AUTH_TOKEN=" + envconfig.APIKey(),
		"ANTHROPIC_API_KEY=",
		// Claude Code treats any non-claude.ai auth source as taking
		// precedence over a stored claude.ai login, which is what a launch
		// uses: the Switch token. Connectors can never load under a launch,
		// but the stored login is still there, and Claude Code prints a
		// startup warning that tells the operator to unset the credential this
		// CLI set. The variable is read before that precedence check, so a
		// launch that sets it never reaches the branch recording the warning.
		//
		// It gates the auto-fetch only. A server named through --mcp-config,
		// the settings mcpServers block, .mcp.json or the SDK keeps the normal
		// MCP trust flow, so this is not an MCP switch.
		"ENABLE_CLAUDEAI_MCP_SERVERS=false",
		"CLAUDE_CODE_ATTRIBUTION_HEADER=0",
		"DISABLE_ERROR_REPORTING=1",
		"DISABLE_FEEDBACK_COMMAND=1",
		"CLAUDE_CODE_DISABLE_FEEDBACK_SURVEY=1",
		// Claude Code enables tool search (tool definitions withheld from the
		// system prompt, fetched on demand through tool_reference blocks) by
		// default only when ANTHROPIC_BASE_URL names a first-party Anthropic
		// host, so routing through the Switch silently turns it off and every
		// request carries the full tool list. The Switch parses and forwards
		// tool_reference blocks, so turn it back on explicitly.
		"ENABLE_TOOL_SEARCH=true",
	}
}

// claudeInheritedModelVars are the model-selecting variables the CLI never
// passes on to the child.
//
// A launch states its model in the inline settings JSON, which outranks every
// one of these. They are removed from the inherited environment anyway, so the
// child's model comes from exactly one place: a value exported in the
// operator's shell for Anthropic itself must not ride along and re-route a
// launch the operator did not aim at Anthropic.
//
// ANTHROPIC_DEFAULT_OPUS_MODEL and CLAUDE_CODE_SUBAGENT_MODEL are in this list
// and are also the two variables the CLI sets for real, from claudeChildEnv
// below. A stale shell export must not give the child a model the operator
// never picked. The child sees only the value the launch asked for.
var claudeInheritedModelVars = []string{
	"ANTHROPIC_DEFAULT_OPUS_MODEL",
	"ANTHROPIC_DEFAULT_SONNET_MODEL",
	"ANTHROPIC_DEFAULT_HAIKU_MODEL",
	"ANTHROPIC_DEFAULT_FABLE_MODEL",
	"ANTHROPIC_MODEL",
	"ANTHROPIC_SMALL_FAST_MODEL",
	"CLAUDE_CODE_SUBAGENT_MODEL",
}

// claudeChildEnv builds Claude Code's environment explicitly: the inherited
// environment with every model-selecting variable dropped, then the fixed
// launch variables, then the model capability overrides for model and rows.
//
// The filter is what makes the inline settings JSON authoritative. The result
// is keyed by name, so the CLI's own value always wins over an inherited one
// and a variable can never appear twice.
func claudeChildEnv(model string, rows []ModelRow) []string {
	drop := make(map[string]bool, len(claudeInheritedModelVars))
	for _, name := range claudeInheritedModelVars {
		drop[name] = true
	}

	// The launch's own variables are collected first, so the inherited pass can
	// skip every name they define. A name defined twice would leave the child's
	// value depending on which copy the OS reads first, and the launch value
	// must be the one that wins.
	fixed := (&Claude{}).envVars()
	fixedNames := make(map[string]bool, len(fixed))
	for _, kv := range fixed {
		name, _, _ := strings.Cut(kv, "=")
		fixedNames[name] = true
	}

	env := make([]string, 0, len(os.Environ())+len(fixed)+1)
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if drop[name] || fixedNames[name] {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, fixed...)

	// Claude Code builds the /model picker's Default row from the Opus tier,
	// and ANTHROPIC_DEFAULT_OPUS_MODEL is the first place it reads that tier
	// from. The settings JSON has no field for the row's text. Unset, the row
	// shows the Opus model from Claude Code's own catalog, whatever the launch
	// routes to. Setting it to the pinned model makes the row show that model.
	// It also makes the "opus" alias resolve to the pinned model, the one
	// target this launch routes to.
	if model != "" {
		env = append(env, "ANTHROPIC_DEFAULT_OPUS_MODEL="+claudeLaunchModelName(model, rows))
	}

	// The subagent model variable, set only when a dedicated
	// subagent model was picked. Subagents run the model it names; omitting it
	// leaves them on the session model, which is Claude Code's "inherit"
	// default. There is no settings-JSON equivalent, so this variable is the
	// only channel that gives subagents a different model than the parent.
	if sub := selectedSubagentModel(); sub != "" {
		env = append(env, "CLAUDE_CODE_SUBAGENT_MODEL="+claudeModelName(sub))
	}

	return env
}

// oneMillionSuffix is Claude Code's own context-budgeting instruction. It is
// not part of any model id: Claude Code strips the suffix from the name before
// it sends the request and budgets a 1M context window for that name.
const oneMillionSuffix = "[1m]"

// claudeModelName returns the model name to hand Claude Code: the switch's own
// name with exactly one [1m] suffix, or none for a haiku name, whatever the
// switch sent. Every trailing suffix is stripped first, so the result does not
// depend on whether the id arrived bare, suffixed, or doubly suffixed.
//
// The suffix is a client-side budgeting instruction. Claude Code strips it
// before building the request, so the wire carries the bare name and the
// switch routes it as usual. Without it Claude Code assumes a 200k window and
// compacts a long session early.
//
// A name whose tier has no 1M window, which is haiku, gets no suffix. Claude
// Code drops a /model row that asks for 1M on such a model, and budgets the
// tier's own window without the suffix.
func claudeModelName(model string) string {
	if model == "" {
		return ""
	}
	// Strip every trailing suffix, not just one: the switch's own ids are
	// already bare, but a value that reached here through a config file or a
	// shell export may carry one, and suffixes must not accumulate.
	bare := model
	for {
		stripped := strings.TrimSuffix(bare, oneMillionSuffix)
		if stripped == bare {
			break
		}
		bare = stripped
	}
	if tier, ok := inferTier(bare); ok && !tierProfiles[tier].oneMillion {
		return bare
	}
	return bare + oneMillionSuffix
}

// claudeRowModelName is claudeModelName for a picker row, whose tier may come
// from the Switch rather than from its name. A row's tier decides its window.
func claudeRowModelName(row ModelRow) string {
	if row.tier == "" {
		return claudeModelName(row.Model)
	}
	bare := strings.TrimSuffix(claudeModelName(row.Model), oneMillionSuffix)
	if tierProfiles[row.tier].oneMillion {
		return bare + oneMillionSuffix
	}
	return bare
}

// claudeLaunchModelName spells the launched model the way its own picker row
// does, so the session model is one of the rows and runs with that row's
// window. A model with no row is spelled from its name.
func claudeLaunchModelName(model string, rows []ModelRow) string {
	bare := strings.TrimSuffix(claudeModelName(model), oneMillionSuffix)
	for _, row := range rows {
		if row.Model == bare {
			return claudeRowModelName(row)
		}
	}
	return claudeModelName(model)
}

// claudeSettingsJSON builds the inline --settings JSON for a launch: the model
// to run, the tier remaps, and the picker rows.
//
// It is one argument on the command line, so it is scoped to the launched
// process and persists nowhere. Nothing is written to ~/.claude.
func claudeSettingsJSON(model string, rows []ModelRow) (string, error) {
	if model == "" {
		return "", nil
	}

	settings := map[string]any{"model": claudeLaunchModelName(model, rows)}
	if overrides := claudeOrderedModelOverrides(); len(overrides) > 0 {
		settings["modelOverrides"] = overrides
	}
	if len(rows) > 0 {
		settings["modelPicker"] = claudeModelPicker(rows)
	}

	data, err := json.Marshal(settings)
	if err != nil {
		return "", fmt.Errorf("build Claude Code settings: %w", err)
	}
	return string(data), nil
}

// claudeModelOverrides maps Claude Code's own model ids to the tenant's names
// for them.
//
// Claude Code resolves a tier word (sonnet, opus, haiku, fable) to a baked-in
// catalog id first, and a session or request can also name any catalog id
// directly, then applies this mapping to whatever id it resolved. So the keys
// are Claude Code's own ids and not the tier words. A tenant that has not
// provisioned a tier alias simply has no entry: the mapping never invents a
// name, and a launch that never names a tier is unaffected by it.
//
// The keys cover the whole first-party lineage the catalog knows, not just the
// current generation. A request that names an older model (a subagent pinned to
// an agent's claude-sonnet-4-6, a settings model field, a picker row) resolves
// to that older id and must still route to the tier the tenant chose. Without
// the entry it would reach the Switch as the older id, and the Switch would
// route it by whatever alias or fallback that id matches.
//
// Three ids are deliberately absent. The Bedrock variant strings
// claude-haiku-4-5-20251001-v1 and claude-fable-5-mythos-5 are not first-party
// ids in the catalog, and the mythos family (claude-mythos-5, claude-mythos-5-1)
// has no tier alias. An override key that is not a first-party id the running
// build knows is silently dropped, verified on the wire: a keyed entry for any
// of these passes through as the id itself. Adding them would be the silent
// no-op this mapping exists to avoid.
//
// Each key is the dated snapshot id where the catalog carries one
// (claude-haiku-4-5-20251001, claude-sonnet-4-5-20250929, claude-opus-4-20250514,
// claude-opus-4-1-20250805, claude-opus-4-5-20251101). The undated
// claude-haiku-4-5 is a catalog name, not a first-party id, and an override
// keyed on it is silently ignored. The valid keys are release-fragile by
// construction: any model that rolls a named snapshot acquires a dated
// first-party id, and yesterday's undated key stops matching while the tier
// word still works. The pinned-key test below documents the working set.
//
// Each value is spelled by claudeModelName, like the pinned model and the
// picker rows. A request that resolves through a tier short name takes this
// value as its model, so a value without the [1m] suffix would budget a 200k
// window and compact a long session early. The haiku value is bare because
// Haiku 4.5 has no 1M window.
//
// The newest ids (claude-opus-5-5, claude-sonnet-5-5) are ones only recent
// Claude Code releases know. An older release drops those keys, and it never
// resolves a tier to them either.
func claudeModelOverrides() map[string]string {
	overrides := make(map[string]string)
	for tier, ids := range claudeFamilyIDs {
		for _, id := range ids {
			overrides[id] = claudeModelName(claudeTierModel(tier))
		}
	}
	return overrides
}

// modelOverride is one modelOverrides entry.
type modelOverride struct{ key, value string }

// orderedModelOverrides is the modelOverrides object with its key order kept.
//
// The order is part of the contract. Claude Code identifies a model whose
// name equals an override value as the first key, in the object's order, that
// maps to that value and that the running build knows. Every key of one tier
// maps to the same alias, so the first key decides which model's profile a
// session on the alias runs. encoding/json sorts map keys, which puts the
// oldest id first: the haiku alias would run as the retired Claude 3.5 Haiku.
type orderedModelOverrides []modelOverride

func (o orderedModelOverrides) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, entry := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		key, err := json.Marshal(entry.key)
		if err != nil {
			return nil, err
		}
		value, err := json.Marshal(entry.value)
		if err != nil {
			return nil, err
		}
		b.Write(key)
		b.WriteByte(':')
		b.Write(value)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// claudeOrderedModelOverrides is claudeModelOverrides in the order Claude
// Code reads it: tier by tier, and within a tier the id its rows behave as
// first, then the rest of the lineage. A build that does not know the first
// id skips it and takes the next.
func claudeOrderedModelOverrides() orderedModelOverrides {
	overrides := claudeModelOverrides()
	ordered := make(orderedModelOverrides, 0, len(overrides))
	for _, tier := range tierWords {
		profile := tierProfiles[tier].behavesAs
		ids := append([]string{profile}, slices.DeleteFunc(slices.Clone(claudeFamilyIDs[tier]), func(id string) bool {
			return id == profile
		})...)
		for _, id := range ids {
			ordered = append(ordered, modelOverride{key: id, value: overrides[id]})
		}
	}
	return ordered
}

// claudeFamilyIDs maps each Claude Code model family to the catalog ids a
// request can resolve to for that family, newest first. The ids are the provider_ids
// first_party strings from the installed Claude Code binary catalog.
//
// The mythos family is absent: it has no tier alias (a request for
// claude-mythos-5 or -5-1 passes through unmapped, reaching the Switch as the
// id itself), so no entry belongs here.
var claudeFamilyIDs = map[modelTier][]string{
	modelTierOpus: {
		"claude-opus-5-5",
		"claude-opus-5",
		"claude-opus-4-8",
		"claude-opus-4-7",
		"claude-opus-4-6",
		"claude-opus-4-5-20251101",
		"claude-opus-4-20250514",
		"claude-opus-4-1-20250805",
	},
	modelTierSonnet: {
		"claude-sonnet-5-5",
		"claude-sonnet-5",
		"claude-sonnet-4-6",
		"claude-sonnet-4-5-20250929",
		"claude-sonnet-4-20250514",
		"claude-3-7-sonnet-20250219",
		"claude-3-5-sonnet-20241022",
	},
	modelTierHaiku: {
		"claude-haiku-4-5-20251001",
		"claude-3-5-haiku-20241022",
	},
	modelTierFable: {
		"claude-fable-5-1",
		"claude-fable-5",
	},
}

// claudeTierModel is the model a tier runs, as the tenant named it.
func claudeTierModel(tier modelTier) string {
	return claudeModelPrefix + "tier-" + string(tier)
}

// claudeModelPicker is the settings block that defines Claude Code's /model
// rows.
//
// replaceBuiltInOptions hides the built-in lineup and the gateway-discovered
// rows, so the rows defined here are the only rows and a discovered duplicate
// cannot appear beside them.
//
// Each row's model is the id that routes. Its behavesAs picks the client-side
// profile Claude Code runs the model with, and its description is the row's
// text. Routing is decided by the model id the row sends to the switch, never
// by either field.
func claudeModelPicker(rows []ModelRow) map[string]any {
	options := make([]any, 0, len(rows))
	for _, row := range rows {
		option := map[string]any{
			"label": row.Label,
			"model": claudeRowModelName(row),
		}
		if row.BehavesAs != "" {
			option["behavesAs"] = row.BehavesAs
		}
		if row.Description != "" {
			option["description"] = row.Description
		}
		options = append(options, option)
	}
	return map[string]any{
		"replaceBuiltInOptions": true,
		"options":               options,
	}
}

func ensureClaudeInstalled() (string, error) {
	if path, err := (&Claude{}).findPath(); err == nil {
		return path, nil
	}

	if err := checkClaudeInstallerDependencies(); err != nil {
		return "", err
	}

	ok, err := ConfirmPrompt("Claude Code is not installed. Install now?")
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("claude installation cancelled")
	}

	bin, args, err := claudeInstallerCommand(runtime.GOOS)
	if err != nil {
		return "", err
	}

	fmt.Fprintf(os.Stderr, "\nInstalling Claude Code...\n")
	cmd := exec.Command(bin, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to install claude: %w", err)
	}

	path, err := (&Claude{}).findPath()
	if err != nil {
		return "", fmt.Errorf("claude was installed but the binary was not found on PATH\n\nYou may need to restart your shell")
	}

	fmt.Fprintf(os.Stderr, "%sClaude Code installed successfully%s\n\n", ansiGreen, ansiReset)
	return path, nil
}

func checkClaudeInstallerDependencies() error {
	switch runtime.GOOS {
	case "windows":
		if _, err := exec.LookPath("powershell"); err != nil {
			return fmt.Errorf("claude is not installed and required dependencies are missing\n\nInstall the following first:\n  PowerShell: https://learn.microsoft.com/powershell/\n\nThen re-run:\n  prizmal claude")
		}
	default:
		var missing []string
		if _, err := exec.LookPath("curl"); err != nil {
			missing = append(missing, "curl: https://curl.se/")
		}
		if _, err := exec.LookPath("bash"); err != nil {
			missing = append(missing, "bash: https://www.gnu.org/software/bash/")
		}
		if len(missing) > 0 {
			return fmt.Errorf("claude is not installed and required dependencies are missing\n\nInstall the following first:\n  %s\n\nThen re-run:\n  prizmal claude", strings.Join(missing, "\n  "))
		}
	}
	return nil
}

func claudeInstallerCommand(goos string) (string, []string, error) {
	switch goos {
	case "windows":
		return "powershell", []string{
			"-NoProfile",
			"-ExecutionPolicy",
			"Bypass",
			"-Command",
			"irm https://claude.ai/install.ps1 | iex",
		}, nil
	case "darwin", "linux":
		return "bash", []string{
			"-c",
			"curl -fsSL https://claude.ai/install.sh | bash",
		}, nil
	default:
		return "", nil, fmt.Errorf("unsupported platform for claude install: %s", goos)
	}
}

// modelEnvVars is gone. The inline settings JSON's modelOverrides maps the
// tier ids to the tenant's names, and its top-level model pins the launch.
// claudeChildEnv sets two model variables: ANTHROPIC_DEFAULT_OPUS_MODEL, so
// the /model picker's Default row shows the pinned model, and
// CLAUDE_CODE_SUBAGENT_MODEL, when a subagent model was picked.
