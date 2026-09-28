// Command prizmal launches any coding-agent harness against a
// Prizmal Switch (or an arbitrary provider endpoint).
//
// Usage:
//
//	prizmal                                list supported integrations
//	prizmal [flags] <integration> [-- <extra args>]
//
// The provider URL comes from --url or $PRIZMAL_SWITCH_URL.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/PrizmalAi/prizmal-cli/internal/config"
	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/fileutil"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	launcher "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

var (
	version              = "dev"
	model                string
	url                  string
	apiKey               string
	yes                  bool
	restore              bool
	persistOnly          bool
	allowUnauthenticated bool
	listFlag             bool
	pickFlag             bool
	subagentModel        string
)

// usageLine is the first line of `prizmal --help`. It spells `[flags]` itself,
// before the integration name: cobra would otherwise append " [flags]" at the
// end, which reads as an instruction to put prizmal's flags where prizmal
// already stops parsing them. Flags before the integration name are prizmal's
// own; every argument after the name is harness passthrough.
const usageLine = "prizmal [flags] [INTEGRATION] [-- EXTRA_ARGS...]"

func main() {
	root := &cobra.Command{
		Use:   usageLine,
		Short: "Launch coding-agent harnesses against the Prizmal Switch",
		Long: `Launch a coding-agent harness (Claude Code, Codex, OpenCode, Pi, ...) configured
to use the Prizmal Switch endpoint.

The provider URL is resolved from (in order):
  1. the --url flag
  2. the $PRIZMAL_SWITCH_URL environment variable
  3. the config file (base_url in ~/.prizmal/config.json)
  4. the default ` + envconfig.DefaultURL + `

The provider API key resolves from --api-key, then $PRIZMAL_SWITCH_KEY,
then the config file's api_key, the same order the URL follows. The launch
announces which source was used:
  using api key from: --api-key | $PRIZMAL_SWITCH_KEY | config file (~/.prizmal/config.json) | none
Launching against a remote host with no credential is refused; loopback
URLs need no key, and --allow-unauthenticated overrides the refusal.

Flags before the integration name are prizmal's own: --model, --url,
--api-key, --yes, --list, --restore, --persist. Every argument after the
integration name is handed to the harness unchanged, so claude --resume
<session-id> and any other harness flag needs no separator and can never
be read as a prizmal flag.

--list answers a different question: it prints the models this switch key's
tenant serves, one per line, and exits without launching. The list comes from
the Switch, so it holds exactly the names that key can route by.

A launch always runs a real model, and never a placeholder. It takes the model
from --model, or from the default saved by an earlier --pick, or asks with the
interactive picker when neither names one. There is no default model name: a
launch with nothing to send stops rather than guessing.

Examples:
  prizmal                                   # list integrations
  prizmal --list                            # list the tenant's models
  prizmal --pick claude                     # choose a model, and save the choice
  prizmal --model gpt-oss:20b claude        # launch Claude Code
  prizmal claude --resume <session-id>      # harness flags pass straight through
  prizmal --url http://localhost:8080 codex -- --sandbox workspace-write
  PRIZMAL_SWITCH_URL=http://localhost:1234 prizmal opencode`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ArbitraryArgs,
		Version:       resolveVersion(version),
		RunE: func(cmd *cobra.Command, args []string) error {
			envconfig.SetBaseURL(url)
			envconfig.SetAPIKey(apiKey)

			cfg, err := ensureConfig()
			if err != nil {
				return err
			}

			// URL precedence: --url > $PRIZMAL_SWITCH_URL > config base_url
			// > default. SetConfigBaseURL only fills the gap below the env var.
			if cfg != nil && cfg.BaseURL != "" {
				envconfig.SetConfigBaseURL(cfg.BaseURL)
			}

			// API key precedence: --api-key > $PRIZMAL_SWITCH_KEY > config
			// api_key (resolved), the same order the URL follows. A key that
			// a higher source already supplies is not resolved at all, so a
			// command-form api_key does not run when the flag or the
			// environment wins. announceKeySource tells the operator which
			// source won.
			if apiKey == "" && envconfig.EnvAPIKey() == "" && cfg != nil && cfg.APIKey != nil {
				resolved, err := cfg.APIKey.Resolve()
				if err != nil {
					return err
				}
				envconfig.SetConfigAPIKey(resolved)
			}

			// --list answers a question rather than launching, so it runs
			// before the integration dispatch and ignores the integration
			// word: `prizmal --list claude` lists the tenant's models and
			// never touches the harness.
			if listFlag {
				return listTenantModels()
			}

			// --pick alone is a mode: it chooses a model, saves it, and exits
			// without launching, because no integration was named to launch.
			// With an integration named it is a launch flag instead, so
			// `prizmal --pick claude` chooses a model and starts Claude Code
			// with it.
			if pickFlag && len(args) == 0 {
				return pickDefaultModel(cfg)
			}

			if len(args) == 0 {
				return listIntegrations()
			}
			launcher.SetConfirmPolicy(yes)
			launcher.SetSubagentModel(subagentModel)
			integration, extraArgs := splitLaunchInvocation(args)
			return launch(integration, extraArgs, cfg)
		},
	}

	registerFlags(root.Flags())

	// Flag parsing stops at the first non-flag token, the integration name,
	// and every later token is handed to the harness unchanged. Interspersed
	// parsing off is what makes `claude --resume <session-id>` and any other
	// harness flag reach the harness instead of dying as an unknown flag.
	root.Flags().SetInterspersed(false)

	if err := root.Execute(); err != nil {
		// A cancellation is the operator's decision, not a failure. The picker
		// reports it so a menu backed out of with Esc or ctrl+c exits without
		// a red error line the operator caused on purpose. The exit code still
		// says nothing was done, which is what a script should see.
		if errors.Is(err, launcher.ErrCancelled) {
			fmt.Fprintf(os.Stderr, "cancelled\n")
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "%serror: %v%s\n", launcher.AnsiRed, err, launcher.AnsiReset)
		os.Exit(1)
	}
}

// registerFlags declares every flag prizmal defines itself, on the given set.
// It exists as a seam: the README's Usage block is checked against this set,
// so a flag cannot be added, renamed, or re-described without the docs
// following it (see TestREADMEUsageDocumentsEveryRegisteredFlag).
func registerFlags(flags *pflag.FlagSet) {
	flags.StringVarP(&model, "model", "m", "", "model to launch with")
	// --pick has no shorthand on purpose. -p is --print in Claude Code, the
	// flag an operator reaches for on almost every non-interactive run, so
	// claiming it here would turn `prizmal claude -p "prompt"` into a picker
	// prompt instead of the print run they meant. Long form only.
	flags.BoolVar(&pickFlag, "pick", false, "choose a model from this switch key's tenant, and save it as the default")
	flags.StringVar(&subagentModel, "subagent-model", "", "model for subagents, which default to the session model")
	flags.StringVarP(&url, "url", "u", "", "provider base URL (or $"+envconfig.EnvVar+")")
	flags.StringVarP(&apiKey, "api-key", "k", "", "provider API key (or $"+envconfig.KeyEnvVar+")")
	flags.BoolVarP(&yes, "yes", "y", false, "auto-approve confirmation prompts")
	flags.BoolVar(&restore, "restore", false, "restore the integration's original configuration and exit")
	flags.BoolVar(&persistOnly, "persist", false, "write the integration configuration without launching (persistent until --restore)")
	flags.BoolVar(&allowUnauthenticated, "allow-unauthenticated", false, "allow an unauthenticated launch against a remote Switch (debugging only)")
	flags.BoolVarP(&listFlag, "list", "l", false, "print the models this switch key's tenant serves, and exit")
}

// listTenantModels prints the names the configured switch key can route by,
// which are its tenant's models, one per line on stdout so the output pipes
// into another command.
//
// It shares the credential gate with a launch. A remote host with no key is
// refused for the same reason, because the Switch answers a keyless request
// with 401 and the operator reads that as a bad key rather than a missing one.
// A listing has no fallback the way a launch does, so a fetch that fails is the
// command failing.
//
// It does not announce the key source the way a launch does. A launch
// announces it because the source decides what the child process gets; a
// listing answers a question and pipes its answer, so narration on stderr only
// pollutes the output. The refusal still names every source when a key is
// missing, which is the case where the source actually matters.
func listTenantModels() error {
	if err := requireCredentialForRemote(); err != nil {
		return err
	}

	names, err := launcher.ListSwitchModels(context.Background())
	if err != nil {
		return launcher.CatalogError(err)
	}
	if len(names) == 0 {
		// Silence would read as a successful listing that lost its rows.
		fmt.Fprintf(os.Stderr, "No models: this switch key's tenant serves none.\n")
		return nil
	}
	for _, name := range names {
		// Explicit discard, like the other best-effort writes here. A write to
		// stdout fails when the reader closes the pipe early, which is what
		// `prizmal --list | head` does; the listing is still correct, so the
		// command must not fail on it.
		_, _ = fmt.Fprintln(os.Stdout, name)
	}
	return nil
}

func listIntegrations() error {
	specs := launcher.ListVisibleIntegrationSpecs()
	fmt.Fprintf(os.Stderr, "Supported integrations:\n\n")
	for _, spec := range specs {
		line := fmt.Sprintf("  %-14s %s", spec.Name, spec.Description)
		if len(spec.Aliases) > 0 {
			line += fmt.Sprintf(" (aliases: %s)", strings.Join(spec.Aliases, ", "))
		}
		fmt.Fprintln(os.Stderr, line)
	}
	fmt.Fprintf(os.Stderr, "\nProvider URL: %s (override with --url or $%s)\n",
		envconfig.BaseURL(), envconfig.EnvVar)
	return nil
}

// launchModels builds the model list a runner is handed: the model the launch
// runs, carrying the capabilities the catalog gives it, plus the tenant's other
// models for the runners that show them in their own picker.
//
// The fetch belongs with the credential gate, and cannot physically sit next
// to it: requireCredentialForRemote runs at the end of launch(), by which
// point Edit and ConfigureWithModels have already written the config that
// needs the capabilities. It honours the gate's intent instead, by only
// calling the endpoint when a key resolved, so an unauthenticated launch
// never touches it.
func launchModels(chosen string, catalog []launcher.LaunchModel, showPickerRows bool) []launcher.LaunchModel {
	if chosen == "" {
		return nil
	}
	return launcher.LaunchModels(chosen, catalog, showPickerRows)
}

// runnerShowsModelList reports whether a runner wants the whole catalog rather
// than only the model it launches.
func runnerShowsModelList(runner launcher.Runner) bool {
	shower, ok := runner.(launcher.ModelListShower)
	return ok && shower.ShowsModelList()
}

// resolveLaunchModel decides which model this launch runs.
//
// It is the whole precedence, in one place: --model wins, then the default the
// last pick saved, then the picker. Nothing falls back to a placeholder name:
// every path either names a real model or stops.
//
// --pick skips the saved default: asking to choose means choosing, not reusing,
// which is what makes `prizmal --pick claude` open the menu on a machine that
// already has a default.
//
// It returns the launch's model list too, which is the model plus, for the
// runners that show one, the rest of the catalog. Only the picker path needs
// the list to succeed: a launch that already has its model treats an unreadable
// list as lost capabilities and lost picker rows, not as a failed launch. The
// CLI points at arbitrary provider endpoints, and one that serves no
// /v1/models is a supported target.
//
// cfg may be nil on a machine with no config file. Then a pick still launches
// with the model the operator chose; there is simply nowhere to save it.
func resolveLaunchModel(cfg *config.Config) (string, []launcher.LaunchModel, error) {
	ctx := context.Background()

	switch {
	case model != "":
		return model, launcher.BestEffortCatalog(ctx, os.Stderr), nil

	case cfg != nil && cfg.DefaultModel != "" && !pickFlag:
		return cfg.DefaultModel, launcher.BestEffortCatalog(ctx, os.Stderr), nil
	}

	// The operator is choosing: either they asked with --pick, or nothing
	// names a model and a terminal is there to ask on. Both need a person and
	// a list, so both are refused before a menu could open empty.
	if !launcher.StdinIsTerminal() {
		if pickFlag {
			return "", nil, fmt.Errorf(
				"--pick needs an interactive terminal: pass --model MODEL instead, or run it on a terminal")
		}
		return "", nil, fmt.Errorf(
			"no model selected: pass --model MODEL, run --pick to choose one, or set default_model in the config file")
	}

	// The credential gate runs here, before the fetch, and launch() runs it
	// again later without harm. The picker's list comes from the switch, so a
	// remote host with no key answers 401, and the operator reads that as a
	// rejected key rather than a missing one: the exact confusion the gate
	// exists to prevent, and the reason --list gates before the same fetch.
	// The launches that skip this point already have their model and never
	// depend on the fetch, so they reach the gate at the end of launch().
	if err := requireCredentialForRemote(); err != nil {
		return "", nil, err
	}

	catalog, err := launcher.FetchCatalog(ctx)
	if err != nil {
		return "", nil, launcher.CatalogError(err)
	}

	chosen, err := launcher.PickModel(launcher.ModelRows(catalog))
	if err != nil {
		return "", nil, err
	}

	// The choice becomes the default, so the next bare launch repeats it. A
	// launch with no config file keeps the choice for this launch only: it is
	// still the model the operator picked.
	if cfg != nil {
		cfg.DefaultModel = chosen
		if err := cfg.Save(); err != nil {
			return "", nil, fmt.Errorf("could not save the default model: %w", err)
		}
	}
	return chosen, catalog, nil
}

// pickDefaultModel opens the picker, saves the choice as the default, and
// returns without launching anything.
//
// It shares the credential gate and the catalog fetch with a launch, and gates
// before the fetch for the reason --list does: a remote switch with no key
// answers 401, and the operator reads that as a rejected key rather than a
// missing one.
//
// A machine with no config file has nowhere to save the choice, so that is
// refused rather than silently doing nothing. Unlike a pick during a launch,
// where the model in hand is the point, this mode exists only to set the
// default; a run that cannot set it has not done what was asked.
func pickDefaultModel(cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("no config file to save a default model in; run a launch once to create one")
	}
	// The menu needs a person. Refusing up front beats opening a full-screen list on a
	// pipe, which would read whatever arrives on stdin as keystrokes.
	if !launcher.StdinIsTerminal() {
		return fmt.Errorf("--pick needs an interactive terminal: pass --model MODEL instead, or run it on a terminal")
	}

	announceKeySource(os.Stderr)
	if err := requireCredentialForRemote(); err != nil {
		return err
	}

	catalog, err := launcher.FetchCatalog(context.Background())
	if err != nil {
		return launcher.CatalogError(err)
	}

	chosen, err := launcher.PickModel(launcher.ModelRows(catalog))
	if err != nil {
		return err
	}

	cfg.DefaultModel = chosen
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("could not save the default model: %w", err)
	}

	// Confirm on stderr, where the CLI's other confirmations go, and where the
	// announcement above already is, so the operator's eye is in one place. The
	// path is named because "saved" without a location leaves the question of
	// where, and a config file is written on every pick.
	if p, err := config.Path(); err == nil {
		fmt.Fprintf(os.Stderr, "%sSaved %s as the default model in %s.%s\n",
			launcher.AnsiGreen, chosen, p, launcher.AnsiReset)
	} else {
		fmt.Fprintf(os.Stderr, "%sSaved %s as the default model.%s\n",
			launcher.AnsiGreen, chosen, launcher.AnsiReset)
	}

	// The bare name stays on stdout, as with --list: stdout carries the
	// machine-readable value so a script can capture the pick, and stderr
	// carries the sentence a person reads.
	_, _ = fmt.Fprintln(os.Stdout, chosen)
	return nil
}

// splitLaunchInvocation splits the positional args of a launch into the
// integration name and the harness passthrough. The integration name is the
// first positional; everything after it is the harness's, except that a `--`
// separator is consumed: the old parser ended flag parsing at `--` before the
// harness ever saw it, and this keeps that behaviour.
func splitLaunchInvocation(args []string) (string, []string) {
	if len(args) == 0 {
		return "", nil
	}
	extra := make([]string, 0, len(args)-1)
	for _, arg := range args[1:] {
		if arg == "--" {
			continue
		}
		extra = append(extra, arg)
	}
	return args[0], extra
}

// restoreReport is the line --restore prints, derived from what Restore did:
// a no-op on a machine prizmal never configured says so, and a removal names
// the pre-launch settings it put back.
func restoreReport(name, removed string, outcome launcher.RestoreOutcome) string {
	if !outcome.Removed {
		return fmt.Sprintf("Nothing to restore: %s has no Prizmal launch configuration.", name)
	}
	if len(outcome.Reinstated) == 0 {
		return removed
	}
	return removed + " Put back " + strings.Join(outcome.Reinstated, ", ") + "."
}

func launch(name string, extraArgs []string, cfg *config.Config) error {
	spec, err := launcher.LookupIntegrationSpec(name)
	if err != nil {
		return fmt.Errorf("%w\nRun `prizmal` to see available integrations", err)
	}

	runner := spec.Runner

	// Before anything writes a config, take any Switch key out of the
	// retained backups: a rotated or foreign key the write-time scrub cannot
	// see, or a copy of a file this build no longer touches. It runs on
	// launch, --persist and --restore alike, and says what it removed.
	for _, path := range fileutil.SweepBackups() {
		fmt.Fprintf(os.Stderr, "Removed a backup that held a Switch key: %s\n", path)
	}

	// Restore mode: undo configuration changes and exit. It never resolves a
	// model, which is why it sits above: restoring a configuration has nothing
	// to launch and must not depend on a reachable switch.
	if restore {
		r, ok := runner.(interface {
			Restore() (launcher.RestoreOutcome, error)
		})
		if !ok {
			return fmt.Errorf("%s does not support restore", spec.Name)
		}
		outcome, err := r.Restore()
		if err != nil {
			return err
		}
		// Runners name themselves for display ("Pi"); spec.Name is the
		// command word ("pi"), which only stands in when a runner has none.
		displayName := spec.Name
		if s, ok := runner.(fmt.Stringer); ok {
			displayName = s.String()
		}
		removed := displayName + " configuration restored."
		if m, ok := runner.(interface{ RestoreSuccessMessage() string }); ok {
			removed = m.RestoreSuccessMessage()
		}
		fmt.Fprintf(os.Stderr, "%s\n", restoreReport(displayName, removed, outcome))
		return nil
	}

	if err := launcher.EnsureIntegrationInstalled(spec.Name, runner); err != nil {
		return err
	}

	// Every launch runs a real model: --model, or the saved default, or a
	// choice the operator makes here. Nothing falls back to a placeholder
	// name, so an empty model is a stopped launch rather than a request the
	// switch has to interpret.
	chosen, catalog, err := resolveLaunchModel(cfg)
	if err != nil {
		return err
	}

	// Only the runners that show their own picker need the rest of the
	// catalog. pi and cline read models[0] as the launch's model, so handing
	// them every model would change the default they write.
	models := launchModels(chosen, catalog, runnerShowsModelList(runner))

	// Editor integrations persist their configuration via Edit before launch.
	if editor, ok := runner.(launcher.Editor); ok {
		if err := editor.Edit(models); err != nil {
			return fmt.Errorf("failed to configure %s: %w", spec.Name, err)
		}
		if persistOnly {
			fmt.Fprintf(os.Stderr, "%s configuration updated.%s\n", launcher.AnsiGreen, launcher.AnsiReset)
			return nil
		}
	} else if configurer, ok := runner.(launcher.Configurer); ok {
		if err := configurer.ConfigureWithModels(chosen, models); err != nil {
			return fmt.Errorf("failed to configure %s: %w", spec.Name, err)
		}
		if persistOnly {
			fmt.Fprintf(os.Stderr, "%s configuration updated.%s\n", launcher.AnsiGreen, launcher.AnsiReset)
			return nil
		}
	} else if persistOnly {
		return fmt.Errorf("%s does not support standalone configuration", spec.Name)
	}

	// Announce the key source, then gate an unauthenticated remote launch.
	// Both sit after the restore and persist-only exits: they describe the
	// launch itself, and --restore must never be gated.
	announceKeySource(os.Stderr)
	if err := requireCredentialForRemote(); err != nil {
		return err
	}

	return runner.Run(chosen, models, extraArgs)
}
