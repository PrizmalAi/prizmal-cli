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
	ansiReset  = "\x1b[0m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiBold   = "\x1b[1m"
)

// LaunchModel is model metadata passed to integration config writers, and it
// carries only what prizmal can actually learn about a model.
//
// Every field here has exactly one writer: the Switch's GET /v1/models
// catalog fills Name, Capabilities, Tier and Description (parseSwitchCatalog),
// and withClaudeTiers fills FoldedInto. The server-backed inventory of the
// original launcher is gone, so a field with no writer is a field nothing can
// read meaningfully — a context window, an output-token limit or a format that
// no writer supplies would make every adapter branch on a permanent zero and
// pick a default every time, which is indistinguishable from deleting the
// branch while reading as if it did something.
//
// TestLaunchModelCarriesOnlyCatalogFacts pins the field set, so adding a field
// back means adding its writer in the same change.
type LaunchModel struct {
	Name         string
	Capabilities []model.Capability

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

// fallbackLaunchModel builds a bare LaunchModel for a model name.
func fallbackLaunchModel(name string) LaunchModel {
	return LaunchModel{Name: name}
}

// findLaunchModel finds a model by name in a list.
func findLaunchModel(models []LaunchModel, name string) (LaunchModel, bool) {
	for _, m := range models {
		if m.Name == name {
			return m, true
		}
	}
	return LaunchModel{}, false
}

var confirmReader = bufio.NewReader(os.Stdin)

// quotePowerShellString quotes s for safe interpolation into a PowerShell
// -Command argument.
func quotePowerShellString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// ansiGray dims text.
const ansiGray = "\033[37m"

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

// launchModelNames extracts non-empty model names from a LaunchModel list.
func launchModelNames(models []LaunchModel) []string {
	names := make([]string, 0, len(models))
	for _, m := range models {
		if m.Name != "" {
			names = append(names, m.Name)
		}
	}
	return names
}

// launchModelMatches reports whether a candidate model name matches a target,
// tolerating the implicit :latest tag and Claude Code's [1m] context-budget
// suffix on either side. The Switch decorates every catalog id with the
// suffix and a launch sends the bare name, so a bare comparison never matches.
func launchModelMatches(candidate, name string) bool {
	if candidate == name {
		return true
	}
	return bareLaunchModelName(candidate) == bareLaunchModelName(name)
}

// bareLaunchModelName strips the decorations a model name can carry without
// naming a different model: the [1m] suffix, then the :latest tag under it.
func bareLaunchModelName(name string) string {
	name = strings.TrimSuffix(name, oneMillionSuffix)
	return strings.TrimSuffix(name, ":latest")
}

// SupportedIntegration lets an integration report platform support separately
// from installation state.
type SupportedIntegration interface {
	Supported() error
}

// ModelItem is a lightweight name/description row for listings.
type ModelItem struct {
	Name        string
	Description string
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
// A runner opts in when it has no other meaning for --model. pi uses a
// provider-qualified --model to choose a provider, and codex already refuses
// the flag as one it manages, so neither is an owner.
type OwningModelFlag interface {
	OwnsModelFlag() bool
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

// AnsiRed, AnsiGreen, AnsiReset and AnsiBold are exported ANSI helpers for
// the CLI entrypoint.
const (
	AnsiRed   = ansiRed
	AnsiGreen = ansiGreen
	AnsiReset = ansiReset
	AnsiBold  = ansiBold
)

// readJSONFile returns nil, nil when the file is absent (there is nothing to
// restore) and an error otherwise, parsed as a generic JSON object. The
// restore paths of pi and cline share it: both remove what their Edit added
// and must treat an absent config as nothing to do.
func readJSONFile(path string) (map[string]any, error) {
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

// writeJSONFile0600 marshals and writes a JSON config at 0600 without taking
// a backup. The restore paths use it in place of fileutil.WriteWithBackup,
// because that helper would copy the key being removed into ~/.prizmal/backup.
func writeJSONFile0600(path string, config map[string]any) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
