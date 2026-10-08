package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/fileutil"
	"github.com/PrizmalAi/prizmal-cli/internal/model"
	"github.com/pelletier/go-toml/v2"
	"golang.org/x/mod/semver"
)

// Codex implements Runner for Codex integration
type Codex struct{}

func (c *Codex) String() string { return "Codex" }

// SupportsDeviceMode reports that Codex can run from an enrolled device: a
// provider's command-backed auth re-runs a command for the bearer token every
// refresh interval and again after a 401, so the launcher can hand it
// `prizmal auth token` instead of a fixed key.
func (c *Codex) SupportsDeviceMode() bool { return true }

// codexFallbackContextWindow is the context window a catalog entry declares
// when neither the operator nor the catalog states one. It is the 1M window
// every model the Switch serves has today, stated rather than learned.
// GET /v1/models marks a 1M model by decorating its id with [1m] and carries no
// other context length, so a model without the mark has no window to learn, and
// no model name is mapped to a window because such a table would be guesswork
// about an endpoint prizmal knows nothing else about.
//
// Codex derives auto-compaction from this number: 90% of context_window
// unless the entry says otherwise (ModelInfo::auto_compact_token_limit in
// codex-rs/protocol). Declaring too small a window makes Codex discard
// conversation history the Switch would have accepted. Revisit this when the
// Switch serves a model with a smaller window. $HARNESS_CONTEXT_LENGTH
// overrides it for an operator who knows better. It is a variable so a test
// can set a window no model has.
var codexFallbackContextWindow = 1_000_000

const (
	codexProfileName    = "prizmal"
	codexProviderName   = "prizmal"
	codexRestoreSuccess = "Codex launch configuration removed."

	// codexRefreshIntervalMs is how long Codex keeps a token from the auth
	// command before running it again. It is the Claude Code interval for the
	// same reason: the device token lives 10 minutes, and a refresh every 4
	// leaves at least 6 on the token in hand. Codex's own default of 5
	// minutes would refresh with only 5 left.
	codexRefreshIntervalMs = launch.DeviceTokenRefreshMs

	// codexApplyPatchToolType is the only value Codex 0.160 defines for
	// apply_patch_tool_type (ApplyPatchToolType::Freeform in codex-rs protocol).
	codexApplyPatchToolType = "freeform"

	codexRootProfileKey          = "profile"
	codexRootModelKey            = "model"
	codexRootModelProviderKey    = "model_provider"
	codexRootModelCatalogJSONKey = "model_catalog_json"

	// codexAgentsSubagentModelKey is the setting Codex reads for the model a
	// spawned subagent runs when the spawn call names none. Codex checks it
	// against the models its catalog lists.
	codexAgentsSubagentModelKey = "agents.default_subagent_model"
)

func (c *Codex) args(model, modelCatalogPath string, extra []string) ([]string, error) {
	if err := codexValidateExtraArgs(extra); err != nil {
		return nil, err
	}

	// Codex falls back to embedded mode for every -c override and warns about
	// it in its footer. --no-daemon makes that choice explicit.
	args := []string{"--no-daemon"}
	managed, err := codexManagedConfigOverrides(modelCatalogPath)
	if err != nil {
		return nil, err
	}
	for _, override := range managed {
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

func (c *Codex) Run(model string, models []launch.LaunchModel, args []string) error {
	if err := EnsureInstalled(); err != nil {
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

	cmd := exec.Command("codex", codexArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = codexChildEnv()
	return runPassingOnTermination(cmd)
}

// runPassingOnTermination runs cmd and waits for it. SIGHUP and SIGTERM go on to
// it, and SIGINT is ignored. Without the handler any of the three ends prizmal
// at once, before the deferred removal of the catalog directory in Run, and the
// directory stays behind. With it, prizmal waits for Codex to exit and Run
// cleans up.
//
// Codex handles Ctrl-C itself: the terminal sends SIGINT to the whole
// foreground process group, so Codex has it already, and passing it on would
// deliver it twice.
func runPassingOnTermination(cmd *exec.Cmd) error {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)

	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	for {
		select {
		case sig := <-signals:
			if sig == syscall.SIGINT {
				continue
			}
			_ = cmd.Process.Signal(sig) // fails on Windows, where the child ends with the console
		case err := <-done:
			return err
		}
	}
}

// envVars returns the child environment that carries the provider credential.
// This is the only channel the key travels on: the provider settings name
// OPENAI_API_KEY via env_key rather than embedding the key, so nothing
// credential-shaped outlives the launched process.
//
// In device mode it carries nothing. The provider settings name Codex's
// command-backed provider auth instead, and Codex runs `prizmal auth token` for
// its bearer token rather than reading a fixed key from the environment. An
// OPENAI_API_KEY the operator exported for OpenAI itself is removed, so it can
// neither reach the Switch nor be mistaken for the provider's key.
func (c *Codex) envVars() []string {
	if envconfig.DeviceMode() {
		return nil
	}
	return []string{"OPENAI_API_KEY=" + envconfig.APIKey()}
}

// executablePath finds the running binary. It is a variable so a test can make
// the lookup fail.
var executablePath = os.Executable

// codexAuthCommand is the command Codex runs for a device token: this binary,
// and the arguments that select its helper subcommand. Codex executes it
// directly, not through a shell, so the path needs no quoting.
func codexAuthCommand() (string, []string, error) {
	exe, err := executablePath()
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

// codexChildEnv builds Codex's environment: the inherited environment minus
// codexInheritedVars, then the variables the launch sets. The launch's own
// names are skipped in the inherited pass, so each appears once and the
// launch value wins.
func codexChildEnv() []string {
	// An OPENAI_API_KEY from the operator's shell never reaches Codex: with a
	// switch key the launch sets its own, and in device mode there is none.
	drop := append(slices.Clone(codexInheritedVars), "OPENAI_API_KEY")
	env := launch.ChildEnv(drop, (&Codex{}).envVars())
	// The helper is a separate prizmal process that resolves the Switch URL from
	// scratch, so this launch's resolved host is pinned for it.
	if envconfig.DeviceMode() {
		env = launch.EnsureHelperBaseURL(env, envconfig.BaseURL())
	}
	return env
}

// codexHygieneOverrides are the -c settings that keep a launch quiet: Codex
// analytics and the feedback upload are off, and --subagent-model, when given,
// becomes the model Codex uses for /review and for spawned subagents. They travel on the command line, so
// nothing is written to disk.
func codexHygieneOverrides() []string {
	overrides := []string{
		"analytics.enabled=false",
		"feedback.enabled=false",
	}
	if sub := launch.SelectedSubagentModel(); sub != "" {
		overrides = append(overrides,
			fmt.Sprintf("review_model=%q", sub),
			fmt.Sprintf("%s=%q", codexAgentsSubagentModelKey, sub),
		)
	}
	return overrides
}

// EnsureInstalled installs Codex with npm when it is missing, after the
// operator confirms.
func EnsureInstalled() error {
	if _, err := exec.LookPath("codex"); err == nil {
		return nil
	}
	if _, err := exec.LookPath("npm"); err != nil {
		return fmt.Errorf("codex is not installed and required dependencies are missing\n\nInstall the following first:\n  npm (Node.js): https://nodejs.org/\n\nThen re-run:\n  prizmal codex")
	}

	ok, err := launch.ConfirmPrompt("Codex is not installed. Install with npm?")
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("codex installation cancelled")
	}

	fmt.Fprintf(os.Stderr, "\nInstalling Codex...\n")
	cmd := exec.Command("npm", "install", "-g", "@openai/codex")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to install codex: %w", err)
	}
	if _, err := exec.LookPath("codex"); err != nil {
		return fmt.Errorf("codex was installed but the binary was not found on PATH\n\nYou may need to restart your shell")
	}
	fmt.Fprintf(os.Stderr, "%sCodex installed successfully%s\n\n", launch.AnsiGreen, launch.AnsiReset)
	return nil
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

func (c *Codex) SkipRestoreInstallCheck() bool {
	return true
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

func codexManagedConfigOverrides(modelCatalogPath string) ([]string, error) {
	overrides := []string{
		fmt.Sprintf("%s=%q", codexRootModelProviderKey, codexProfileName),
		fmt.Sprintf("model_providers.%s.name=%q", codexProfileName, codexProviderName),
		fmt.Sprintf("model_providers.%s.base_url=%q", codexProfileName, codexBaseURL()),
		fmt.Sprintf("model_providers.%s.wire_api=%q", codexProfileName, "responses"),
	}
	if envconfig.DeviceMode() {
		// Codex rejects a provider that sets both env_key and a command, so
		// device mode names the command and leaves env_key out.
		command, args, err := codexAuthCommand()
		if err != nil {
			return nil, err
		}
		overrides = append(overrides,
			fmt.Sprintf("model_providers.%s.auth.command=%q", codexProfileName, command),
			fmt.Sprintf("model_providers.%s.auth.args=%s", codexProfileName, codexAuthArgsTOML(args)),
			fmt.Sprintf("model_providers.%s.auth.refresh_interval_ms=%d", codexProfileName, codexRefreshIntervalMs),
		)
	} else {
		overrides = append(overrides, fmt.Sprintf("model_providers.%s.env_key=%q", codexProfileName, "OPENAI_API_KEY"))
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
	return strings.TrimRight(envconfig.ConnectableHost().String(), "/") + "/v1/"
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
	if model, ok := launch.FindLaunchModel(models, modelName); ok {
		model.Name = modelName
		return model.WithCloudLimits()
	}
	return launch.FallbackLaunchModel(modelName)
}

// codexCatalogModels returns the models Codex's /model picker lists: the rows
// the prizmal picker lists, in its order, each with the row's text. A tier alias
// is a row and the model folded into it is not, so the two pickers show
// one list.
//
// The launched model gets an entry even when no row shows it, such as -m naming
// a model a tier row folds away. Codex reads that entry for the model's
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
		launched = launched || launch.LaunchModelMatches(row.Model, modelName)
	}
	if !launched {
		entry := codexCatalogModel(modelName, models)
		entry.FoldedInto = ""
		out = append(out, entry)
	}
	// Codex rejects a subagent model its catalog doesn't list.
	if sub := launch.SelectedSubagentModel(); sub != "" && !slices.ContainsFunc(out, func(m launch.LaunchModel) bool { return launch.LaunchModelMatches(m.Name, sub) }) {
		out = append(out, codexCatalogModel(sub, models))
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
// WriteModelCatalog writes the model catalog a launch of model hands Codex to
// catalogPath, built the way Run builds it. It reads Codex's own system prompt
// from the installed binary, so Codex must be installed.
func WriteModelCatalog(catalogPath, model string, models []launch.LaunchModel) error {
	return writeCodexModelCatalog(catalogPath, codexCatalogModels(model, models))
}

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

func buildCodexModelEntry(launchModel launch.LaunchModel) map[string]any {
	modelName := launchModel.Name

	// The operator's $HARNESS_CONTEXT_LENGTH wins, then the window the catalog
	// states for the model (LaunchModel.ContextLength, which the core sets from
	// the [1m] mark on an id), then the fallback.
	contextWindow := codexFallbackContextWindow
	if launchModel.ContextLength > 0 {
		contextWindow = launchModel.ContextLength
	}
	if ctxLen := envconfig.ContextLength(); ctxLen > 0 {
		contextWindow = ctxLen
	}

	modalities := []string{"text"}
	if launchModel.HasCapability(model.CapabilityVision) {
		modalities = append(modalities, "image")
	}

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
		// Codex sends its freeform apply_patch edit tool only for a model whose
		// entry sets this (codex-rs core/src/tools/spec_plan.rs). Without it
		// Codex edits files through shell commands. The Switch translates the
		// tool for routes that have no freeform tools.
		"apply_patch_tool_type": codexApplyPatchToolType,
		// Codex defers its less-used tools behind a tool_search tool for an
		// entry that sets this, so a request carries a short tool list. It is
		// the Codex counterpart of the ENABLE_TOOL_SEARCH a Claude Code launch
		// sets.
		"supports_search_tool": true,
	}
}

// codexMinVersion is the oldest Codex the launch supports. The launch passes
// --no-daemon and command-backed provider auth, reads `codex debug models
// --bundled`, and writes supports_search_tool, apply_patch_tool_type and
// agents.default_subagent_model, which earlier releases do not know. It is the
// release the terminal baselines pin, and a test keeps the two equal.
const codexMinVersion = "0.160.0"

func checkCodexVersion() error {
	if _, err := exec.LookPath("codex"); err != nil {
		return fmt.Errorf("codex is not installed, install with: npm install -g @openai/codex")
	}

	out, err := exec.Command("codex", "--version").Output()
	if err != nil {
		return fmt.Errorf("failed to get codex version: %w", err)
	}

	// Parse output like "codex-cli 0.160.0"
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) < 2 {
		return fmt.Errorf("unexpected codex version output: %s", string(out))
	}

	version := "v" + fields[len(fields)-1]

	if semver.Compare(version, "v"+codexMinVersion) < 0 {
		return fmt.Errorf("codex version %s is too old, minimum required is %s, update with: npm update -g @openai/codex", fields[len(fields)-1], codexMinVersion)
	}

	return nil
}
