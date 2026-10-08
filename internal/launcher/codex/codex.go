package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/fileutil"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/claude"
	"github.com/pelletier/go-toml/v2"
	"golang.org/x/mod/semver"
)

// Codex implements launch.Runner for Codex integration
type Codex struct{}

func (c *Codex) String() string { return "Codex" }

// codexSetting is one provider setting, rendered by both the -c overrides the
// launch passes and the profile file it writes, so the two cannot disagree.
type codexSetting struct {
	key   string
	value string
}

// SupportsDeviceMode reports that Codex can run from an enrolled device: a
// provider's command-backed auth re-runs a command for the bearer token every
// refresh interval and again after a 401, so the launcher can hand it
// `prizmal auth token` instead of a fixed key.
func (c *Codex) SupportsDeviceMode() bool { return true }

const (
	codexProfileName    = "prizmal"
	codexProviderName   = "prizmal"
	codexRestoreSuccess = "Codex launch configuration removed."

	// codexFallbackContextWindow is the context window every Codex catalog
	// entry declares, and it is the 1M window every model the Switch serves
	// has today, stated rather than learned. GET /v1/models carries no context length (the
	// Switch sends id, input_modalities, tier and description), so nothing here
	// can learn the real one, and no model name is mapped to a window because
	// such a table would be guesswork about an endpoint prizmal knows nothing
	// else about.
	//
	// Codex derives auto-compaction from this number — 90% of context_window
	// unless the entry says otherwise (ModelInfo::auto_compact_token_limit in
	// codex-rs/protocol) — so declaring too small a window makes Codex discard
	// conversation history the Switch would have accepted. Revisit this when the
	// Switch serves a model with a smaller window. $HARNESS_CONTEXT_LENGTH
	// overrides it for an operator who knows better.
	codexFallbackContextWindow = 1_000_000

	// codexRefreshIntervalMs is how long Codex keeps a token from the auth
	// command before running it again. It is the Claude Code interval for the
	// same reason: the device token lives 10 minutes, and a refresh every 4
	// leaves at least 6 on the token in hand. Codex's own default of 5
	// minutes would refresh with only 5 left.
	codexRefreshIntervalMs = claude.ClaudeHelperTTLMs

	codexRootProfileKey          = "profile"
	codexRootModelKey            = "model"
	codexRootModelProviderKey    = "model_provider"
	codexRootModelCatalogJSONKey = "model_catalog_json"
)

func (c *Codex) args(model, modelCatalogPath string, extra []string) ([]string, error) {
	if err := codexValidateExtraArgs(extra); err != nil {
		return nil, err
	}

	overrides, err := codexManagedConfigOverrides(modelCatalogPath)
	if err != nil {
		return nil, err
	}

	// Codex falls back to embedded mode for every -c override and warns about
	// it in its footer. --no-daemon makes that choice explicit.
	args := []string{"--no-daemon"}
	for _, override := range overrides {
		args = append(args, "-c", override)
	}
	for _, override := range codexHygieneOverrides() {
		args = append(args, "-c", override)
	}
	if model != "" {
		args = append(args, "-m", model)
	}
	args = append(args, extra...)
	return args, nil
}

func (c *Codex) Installed() bool {
	_, err := exec.LookPath("codex")
	return err == nil
}

func (c *Codex) Run(model string, models []launch.LaunchModel, args []string) error {
	codexPath, err := CodexInstaller.EnsureInstalled()
	if err != nil {
		return err
	}
	if err := checkCodexVersion(); err != nil {
		return err
	}

	// The catalog is the one thing Codex needs as a file. It goes in a
	// directory of its own that is removed when the launch returns, so a plain
	// launch leaves nothing under ~/.codex. Everything else rides -c overrides.
	catalogDir, err := os.MkdirTemp("", "prizmal-codex-")
	if err != nil {
		return fmt.Errorf("failed to configure codex: %w", err)
	}
	defer func() { _ = os.RemoveAll(catalogDir) }()
	catalogPath := filepath.Join(catalogDir, "model.json")
	if err := writeCodexModelCatalog(catalogPath, codexCatalogModels(model, models)); err != nil {
		return fmt.Errorf("failed to configure codex: %w", err)
	}

	codexArgs, err := c.args(model, catalogPath, args)
	if err != nil {
		return fmt.Errorf("failed to configure codex: %w", err)
	}

	cmd := exec.Command(codexPath, codexArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = codexChildEnv()
	return cmd.Run()
}

// envVars returns the child environment that carries the provider credential.
// This is the only channel the key travels on: the generated profile names
// OPENAI_API_KEY via env_key rather than embedding the key, so nothing
// credential-shaped outlives the launched process.
//
// In device mode it carries nothing: the profile names Codex's command-backed
// provider auth instead, and Codex runs `prizmal auth token` for its bearer
// token rather than reading a fixed key from the environment.
func (c *Codex) envVars() []string {
	if envconfig.DeviceMode() {
		return nil
	}
	return []string{"OPENAI_API_KEY=" + envconfig.APIKey()}
}

// codexExecutable is os.Executable behind a variable, so a test can exercise
// the launch that cannot resolve its own path.
var codexExecutable = os.Executable

// codexAuthCommand is the command Codex runs for a device token: this binary,
// and the arguments that select its helper subcommand. Codex executes it
// directly, not through a shell, so the path needs no quoting.
func codexAuthCommand() (string, []string, error) {
	exe, err := codexExecutable()
	if err != nil {
		return "", nil, fmt.Errorf("find the prizmal executable: %w", err)
	}
	return exe, []string{"auth", "token"}, nil
}

// codexAuthArgsTOML spells the helper arguments as a TOML array.
func codexAuthArgsTOML(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = strconv.Quote(a)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// codexInheritedVars are the variables the CLI never passes on to Codex.
//
// Each one would send the session somewhere other than the Switch, or sign it
// in as someone other than the Switch key: CODEX_API_KEY and CODEX_ACCESS_TOKEN
// override the credential Codex resolves, OPENAI_BASE_URL names another
// endpoint for the tools that read it, and CODEX_HOME moves Codex to a
// configuration directory that holds none of the files a launch writes.
var codexInheritedVars = []string{
	"OPENAI_BASE_URL",
	"CODEX_API_KEY",
	"CODEX_ACCESS_TOKEN",
	"CODEX_HOME",
}

// codexChildEnv returns the environment Codex is launched with: the inherited
// environment minus codexInheritedVars and minus OPENAI_API_KEY, then the
// variables the launch sets. A launch's own name is skipped in the inherited
// pass, so each appears once and the launch value wins.
//
// In device mode the environment carries no credential at all. Codex runs
// `prizmal auth token` for its bearer token, so an OPENAI_API_KEY the operator
// exported for OpenAI itself is removed rather than passed on, and the Switch
// URL this launch resolved is pinned for the helper, which resolves its own
// (see ensureHelperBaseURL).
func codexChildEnv() []string {
	drop := make(map[string]bool, len(codexInheritedVars))
	for _, name := range codexInheritedVars {
		drop[name] = true
	}
	// An OPENAI_API_KEY from the operator's shell never reaches Codex: with a
	// switch key the launch sets its own, and in device mode there is none.
	drop["OPENAI_API_KEY"] = true
	fixed := (&Codex{}).envVars()
	for _, kv := range fixed {
		name, _, _ := strings.Cut(kv, "=")
		drop[name] = true
	}

	env := make([]string, 0, len(os.Environ())+len(fixed))
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if drop[name] {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, fixed...)
	// The helper is a separate prizmal process that resolves the Switch URL from
	// scratch, so this launch's resolved host is pinned for it.
	if envconfig.DeviceMode() {
		env = claude.EnsureHelperBaseURL(env, envconfig.BaseURL())
	}
	return env
}

// codexHygieneOverrides are the -c settings that keep a launch quiet: Codex
// analytics and the feedback upload are off, and --subagent-model, when given,
// names the model for both slots Codex reads for work it delegates.
// agents.default_subagent_model is the model a spawned subagent runs when the
// spawn call names none, which is the slot the flag's own name means;
// review_model is the model /review runs. They travel on the command line, so
// nothing is written to disk.
func codexHygieneOverrides() []string {
	overrides := []string{
		"analytics.enabled=false",
		"feedback.enabled=false",
	}
	if sub := launch.SelectedSubagentModel(); sub != "" {
		overrides = append(overrides,
			fmt.Sprintf("agents.default_subagent_model=%q", sub),
			fmt.Sprintf("review_model=%q", sub),
		)
	}
	return overrides
}

// codexInstaller is Codex's install story. It is the plain one, like Cline's:
// npm puts the binary on PATH when it works, so there is no fallback directory
// to read, and Node.js is the only thing the installer needs on every
// platform.
var CodexInstaller = launch.Installer{
	Name:         "codex",
	DisplayName:  "Codex",
	Prompt:       "Codex is not installed. Install with npm?",
	Locate:       locateCodex,
	Dependencies: codexInstallerDependencies,
	Command:      codexInstallerCommand,
}

// locateCodex returns the codex binary when it is on PATH. It returns the bare
// command name rather than the path LookPath resolved, which is what a codex
// launch has always exec'd.
func locateCodex(string) (string, error) {
	if _, err := exec.LookPath("codex"); err != nil {
		return "", launch.ErrHarnessAbsent
	}
	return "codex", nil
}

// codexInstallerDependencies lists what Codex's installer needs: npm installs
// Codex on every platform alike, so Node.js is the whole list.
func codexInstallerDependencies(string) []launch.Dependency {
	return []launch.Dependency{launch.NpmDependency}
}

// codexInstallerCommand is the npm install Codex ships, on every platform.
func codexInstallerCommand(string) (string, []string, error) {
	return "npm", []string{"install", "-g", "@openai/codex"}, nil
}

// EnsureCodexInstalled installs Codex when it is missing, after the operator
// confirms.
func EnsureCodexInstalled() error {
	_, err := CodexInstaller.EnsureInstalled()
	return err
}

func (c *Codex) Restore() (launch.RestoreOutcome, error) {
	configPath, err := codexConfigPath()
	if err != nil {
		return launch.RestoreOutcome{}, err
	}

	profileRemoved, err := removeCodexProfileConfig()
	if err != nil {
		return launch.RestoreOutcome{}, codexRestoreFailure(configPath, err)
	}
	catalogRemoved, err := removeCodexModelCatalogIfUnused(configPath)
	if err != nil {
		return launch.RestoreOutcome{}, codexRestoreFailure(configPath, err)
	}
	return launch.RestoreOutcome{Removed: profileRemoved || catalogRemoved}, nil
}

func (c *Codex) RestoreSuccessMessage() string {
	return codexRestoreSuccess
}

func codexRestoreFailure(configPath string, err error) error {
	return fmt.Errorf("restore Codex config: %w\n\nRestore did not complete. Check these files before retrying:\n  Codex config: %s\n  CLI profile: %s\n  CLI model catalog: %s\n  Backups: %s",
		err,
		configPath,
		codexProfileConfigPathForConfig(configPath),
		codexModelCatalogPathForConfig(configPath),
		fileutil.BackupDir(),
	)
}

func removeCodexProfileConfig() (bool, error) {
	profilePath, err := codexProfileConfigPath()
	if err != nil {
		return false, err
	}
	return removeCodexFile(profilePath)
}

func removeCodexModelCatalogIfUnused(configPath string) (bool, error) {
	catalogPath := codexModelCatalogPathForConfig(configPath)
	data, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if err == nil {
		config, parseErr := codexParseConfig(string(data))
		if parseErr != nil {
			return false, parseErr
		}
		if config.RootString(codexRootModelCatalogJSONKey) == catalogPath {
			return false, nil
		}
	}
	return removeCodexFile(catalogPath)
}

// removeCodexFile deletes path and reports whether there was one to delete.
func removeCodexFile(path string) (bool, error) {
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func codexValidateExtraArgs(args []string) error {
	for i, arg := range args {
		switch {
		case arg == "-m", strings.HasPrefix(arg, "-m"):
			return fmt.Errorf("conflicting extra argument %q: prizmal codex manages --model", arg)
		case arg == "--model", strings.HasPrefix(arg, "--model="):
			return fmt.Errorf("conflicting extra argument %q: prizmal codex manages --model", arg)
		case arg == "-c", arg == "--config":
			if i+1 < len(args) && codexConfigOverrideConflicts(args[i+1]) {
				return fmt.Errorf("conflicting extra config %q: prizmal codex manages provider and model catalog config", args[i+1])
			}
		case strings.HasPrefix(arg, "-c") && len(arg) > len("-c"):
			if codexConfigOverrideConflicts(strings.TrimPrefix(arg, "-c")) {
				return fmt.Errorf("conflicting extra config %q: prizmal codex manages provider and model catalog config", arg)
			}
		case strings.HasPrefix(arg, "--config="):
			if codexConfigOverrideConflicts(strings.TrimPrefix(arg, "--config=")) {
				return fmt.Errorf("conflicting extra config %q: prizmal codex manages provider and model catalog config", arg)
			}
		}
	}
	return nil
}

// codexProviderSettings returns the provider's credential settings: the ones
// that depend on how the launch authenticates, and the only ones whose value
// can fail to resolve. The -c overrides and the profile file both render these
// pairs, so they are built in one place.
func codexProviderSettings() ([]codexSetting, error) {
	if !envconfig.DeviceMode() {
		return []codexSetting{{"env_key", strconv.Quote("OPENAI_API_KEY")}}, nil
	}
	// Codex rejects a provider that sets both env_key and a command, so
	// device mode names the command and leaves env_key out.
	command, args, err := codexAuthCommand()
	if err != nil {
		return nil, err
	}
	return []codexSetting{
		{"auth.command", strconv.Quote(command)},
		{"auth.args", codexAuthArgsTOML(args)},
		{"auth.refresh_interval_ms", strconv.Itoa(codexRefreshIntervalMs)},
	}, nil
}

func codexManagedConfigOverrides(modelCatalogPath string) ([]string, error) {
	settings, err := codexProviderSettings()
	if err != nil {
		return nil, err
	}
	overrides := []string{
		fmt.Sprintf("%s=%q", codexRootModelProviderKey, codexProfileName),
		fmt.Sprintf("model_providers.%s.name=%q", codexProfileName, codexProviderName),
		fmt.Sprintf("model_providers.%s.base_url=%q", codexProfileName, codexBaseURL()),
		fmt.Sprintf("model_providers.%s.wire_api=%q", codexProfileName, "responses"),
	}
	for _, setting := range settings {
		overrides = append(overrides, "model_providers."+codexProfileName+"."+setting.key+"="+setting.value)
	}
	if modelCatalogPath != "" {
		overrides = append(overrides, fmt.Sprintf("%s=%q", codexRootModelCatalogJSONKey, modelCatalogPath))
	}
	return overrides, nil
}

func codexConfigOverrideConflicts(value string) bool {
	key, _, ok := strings.Cut(strings.TrimSpace(value), "=")
	if !ok {
		return false
	}
	key = strings.TrimSpace(key)
	key = strings.Trim(key, `"'`)
	switch {
	case key == codexRootProfileKey,
		key == codexRootModelKey,
		key == codexRootModelProviderKey,
		key == codexRootModelCatalogJSONKey:
		return true
	case strings.HasPrefix(key, "model_providers."):
		return true
	}
	return false
}

func codexConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex", "config.toml"), nil
}

func codexModelCatalogPathForConfig(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "model.json")
}

func codexProfileConfigPath() (string, error) {
	configPath, err := codexConfigPath()
	if err != nil {
		return "", err
	}
	return codexProfileConfigPathForConfig(configPath), nil
}

func codexProfileConfigPathForConfig(configPath string) string {
	return codexNamedProfileConfigPathForConfig(configPath, codexProfileName)
}

func codexNamedProfileConfigPathForConfig(configPath, profileName string) string {
	return filepath.Join(filepath.Dir(configPath), profileName+".config.toml")
}

func codexBaseURL() string {
	return envconfig.OpenAIBaseURL(true)
}

type codexParsedConfig struct {
	values map[string]any
}

func (c codexParsedConfig) String(path ...string) (string, bool) {
	if len(path) == 0 {
		return "", false
	}
	var current any = c.values
	for _, part := range path {
		table, ok := current.(map[string]any)
		if !ok {
			return "", false
		}
		current, ok = table[part]
		if !ok {
			return "", false
		}
	}
	value, ok := current.(string)
	if !ok {
		return "", false
	}
	return value, true
}

func (c codexParsedConfig) Exists(path ...string) bool {
	if len(path) == 0 {
		return false
	}
	var current any = c.values
	for _, part := range path {
		table, ok := current.(map[string]any)
		if !ok {
			return false
		}
		current, ok = table[part]
		if !ok {
			return false
		}
	}
	return true
}

func (c codexParsedConfig) RootString(key string) string {
	value, _ := c.RootStringOK(key)
	return value
}

func (c codexParsedConfig) RootStringOK(key string) (string, bool) {
	return c.String(key)
}

func (c codexParsedConfig) ProfileString(profileName, key string) string {
	value, _ := c.String("profiles", profileName, key)
	return value
}

func (c codexParsedConfig) ProviderString(profileName, key string) string {
	value, _ := c.String("model_providers", profileName, key)
	return value
}

func codexParseConfig(text string) (codexParsedConfig, error) {
	values, err := codexParseConfigText(text)
	if err != nil {
		return codexParsedConfig{}, err
	}
	return codexParsedConfig{values: values}, nil
}

func codexParseConfigText(text string) (map[string]any, error) {
	cfg := map[string]any{}
	if strings.TrimSpace(text) == "" {
		return cfg, nil
	}
	if err := toml.Unmarshal([]byte(text), &cfg); err != nil {
		return nil, fmt.Errorf("invalid Codex config TOML: %w", err)
	}
	return cfg, nil
}

func codexCatalogModel(modelName string, models []launch.LaunchModel) launch.LaunchModel {
	if model, ok := launch.FindCatalogModel(models, modelName); ok {
		model.Name = modelName
		return model
	}
	return launch.FallbackLaunchModel(modelName)
}

// codexCatalogModels returns the models Codex's /model picker lists: the rows
// the prizmal picker lists, in its order, each with the row's text. A tier alias
// is a row and the router config folded into it is not, so the two pickers show
// one list.
//
// The launched model gets an entry even when no row shows it, such as -m naming
// a config a tier row folds away. Codex reads that entry for the model's
// window and prompt.
func codexCatalogModels(modelName string, models []launch.LaunchModel) []launch.LaunchModel {
	rows := launch.ModelRows(models)
	out := make([]launch.LaunchModel, 0, len(rows)+1)
	launched := false
	for _, row := range rows {
		entry := codexCatalogModel(row.Model, models)
		entry.Description = row.Description
		entry.FoldedInto = ""
		out = append(out, entry)
		launched = launched || launch.ModelNamesSame(row.Model, modelName)
	}
	if !launched {
		entry := codexCatalogModel(modelName, models)
		entry.FoldedInto = ""
		out = append(out, entry)
	}
	return out
}

// ShowsModelList reports that a launch hands Codex the whole catalog, because
// Codex lists the catalog's entries in its own /model picker.
func (c *Codex) ShowsModelList() bool { return true }

// codexReasoningLevels are the efforts a Codex entry offers. The Switch
// accepts a reasoning effort on every route, so each model gets the picker.
var codexReasoningLevels = []any{
	map[string]any{"effort": "low", "description": "Fast responses with lighter reasoning"},
	map[string]any{"effort": "medium", "description": "Balances speed and reasoning depth for everyday tasks"},
	map[string]any{"effort": "high", "description": "Greater reasoning depth for complex problems"},
}

// writeCodexModelCatalog writes one entry per model. The first model is the
// launch's own: it gets the lowest priority number, which Codex sorts first
// and offers as the default.
func writeCodexModelCatalog(catalogPath string, models []launch.LaunchModel) error {
	prompt, err := readCodexSystemPrompt()
	if err != nil {
		return err
	}
	labels := make(map[string]string, len(models))
	for _, row := range launch.ModelRows(models) {
		labels[row.Model] = row.Label
	}
	entries := make([]any, 0, len(models))
	for i, model := range models {
		entry := buildCodexModelEntry(model)
		if label := labels[model.Name]; label != "" {
			entry["display_name"] = label
		}
		entry["priority"] = i
		entry["base_instructions"] = prompt.baseInstructions
		if len(prompt.modelMessages) > 0 {
			entry["model_messages"] = prompt.modelMessages
		}
		entries = append(entries, entry)
	}

	catalog := map[string]any{"models": entries}

	data, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(catalogPath, data, 0o644)
}

// codexSystemPrompt is the prompt Codex ships for a model: the legacy
// base_instructions text and the model_messages object that holds the same
// template with its sections.
type codexSystemPrompt struct {
	baseInstructions string
	modelMessages    json.RawMessage
}

// readCodexSystemPrompt reads the prompt of the installed Codex's default
// model, the listed model with the lowest priority number. It asks the binary
// at launch, so the prompt always matches the installed version and the
// repository carries no copy of it. A launch without a prompt would send
// Codex's requests with empty instructions, so an unreadable prompt is an
// error.
func readCodexSystemPrompt() (codexSystemPrompt, error) {
	out, err := exec.Command("codex", "debug", "models", "--bundled").Output()
	if err != nil {
		return codexSystemPrompt{}, fmt.Errorf("could not read Codex's system prompt with `codex debug models --bundled`: %w", err)
	}
	var bundle struct {
		Models []struct {
			Visibility       string          `json:"visibility"`
			Priority         int             `json:"priority"`
			BaseInstructions string          `json:"base_instructions"`
			ModelMessages    json.RawMessage `json:"model_messages"`
		} `json:"models"`
	}
	if err := json.Unmarshal(out, &bundle); err != nil {
		return codexSystemPrompt{}, fmt.Errorf("could not parse Codex's bundled catalog: %w", err)
	}
	best := -1
	for i, m := range bundle.Models {
		if m.Visibility != "list" || m.BaseInstructions == "" {
			continue
		}
		if best < 0 || m.Priority < bundle.Models[best].Priority {
			best = i
		}
	}
	if best < 0 {
		return codexSystemPrompt{}, errors.New("the catalog bundled with Codex has no listed model with a system prompt")
	}
	m := bundle.Models[best]
	return codexSystemPrompt{baseInstructions: m.BaseInstructions, modelMessages: m.ModelMessages}, nil
}

// buildCodexModelEntry renders one entry of codex's model catalog.
//
// Two of its values are constants rather than facts prizmal reads from the
// Switch, and both used to look derived: the context window fell back to
// codexFallbackContextWindow (overridable by $HARNESS_CONTEXT_LENGTH, the only
// window input the tree has) and the truncation policy was always byte
// truncation, because no catalog entry names a server-side token budget to
// truncate against.

func buildCodexModelEntry(launchModel launch.LaunchModel) map[string]any {
	modelName := launchModel.Name

	// The declared window is the fallback unless the operator states the real
	// one. Nothing else in the tree carries a window: the catalog sends id,
	// input_modalities, tier and description and no context length at all, so a
	// a LaunchModel has no window field to read and every model the Switch serves
	// is declared with this number.
	contextWindow := codexFallbackContextWindow
	if ctxLen := envconfig.ContextLength(); ctxLen > 0 {
		contextWindow = ctxLen
	}

	// The declared input modalities come from the capability vocabulary, so
	// codex learns "image" from the Switch's "image" modality instead of
	// carrying its own list.
	modalities := launch.DeclaredInputs(launchModel, launch.HarnessCodex)

	return map[string]any{
		"slug":                    modelName,
		"display_name":            modelName,
		"description":             launchModel.Description,
		"default_reasoning_level": "medium",
		"context_window":          contextWindow,
		"shell_type":              "default",
		"visibility":              "list",
		"supported_in_api":        true,
		"priority":                0,
		// Truncation in tokens, as Codex's own catalog uses for every model it
		// ships: codex-rs reads a bytes-mode limit as bytes (a token limit is
		// converted with approx_bytes_for_tokens only when a user configures
		// one), so "bytes" would cut tool output at 10000 bytes, four times
		// under the 10000 tokens every entry in Codex's models_cache.json
		// declares.
		"truncation_policy": map[string]any{"mode": "tokens", "limit": 10000},
		"input_modalities":  modalities,
		// Codex reads a present base_instructions as the model's whole
		// instruction template and accepts an empty one, so an empty string
		// here is what a session runs with, not an omitted default. It cannot
		// be left out either: the decoder rejects a model carrying neither
		// base_instructions nor model_messages.instructions_template.
		"base_instructions":            "",
		"support_verbosity":            true,
		"default_verbosity":            "low",
		"supported_reasoning_levels":   codexReasoningLevels,
		"experimental_supported_tools": []any{},
	}
}

func checkCodexVersion() error {
	if _, err := exec.LookPath("codex"); err != nil {
		return fmt.Errorf("codex is not installed, install with: npm install -g @openai/codex")
	}

	out, err := exec.Command("codex", "--version").Output()
	if err != nil {
		return fmt.Errorf("failed to get codex version: %w", err)
	}

	// Parse output like "codex-cli 0.87.0"
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) < 2 {
		return fmt.Errorf("unexpected codex version output: %s", string(out))
	}

	version := "v" + fields[len(fields)-1]
	minVersion := "v0.134.0"

	if semver.Compare(version, minVersion) < 0 {
		return fmt.Errorf("codex version %s is too old, minimum required is %s, update with: npm update -g @openai/codex", fields[len(fields)-1], "0.134.0")
	}

	return nil
}
