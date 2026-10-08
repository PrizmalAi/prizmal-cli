package launch

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/PrizmalAi/prizmal-cli/internal/model"
)

// ANSI color helpers shared by the adapters.
const (
	AnsiReset  = "\x1b[0m"
	AnsiRed    = "\x1b[31m"
	AnsiGreen  = "\x1b[32m"
	AnsiYellow = "\x1b[33m"
	AnsiBold   = "\x1b[1m"
)

// LaunchModel is minimal model metadata passed to integration config writers.
// The server-backed inventory from the original launcher is gone; models come
// from the --model flag, so most fields stay zero and adapters fall back to
// their defaults.
type LaunchModel struct {
	Name         string
	Remote       bool
	ToolCapable  bool
	Capabilities []model.Capability
	Details      struct {
		ContextLength int
		Format        string
		Family        string
		Families      []string
	}
	ContextLength   int
	MaxOutputTokens int

	// Tier is the Claude tier the Switch says this model serves, one of
	// opus, sonnet, haiku or fable, and empty when it said none.
	Tier string
	// Description is the Switch's text for the model's picker row.
	Description string
	// FoldedInto is the tier alias whose row stands for this model. A folded
	// model gets no row of its own, and stays in the catalog so a launch that
	// names it still finds its tier and description.
	FoldedInto string
}

func (m LaunchModel) HasCapability(capability model.Capability) bool {
	for _, c := range m.Capabilities {
		if c == capability {
			return true
		}
	}
	return false
}

func (m LaunchModel) WithCloudLimits() LaunchModel { return m }

// FallbackLaunchModel builds a bare LaunchModel for a model name.
func FallbackLaunchModel(name string) LaunchModel {
	return LaunchModel{Name: name}
}

// FindLaunchModel finds a model by name in a list.
func FindLaunchModel(models []LaunchModel, name string) (LaunchModel, bool) {
	for _, m := range models {
		if m.Name == name {
			return m, true
		}
	}
	return LaunchModel{}, false
}

// LookupCloudModelLimit always reports no limit known.
func LookupCloudModelLimit(string) (struct{ Context, Output int }, bool) {
	return struct{ Context, Output int }{}, false
}

var confirmReader = bufio.NewReader(os.Stdin)

// AnsiGray dims text.
const AnsiGray = "\033[37m"

// ErrCancelled is returned when the user cancels a prompt.
var ErrCancelled = errors.New("cancelled")

// ConfirmDefault controls which answer is highlighted in confirmation prompts.
type ConfirmDefault int

const (
	ConfirmDefaultYes ConfirmDefault = iota
	ConfirmDefaultNo
)

// ConfirmOptions customizes labels for confirmation prompts.
type ConfirmOptions struct {
	YesLabel string
	NoLabel  string
	Default  ConfirmDefault
}

// DefaultConfirmPrompt provides a TUI-based confirmation prompt.
var DefaultConfirmPrompt func(prompt string, options ConfirmOptions) (bool, error)

type launchConfirmPolicy struct {
	yes               bool
	requireYesMessage bool
}

var currentLaunchConfirmPolicy launchConfirmPolicy

// SetConfirmPolicy configures the auto-approve behavior for confirmation
// prompts. When yes is true, all ConfirmPrompt calls return true without
// prompting. This is wired from the --yes flag in main.go.
func SetConfirmPolicy(yes bool) {
	currentLaunchConfirmPolicy.yes = yes
}

// ConfirmPrompt is the shared confirmation gate (auto-approved by --yes).
func ConfirmPrompt(prompt string) (bool, error) {
	return ConfirmPromptWithOptions(prompt, ConfirmOptions{})
}

// ConfirmPromptWithOptions is the shared confirmation gate for launch flows
// that need custom yes/no labels.
func ConfirmPromptWithOptions(prompt string, options ConfirmOptions) (bool, error) {
	if currentLaunchConfirmPolicy.yes {
		return true, nil
	}
	if currentLaunchConfirmPolicy.requireYesMessage {
		return false, fmt.Errorf("%s requires confirmation; re-run with --yes to continue", prompt)
	}
	if DefaultConfirmPrompt != nil {
		return DefaultConfirmPrompt(prompt, options)
	}
	return plainConfirmPrompt(prompt, options)
}

func plainConfirmPrompt(prompt string, options ConfirmOptions) (bool, error) {
	defaultNo := options.Default == ConfirmDefaultNo
	suffix := "(y/N)"
	if !defaultNo {
		suffix = "(Y/n)"
	}
	fmt.Fprintf(os.Stderr, "%s %s ", prompt, suffix)
	line, err := confirmReader.ReadString('\n')
	if err != nil && line == "" {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer == "" {
		return !defaultNo, nil
	}
	return answer == "y" || answer == "yes", nil
}

// LaunchModelNames extracts non-empty model names from a LaunchModel list.
func LaunchModelNames(models []LaunchModel) []string {
	names := make([]string, 0, len(models))
	for _, m := range models {
		if m.Name != "" {
			names = append(names, m.Name)
		}
	}
	return names
}

// LaunchModelMatches reports whether a candidate model name matches a target,
// tolerating the implicit :latest tag and Claude Code's [1m] context-budget
// suffix on either side. The Switch decorates every catalog id with the
// suffix and a launch sends the bare name, so a bare comparison never matches.
func LaunchModelMatches(candidate, name string) bool {
	if candidate == name {
		return true
	}
	return bareLaunchModelName(candidate) == bareLaunchModelName(name)
}

// bareLaunchModelName strips the decorations a model name can carry without
// naming a different model: the [1m] suffix, then the :latest tag under it.
func bareLaunchModelName(name string) string {
	name = strings.TrimSuffix(name, OneMillionSuffix)
	return strings.TrimSuffix(name, ":latest")
}

// SupportedIntegration lets an integration report platform support separately
// from installation state.
type SupportedIntegration interface {
	Supported() error
}

// ModelListShower is implemented by a runner that shows the launched harness
// the whole model catalog, rather than only the model it runs.
//
// It is opt-in because a runner without it may read models[0] as the model to
// select: pi and cline both write that model into their config as the default
// provider's model. Handing them a wider list would change what a bare launch
// selects, so widening the list is the runner's own decision to make.
type ModelListShower interface {
	ShowsModelList() bool
}

// Runner executes an integration with the selected model and passthrough args.
type Runner interface {
	Run(model string, models []LaunchModel, args []string) error
	String() string
}

// OwningModelFlag is a runner whose launch model is set only from prizmal's
// own resolution, never from a --model the operator typed after the integration
// name. The CLI consumes such an argument before dispatch and hands the runner
// the resolved model instead.
//
// A runner opts in when it has no other meaning for --model. Claude Code and
// Codex do: the launch states the model itself. pi uses a provider-qualified
// --model to choose a provider, so it is not an owner.
type OwningModelFlag interface {
	OwnsModelFlag() bool
}

// SubagentModelRunner is a runner that gives subagents the model --subagent-model
// names. Claude Code and Codex do. The other harnesses have no such setting, so
// the CLI refuses the flag for them rather than ignoring it.
type SubagentModelRunner interface {
	TakesSubagentModel() bool
}

// DeviceModeRunner is a runner that can refresh its own credential from an
// enrolled device key. Claude Code does it through its apiKeyHelper, and Pi
// through an extension its launch loads whose provider runs `prizmal auth
// token` for each request, so both opt in. Every other runner receives the key
// once through the environment and cannot refresh a short-lived device token;
// the CLI ignores device login for them and runs them on the switch key the
// config already holds.
type DeviceModeRunner interface {
	SupportsDeviceMode() bool
}

// SupportsDeviceMode reports whether a runner can run from an enrolled device.
func SupportsDeviceMode(runner Runner) bool {
	d, ok := runner.(DeviceModeRunner)
	return ok && d.SupportsDeviceMode()
}

// Editor can edit config files for integrations that support model configuration.
type Editor interface {
	Paths() []string
	Edit(models []LaunchModel) error
	Models() []string
}

// Configurer persists configuration via Configure instead of Edit, for
// integrations that manage their own config files.
type Configurer interface {
	ConfigureWithModels(primary string, models []LaunchModel) error
}

// ReadJSONFile returns nil, nil when the file is absent (there is nothing to
// restore) and an error otherwise, parsed as a generic JSON object. The
// restore paths of pi and cline share it: both remove what their Edit added
// and must treat an absent config as nothing to do.
func ReadJSONFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	config := make(map[string]any)
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return config, nil
}

// WriteJSONFile0600 marshals and writes a JSON config at 0600 without taking
// a backup. The restore paths use it in place of fileutil.WriteWithBackup,
// because that helper would copy the key being removed into ~/.prizmal/backup.
func WriteJSONFile0600(path string, config map[string]any) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
