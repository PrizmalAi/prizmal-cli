package opencode

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

const openCodeInstallScript = "curl -fsSL https://opencode.ai/install | bash"

var openCodeGOOS = runtime.GOOS

// OpenCode implements launch.Runner and Editor for OpenCode integration.
// Config is passed via OPENCODE_CONFIG_CONTENT env var at launch time
// instead of writing to opencode's config files.
type OpenCode struct {
	configContent string // JSON config built by Edit, passed to Run via env var
}

func (o *OpenCode) String() string { return "OpenCode" }

// RunsHeadless reports that OpenCode's `run` subcommand runs one message and
// exits. The subcommand is the first positional argument: a value of an option
// before it is not one.
func (o *OpenCode) RunsHeadless(args []string) bool {
	return launch.FirstPositional(args, opencodeValueFlags) == "run"
}

// opencodeValueFlags are OpenCode's options that take a value, so the
// subcommand scan does not read a value as the subcommand.
var opencodeValueFlags = map[string]bool{
	"-m": true, "--model": true,
	"-s": true, "--session": true,
	"--prompt": true, "--agent": true,
	"--port": true, "--hostname": true,
	"--mdns-domain": true, "--log-level": true,
	"--cors": true, "--replay-limit": true,
}

// OpenCodeInstaller is OpenCode's install story. Its curl installer writes the
// binary to ~/.opencode/bin, which is not on PATH on a machine that ran it and
// never added it, so that directory is read before the CLI will believe
// OpenCode is missing. Windows installs through npm instead of the script,
// and so needs Node.js rather than curl and bash.
var OpenCodeInstaller = launch.Installer{
	Name:         "opencode",
	DisplayName:  "OpenCode",
	GOOS:         func() string { return openCodeGOOS },
	Locate:       findOpenCode,
	Dependencies: OpenCodeInstallerDependencies,
	Command:      OpenCodeInstallerCommand,
}

// findOpenCode returns the opencode binary path, checking PATH first then the
// curl installer location (~/.opencode/bin) which may not be on PATH yet. The
// goos parameter is the installer's OS seam, because the name on disk differs
// on Windows and the package var is what a test overrides.
//
// An absent binary is launch.ErrHarnessAbsent and nothing else: it means "install it",
// so the install pipeline goes on to the dependency probe instead of reporting
// the lookup that already found nothing. A home directory that cannot be read
// is absent rather than a failure, for the same reason as in Claude's locate:
// PATH came up empty first, so the installer is the answer, and the operator
// never set the variable the error would name.
func findOpenCode(goos string) (string, error) {
	if p, err := exec.LookPath("opencode"); err == nil {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", launch.ErrHarnessAbsent
	}
	name := "opencode"
	if goos == "windows" {
		name = "opencode.exe"
	}
	fallback := filepath.Join(home, ".opencode", "bin", name)
	if _, err := os.Stat(fallback); err == nil {
		return fallback, nil
	}
	return "", launch.ErrHarnessAbsent
}

func (o *OpenCode) Installed() bool {
	return OpenCodeInstaller.Installed()
}

func (o *OpenCode) Run(model string, models []launch.LaunchModel, args []string) error {
	opencodePath, err := OpenCodeInstaller.EnsureInstalled()
	if err != nil {
		return err
	}

	cmd := exec.Command(opencodePath, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = o.childEnv(model, models)
	// Select the Prizmal model on launch so opencode doesn't fall back to its
	// own default (e.g. Claude Sonnet). The inline config registers the
	// provider + model, but opencode picks its own default model unless
	// --model is passed on the CLI.
	if model != "" {
		cmd.Args = append([]string{opencodePath, "--model", "prizmal/" + model}, args...)
	}
	return cmd.Run()
}

// childEnv is the environment opencode runs in: the inherited environment
// without any OPENCODE_CONFIG_DIR, then the variables the launch adds. opencode
// reads one config directory from that variable, so a value the user set for
// their own tools is replaced for this launch, even when the search tool could
// not be written and the launch sets none.
func (o *OpenCode) childEnv(model string, models []launch.LaunchModel) []string {
	return launch.ChildEnv([]string{"OPENCODE_CONFIG_DIR"}, o.envVars(model, models))
}

// envVars returns the variables prizmal adds to the opencode child process, on
// top of the inherited environment. It deliberately sets none of opencode's
// OPENCODE_ENABLE_* search flags: those switch on a websearch tool that calls
// opencode's hosted search backends directly. Search goes through the Switch
// instead, via the generated tool wired up here.
//
// It leaves opencode's XDG directories alone: the model picker state, prompt
// history, sign-ins and sessions are opencode's own.
func (o *OpenCode) envVars(model string, models []launch.LaunchModel) []string {
	var env []string
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
		// childEnv drops any inherited copy of this variable, so the
		// directory the launch writes the search tool to is the one opencode
		// reads.
		"OPENCODE_CONFIG_DIR="+dir,
		openCodeSearchURLEnv+"="+envconfig.OpenAIBaseURL(false),
		openCodeSearchKeyEnv+"="+envconfig.APIKey(),
		openCodeSearchModelEnv+"="+model,
	)
}

// OpenCodeInstallerDependencies lists what OpenCode's installer needs to run.
// Windows installs the npm package and so needs Node.js; the other platforms
// pipe the install script through bash and need curl to fetch it.
func OpenCodeInstallerDependencies(goos string) []launch.Dependency {
	if goos == "windows" {
		return []launch.Dependency{launch.NpmDependency}
	}
	return []launch.Dependency{launch.CurlDependency, launch.BashDependency}
}

func OpenCodeInstallerCommand(goos string) (string, []string, error) {
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
// Returns content built by Edit if available, otherwise builds it from the
// requested model and the catalog.
func (o *OpenCode) resolveContent(model string, models []launch.LaunchModel) string {
	if o.configContent != "" {
		return o.configContent
	}
	resolvedModels := resolveOpenCodeRunModels(model, models)
	if len(resolvedModels) == 0 {
		return ""
	}
	content, err := buildInlineConfig(resolvedModels[0], resolvedModels)
	if err != nil {
		return ""
	}
	return content
}

func resolveOpenCodeRunModels(primary string, models []launch.LaunchModel) []launch.LaunchModel {
	if primary == "" {
		return nil
	}

	resolved := make([]launch.LaunchModel, 0, 1+len(models))
	appendModel := func(name string) {
		if name == "" || hasLaunchModel(resolved, name) {
			return
		}
		if model, ok := launch.FindCatalogModel(models, name); ok {
			resolved = append(resolved, model)
			return
		}
		resolved = append(resolved, launch.FallbackLaunchModel(name))
	}

	appendModel(primary)
	for _, model := range models {
		appendModel(model.Name)
	}
	return resolved
}

func hasLaunchModel(models []launch.LaunchModel, name string) bool {
	for _, model := range models {
		if launch.ModelNamesSame(model.Name, name) {
			return true
		}
	}
	return false
}

// Paths lists the files prizmal edited. It edits none: the launch passes its
// configuration in the environment.
func (o *OpenCode) Paths() []string { return nil }

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
	return nil
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
					"baseURL": envconfig.OpenAIBaseURL(false),
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

func buildModelEntries(modelList []launch.LaunchModel) map[string]any {
	models := make(map[string]any)
	for _, model := range modelList {
		// The declared input modalities come from the capability vocabulary in
		// capability.go, which owns what the Switch's modalities mean, what
		// opencode spells them as, and that an unknown model keeps the
		// permissive list so an attachment is not newly refused.
		entry := map[string]any{
			"name": model.Name,
			"modalities": map[string]any{
				"input":  launch.DeclaredInputs(model, launch.HarnessOpenCode),
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
		models[model.Name] = entry
	}
	return models
}

func openCodeModelSupportsThinkingLevels(model launch.LaunchModel) bool {
	return strings.Contains(normalizeOpenCodeModelFamily(model.Name), "gptoss")
}

func normalizeOpenCodeModelFamily(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "_", "")
	return s
}
