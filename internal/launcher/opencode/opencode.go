package opencode

import (
	"encoding/json"
	"fmt"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/fileutil"
)

const openCodeInstallScript = "curl -fsSL https://opencode.ai/install | bash"

var openCodeGOOS = runtime.GOOS

// OpenCode implements Runner and Editor for OpenCode integration.
// Config is passed via OPENCODE_CONFIG_CONTENT env var at launch time
// instead of writing to opencode's config files.
type OpenCode struct {
	configContent string // JSON config built by Edit, passed to Run via env var
}

func (o *OpenCode) String() string { return "OpenCode" }

// Find returns the opencode binary path, checking PATH first then the
// curl installer location (~/.opencode/bin) which may not be on PATH yet.
func Find() (string, bool) {
	if p, err := exec.LookPath("opencode"); err == nil {
		return p, true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	name := "opencode"
	if openCodeGOOS == "windows" {
		name = "opencode.exe"
	}
	fallback := filepath.Join(home, ".opencode", "bin", name)
	if _, err := os.Stat(fallback); err == nil {
		return fallback, true
	}
	return "", false
}

func (o *OpenCode) Run(model string, models []launch.LaunchModel, args []string) error {
	opencodePath, err := EnsureInstalled()
	if err != nil {
		return err
	}

	cmd := exec.Command(opencodePath, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), o.envVars(model, models)...)
	// Select the Prizmal model on launch so opencode doesn't fall back to its
	// own default (e.g. Claude Sonnet). The inline config registers the
	// provider + model, but opencode picks its own default model unless
	// --model is passed on the CLI.
	if model != "" {
		cmd.Args = append([]string{opencodePath, "--model", "prizmal/" + model}, args...)
	}
	return cmd.Run()
}

// envVars returns the variables prizmal adds to the opencode child process, on
// top of the inherited environment. It deliberately sets none of opencode's
// OPENCODE_ENABLE_* search flags: those switch on a websearch tool that calls
// opencode's hosted search backends directly. Search goes through the Switch
// instead, via the generated tool wired up here.
func (o *OpenCode) envVars(model string, models []launch.LaunchModel) []string {
	env := filterEnv(os.Environ(), "OPENCODE_CONFIG_DIR=")
	if content := o.resolveContent(model, models); content != "" {
		env = append(env, "OPENCODE_CONFIG_CONTENT="+content)
	}

	dir, err := openCodeSearchDir()
	if err != nil {
		return env
	}
	if err := writeOpenCodeSearchTool(dir); err != nil {
		// Search is an extra, not a precondition for the session. A launch that
		// cannot write the tool still runs, just without web search.
		return env
	}
	return append(env,
		// opencode reads one config directory from this variable, so a
		// value the user set for their own tools is replaced for this launch
		// unconditionally: filterEnv removed any inherited copy above.
		"OPENCODE_CONFIG_DIR="+dir,
		openCodeSearchURLEnv+"="+strings.TrimRight(envconfig.Host().String(), "/")+"/v1",
		openCodeSearchKeyEnv+"="+envconfig.APIKey(),
		openCodeSearchModelEnv+"="+model,
	)
}

func EnsureInstalled() (string, error) {
	if opencodePath, ok := Find(); ok {
		return opencodePath, nil
	}

	if err := checkOpenCodeInstallerDependencies(); err != nil {
		return "", err
	}

	ok, err := launch.ConfirmPrompt("OpenCode is not installed. Install now?")
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("opencode installation cancelled")
	}

	bin, args, err := openCodeInstallerCommand(openCodeGOOS)
	if err != nil {
		return "", err
	}

	fmt.Fprintf(os.Stderr, "\nInstalling OpenCode...\n")
	cmd := exec.Command(bin, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to install opencode: %w", err)
	}

	opencodePath, ok := Find()
	if !ok {
		return "", fmt.Errorf("opencode was installed but the binary was not found on PATH\n\nYou may need to restart your shell")
	}

	fmt.Fprintf(os.Stderr, "%sOpenCode installed successfully%s\n\n", launch.AnsiGreen, launch.AnsiReset)
	return opencodePath, nil
}

func checkOpenCodeInstallerDependencies() error {
	switch openCodeGOOS {
	case "windows":
		if _, err := exec.LookPath("npm"); err != nil {
			return fmt.Errorf("opencode is not installed and required dependencies are missing\n\nInstall the following first:\n  npm (Node.js): https://nodejs.org/\n\nThen re-run:\n  prizmal opencode")
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
			return fmt.Errorf("opencode is not installed and required dependencies are missing\n\nInstall the following first:\n  %s\n\nThen re-run:\n  prizmal opencode", strings.Join(missing, "\n  "))
		}
	}
	return nil
}

func openCodeInstallerCommand(goos string) (string, []string, error) {
	switch goos {
	case "windows":
		return "npm", []string{"install", "-g", "opencode-ai@latest"}, nil
	case "darwin", "linux":
		return "bash", []string{"-c", "set -o pipefail; " + openCodeInstallScript}, nil
	default:
		return "", nil, fmt.Errorf("unsupported platform for opencode install: %s", goos)
	}
}

// resolveContent returns the inline config to send via OPENCODE_CONFIG_CONTENT.
// Returns content built by Edit if available, otherwise builds from model.json
// with the requested model as primary (e.g. re-launch with saved config).
func (o *OpenCode) resolveContent(model string, models []launch.LaunchModel) string {
	if o.configContent != "" {
		return o.configContent
	}
	resolvedModels := resolveOpenCodeRunModels(model, models, readModelJSONModels())
	if len(resolvedModels) == 0 {
		return ""
	}
	content, err := buildInlineConfig(resolvedModels[0], resolvedModels)
	if err != nil {
		return ""
	}
	return content
}

func resolveOpenCodeRunModels(primary string, models []launch.LaunchModel, stateModels []string) []launch.LaunchModel {
	if primary == "" {
		return nil
	}

	resolved := make([]launch.LaunchModel, 0, 1+len(models)+len(stateModels))
	appendModel := func(name string) {
		if name == "" || hasLaunchModel(resolved, name) {
			return
		}
		if model, ok := launch.FindLaunchModel(models, name); ok {
			resolved = append(resolved, model)
			return
		}
		resolved = append(resolved, launch.FallbackLaunchModel(name))
	}

	appendModel(primary)
	for _, model := range models {
		appendModel(model.Name)
	}
	for _, model := range stateModels {
		appendModel(model)
	}
	return resolved
}

func hasLaunchModel(models []launch.LaunchModel, name string) bool {
	for _, model := range models {
		if launch.LaunchModelMatches(model.Name, name) || launch.LaunchModelMatches(name, model.Name) {
			return true
		}
	}
	return false
}

func (o *OpenCode) Paths() []string {
	sp, err := openCodeStatePath()
	if err != nil {
		return nil
	}
	if _, err := os.Stat(sp); err == nil {
		return []string{sp}
	}
	return nil
}

// openCodeStatePath returns the path to opencode's model state file.
// TODO: this hardcodes the Linux/macOS XDG path. On Windows, opencode stores
// state under %LOCALAPPDATA% (or similar) — verify and branch on runtime.GOOS.
func openCodeStatePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "opencode", "model.json"), nil
}

func (o *OpenCode) Edit(models []launch.LaunchModel) error {
	modelList := launch.LaunchModelNames(models)
	if len(modelList) == 0 {
		return nil
	}

	content, err := buildInlineConfig(models[0], models)
	if err != nil {
		return err
	}
	o.configContent = content

	// Write model state file so models appear in OpenCode's model picker
	statePath, err := openCodeStatePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		return err
	}

	state := map[string]any{
		"recent":   []any{},
		"favorite": []any{},
		"variant":  map[string]any{},
	}
	if data, err := os.ReadFile(statePath); err == nil {
		_ = json.Unmarshal(data, &state) // Ignore parse errors; use defaults
	}

	recent, _ := state["recent"].([]any)

	// Remove existing managed entries so we can re-add the current models.
	newRecent := slices.DeleteFunc(slices.Clone(recent), func(entry any) bool {
		e, ok := entry.(map[string]any)
		if !ok {
			return false
		}
		return isOpenCodeManagedProvider(e)
	})

	// Prepend models in reverse order so first model ends up first
	for _, model := range slices.Backward(modelList) {
		newRecent = slices.Insert(newRecent, 0, any(map[string]any{
			"providerID": "prizmal",
			"modelID":    model,
		}))
	}

	const maxRecentModels = 10
	newRecent = newRecent[:min(len(newRecent), maxRecentModels)]

	state["recent"] = newRecent

	// Clean up managed entries from the favorite list and variant map.
	if favorite, ok := state["favorite"].([]any); ok {
		state["favorite"] = slices.DeleteFunc(slices.Clone(favorite), func(entry any) bool {
			e, ok := entry.(map[string]any)
			if !ok {
				return false
			}
			return isOpenCodeManagedProvider(e)
		})
	}
	if variants, ok := state["variant"].(map[string]any); ok {
		for k := range variants {
			if strings.HasPrefix(k, "prizmal/") {
				delete(variants, k)
			}
		}
	}

	stateData, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.WriteWithBackup(statePath, stateData, "opencode")
}

func (o *OpenCode) Models() []string {
	return nil
}

// buildInlineConfig produces the JSON string for OPENCODE_CONFIG_CONTENT.
// primary is the model to launch with, models is the full list of available models.
func buildInlineConfig(primary launch.LaunchModel, models []launch.LaunchModel) (string, error) {
	if primary.Name == "" || len(models) == 0 {
		return "", fmt.Errorf("buildInlineConfig: primary and models are required")
	}

	config := map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"provider": map[string]any{
			"prizmal": map[string]any{
				"npm":  "@ai-sdk/openai-compatible",
				"name": "Prizmal",
				"options": map[string]any{
					"baseURL": envconfig.Host().String() + "/v1",
					"apiKey":  envconfig.APIKey(),
				},
				"models": buildModelEntries(models),
			},
		},
		"model": "prizmal/" + primary.Name,
		// Remove opencode's built-in websearch tool from the model's toolset.
		// It posts to opencode's hosted search backends, outside the Switch.
		// Both blocks are set because opencode folds `tools` into `permission`
		// and then lets an explicit `permission` entry win, so stating only one
		// leaves the outcome dependent on which of the two the user's own
		// config happens to carry.
		"tools":      map[string]any{"websearch": false},
		"permission": map[string]any{"websearch": "deny"},
	}
	data, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// readModelJSONModels reads launch-managed model IDs from the opencode model.json state file
func readModelJSONModels() []string {
	statePath, err := openCodeStatePath()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(statePath)
	if err != nil {
		return nil
	}
	var state map[string]any
	if err := json.Unmarshal(data, &state); err != nil {
		return nil
	}
	recent, _ := state["recent"].([]any)
	var models []string
	for _, entry := range recent {
		e, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if !isOpenCodeManagedProvider(e) {
			continue
		}
		if id, ok := e["modelID"].(string); ok && id != "" {
			models = append(models, id)
		}
	}
	return models
}

// isOpenCodeManagedProvider reports whether a model-picker state entry is one
// this CLI manages: the 'prizmal' provider id.
func isOpenCodeManagedProvider(e map[string]any) bool {
	id, _ := e["providerID"].(string)
	return id == "prizmal"
}

func buildModelEntries(modelList []launch.LaunchModel) map[string]any {
	models := make(map[string]any)
	for _, model := range modelList {
		entry := map[string]any{
			"name": model.Name,
			"modalities": map[string]any{
				"input":  openCodeInputModalities(model),
				"output": []string{"text"},
			},
		}
		if model.HasCapability("thinking") {
			entry["reasoning"] = true
			if openCodeModelSupportsThinkingLevels(model) {
				entry["options"] = map[string]any{"reasoningEffort": "medium"}
				entry["variants"] = map[string]any{
					"low":    map[string]any{"reasoningEffort": "low"},
					"medium": map[string]any{"reasoningEffort": "medium"},
					"high":   map[string]any{"reasoningEffort": "high"},
					"max":    map[string]any{"reasoningEffort": "max"},
				}
			} else {
				entry["variants"] = map[string]any{
					"none":   map[string]any{"reasoningEffort": "none"},
					"low":    map[string]any{"disabled": true},
					"medium": map[string]any{"disabled": true},
					"high":   map[string]any{"disabled": true},
				}
			}
		}
		if model.MaxOutputTokens > 0 {
			limit := make(map[string]any)
			if model.ContextLength > 0 {
				limit["context"] = model.ContextLength
			}
			limit["output"] = model.MaxOutputTokens
			entry["limit"] = limit
		}
		models[model.Name] = entry
	}
	return models
}

// openCodeInputModalities returns the input modalities to declare for a model.
// OpenCode only sends an attached image or PDF when the entry declares the
// matching input modality; otherwise it writes a refusal into the prompt and
// the model answers without the document.
//
// The Switch's GET /v1/models is the source of truth, reached at launch by
// WithSwitchCapabilities. Its document modality is spelled "file" and reaches
// here as CapabilityDocument; opencode spells the same thing "pdf". Image
// input is a separate signal on both sides, so each modality is declared from
// its own capability and neither stands in for the other.
//
// A model with no capabilities at all is unknown, not text-only. The Switch
// omits input_modalities on some entries, and the fetch is skipped outright
// for an unauthenticated launch, so unknown is common and must not newly block
// an attachment: it keeps the permissive list this entry has always declared.
func openCodeInputModalities(m launch.LaunchModel) []string {
	if len(m.Capabilities) == 0 {
		return []string{"text", "image", "pdf"}
	}
	modalities := []string{"text"}
	if m.HasCapability("vision") {
		modalities = append(modalities, "image")
	}
	if m.HasCapability("document") {
		modalities = append(modalities, "pdf")
	}
	return modalities
}

func openCodeModelSupportsThinkingLevels(model launch.LaunchModel) bool {
	for _, family := range append([]string{model.Details.Family}, model.Details.Families...) {
		if normalizeOpenCodeModelFamily(family) == "gptoss" {
			return true
		}
	}

	return strings.Contains(normalizeOpenCodeModelFamily(model.Name), "gptoss")
}

func normalizeOpenCodeModelFamily(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "_", "")
	return s
}
