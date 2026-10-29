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
	"slices"
	"strings"
	"time"

	"github.com/PrizmalAi/prizmal-cli/internal/config"
	"github.com/PrizmalAi/prizmal-cli/internal/device"
	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/fileutil"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	launcher "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/registry"
	"github.com/PrizmalAi/prizmal-cli/internal/relay"
	"github.com/PrizmalAi/prizmal-cli/internal/tiers"
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
	relayFlag            bool
	apiFlag              string
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
--api-key, --relay, --yes, --list, --restore, --persist. Every argument after the
integration name is handed to the harness unchanged, so claude --resume
<session-id> and any other harness flag needs no separator and can never
be read as a prizmal flag.

--relay starts a loopback relay for the launch. The harness receives a random
placeholder credential, and the relay sends the launch's real credential to the
Switch. It stops when the harness exits. Cline and Kilo Code always run through it: Cline so its
/model picker can list the tenant's names, Kilo Code so a device login
refreshes per request.

--list answers a different question: it prints the models this switch key's
tenant serves, one per line, and exits without launching. It holds exactly the
names that key can route to: the Claude tier aliases your tenant holds first,
since those route even though the Switch does not list them, then the tenant's
own entries.


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
			if relayFlag && listFlag {
				return fmt.Errorf("--relay cannot be used with --list: --list answers a query without launching a harness")
			}
			if relayFlag && (restore || persistOnly) {
				return fmt.Errorf("--relay only applies to a launch and cannot be used with --restore or --persist")
			}
			if apiFlag != "" && len(args) == 0 {
				return fmt.Errorf("--api needs an integration to launch")
			}
			if relayFlag && len(args) == 0 {
				return fmt.Errorf("--relay needs an integration to launch")
			}
			// --api is checked before anything that prompts or installs, so a
			// name the harness does not speak is refused on a machine that has
			// not installed the harness too. A name prizmal does not know falls
			// through to launch(), which reports it.
			if apiFlag != "" {
				if spec, err := registry.LookupIntegrationSpec(args[0]); err == nil {
					if err := launcher.SelectAPI(spec.Name, spec.Runner, apiFlag); err != nil {
						return err
					}
				}
			}

			// A launch checks for a newer release first. --list, --pick alone,
			// --restore and --persist answer a question, and the device
			// subcommands feed another program, so none of them ask.
			if len(args) > 0 && !listFlag && !restore && !persistOnly {
				if err := checkForUpdate(); err != nil {
					return err
				}
			}

			// A harness that is missing, or too old to serve a launch, stops
			// the launch here, before the sign-in and the model pick that
			// would otherwise come first and be wasted. A name prizmal does
			// not know falls through to launch(), which reports it.
			if len(args) > 0 && !listFlag && !restore && !persistOnly {
				launcher.SetConfirmPolicy(yes)
				if err := ensureHarnessUsable(args[0]); err != nil {
					return err
				}
			}

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

			// Device mode: an enrolled device key becomes the credential when
			// its refresh succeeds, outranking a switch key from the
			// environment or the config file. An explicit --api-key is the
			// operator naming a credential for this launch, so it suppresses
			// device mode. When the device key exists but its refresh fails,
			// enterDeviceMode falls back to the key this block already
			// resolved, with a warning, rather than aborting a launch that
			// has a working credential in hand.
			//
			// It runs only for a command that needs a credential — --list, a
			// pick, or a launch — and only when device login applies to it:
			// a launch of a harness with no refresh contract ignores device
			// login entirely and runs on the key this block already resolved.
			// A bare `prizmal` lists the integrations and must not refresh a
			// token, or hit the network, to print a table.
			if apiKey == "" && (listFlag || pickFlag || len(args) > 0) && deviceLoginApplies(listFlag, args) {
				if err := enterDeviceMode(); err != nil {
					return err
				}
			}

			// --list answers a question rather than launching, so it runs
			// before the integration dispatch and ignores the integration
			// word: `prizmal --list claude` lists the tenant's models and
			// never touches the harness.
			if listFlag {
				return listTenantModels()
			}

			// ask is how this launch reaches the operator: the real terminal
			// probe and the real picker menu. It is stated once, where the
			// launch is made, so the two interactive decisions a launch can
			// take are readable together and neither is left implicit in a
			// package variable a caller may already have replaced.
			ask := launcher.AskOperator()

			// --pick alone is a mode: it chooses a model, saves it, and exits
			// without launching, because no integration was named to launch.
			// With an integration named it is a launch flag instead, so
			// `prizmal --pick claude` chooses a model and starts Claude Code
			// with it.
			if pickFlag && len(args) == 0 {
				return pickDefaultModel(cfg, ask)
			}

			if len(args) == 0 {
				return listIntegrations()
			}
			launcher.SetConfirmPolicy(yes)
			launcher.SetSubagentModel(subagentModel)
			integration, extraArgs := splitLaunchInvocation(args)
			return launch(integration, extraArgs, cfg, ask)
		},
	}

	registerFlags(root.Flags())

	// `prizmal login` and `prizmal auth token` sit beside the launch command.
	// They are the only two things that need the device key, and they run
	// without a model, a catalog or a harness.
	root.AddCommand(deviceCommands()...)

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
	flags.StringVar(&subagentModel, "subagent-model", "", "model for subagents, which default to the session model (Claude Code and Codex)")
	flags.StringVarP(&url, "url", "u", "", "provider base URL (or $"+envconfig.EnvVar+")")
	flags.StringVarP(&apiKey, "api-key", "k", "", "provider API key (or $"+envconfig.KeyEnvVar+")")
	flags.BoolVar(&relayFlag, "relay", false, "route the launched harness through a per-session loopback relay")
	flags.StringVar(&apiFlag, "api", "", "wire API the harness speaks to the Switch: chat, responses or anthropic (default: the harness's own)")
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
// A tenant that lists nothing is not a failure. The catalog reader reports it
// as ErrNoModels because a picker must not offer choices the tenant does not
// serve, and that same error reaches a listing whose answer is a valid empty
// one. So it is caught here and turned into the message and the exit 0 that
// answer means, rather than into CatalogError's red error line.
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
	if errors.Is(err, launcher.ErrNoModels) {
		// Silence would read as a successful listing that lost its rows.
		fmt.Fprintf(os.Stderr, "No models: this switch key's tenant serves none.\n")
		return nil
	}
	if err != nil {
		return launcher.CatalogError(err)
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
	specs := registry.ListVisibleIntegrationSpecs()
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
// point Edit has already written the config that needs the capabilities. It
// honours the gate's intent instead, by only
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
// last pick saved, then the picker. A launch that cannot ask stops.
//
// --pick skips the saved default: asking to choose means choosing, not reusing,
// which is what makes `prizmal --pick claude` open the menu on a machine that
// already has a default.
//
// Nothing saves a default unless --pick is passed or the operator uses the
// picker's save shortcut. A pick taken in a plain launch runs that launch and
// is forgotten, and --model never writes.
//
// It returns the launch's model list too, which is the model plus, for the
// runners that show one, the rest of the catalog. Only the picker path needs
// the list to succeed: a launch that already has its model treats an unreadable
// list as lost capabilities and lost picker rows, not as a failed launch. The
// CLI points at arbitrary provider endpoints, and one that serves no
// /v1/models is a supported target.
//
// cfg may be nil on a machine with no config file, such as one that signed in
// with a device key. Saving a default then creates the file.
//
// harnessModel is the model taken from a --model written after the integration
// name, which is empty for a runner that does not own that flag. It ranks with
// the --model flag: it is consulted first and overrides the saved default
// exactly as the flag does.
//
// ask is how the launch reaches the operator when nothing else names a model:
// whether a person is there to answer, and the menu that asks them. It is a
// parameter rather than a package variable so the launch says which it uses,
// and so a test states its own without restoring anything afterwards.
//
// headless says the harness runs one prompt and exits, so there is no session
func resolveLaunchModel(cfg *config.Config, harnessModel string, ask launcher.Asking, headless bool) (string, []launcher.LaunchModel, error) {
	return resolveLaunchModelFor(cfg, harnessModel, ask, headless, tiers.Generic)
}

// resolveLaunchModelFor is resolveLaunchModel for a launch whose picker shows
// vocab's rows: the tier words of the harness it launches, or the router
// configs alone for a harness without tier words.
func resolveLaunchModelFor(cfg *config.Config, harnessModel string, ask launcher.Asking, headless bool, vocab tiers.Vocabulary) (string, []launcher.LaunchModel, error) {
	ctx := context.Background()

	switch {
	case model != "":
		return model, launcher.BestEffortCatalog(ctx, os.Stderr), nil

	case harnessModel != "":
		return harnessModel, launcher.BestEffortCatalog(ctx, os.Stderr), nil

	case cfg != nil && cfg.DefaultModel != "" && !pickFlag && !launcher.IsReservedModelName(cfg.DefaultModel):
		return cfg.DefaultModel, launcher.BestEffortCatalog(ctx, os.Stderr), nil
	}

	// --pick is the operator asking for the menu, and nothing else can answer
	// for them, so it is refused without a terminal.
	if pickFlag && !ask.CanAsk() {
		return "", nil, fmt.Errorf(
			"--pick needs an interactive terminal: pass --model MODEL instead, or run it on a terminal")
	}

	// Nothing names a model and nobody can be asked. A scripted run, or one
	// whose harness prints and exits, cannot open a menu, so it stops with the
	// names it can choose from. A person at a terminal still gets the menu.
	if !pickFlag && (headless || !ask.CanAsk()) {
		return "", nil, noModelSelectedError(ctx)
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

	picked, err := ask.Pick(launcher.ModelRowsFor(catalog, vocab))
	if err != nil {
		return "", nil, err
	}

	// A pick taken in a plain launch is for that launch only. It becomes the
	// default when the operator asked for that: --pick, or the picker's save
	// shortcut.
	if pickFlag || picked.Save {
		if err := saveDefaultModel(cfg, picked.Model); err != nil {
			return "", nil, err
		}
	}
	return picked.Model, catalog, nil
}

// saveDefaultModel writes model as the default. Without a loaded config it
// reads the file on disk and changes only default_model, so no other field is
// lost. A machine with no file gets a new one: a device-login machine has none
// because the device key is its credential, so the new file holds the version
// and the default alone, with base_url and api_key omitted when empty. Any
// other read error fails the save and leaves the file as it was.
func saveDefaultModel(cfg *config.Config, model string) error {
	if cfg == nil {
		loaded, err := config.Load()
		switch {
		case err == nil:
			cfg = loaded
		case errors.Is(err, config.ErrNoConfig):
			cfg = &config.Config{}
		default:
			return fmt.Errorf("could not read the config to save the default model: %w", err)
		}
	}
	cfg.DefaultModel = model
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("could not save the default model: %w", err)
	}
	return nil
}

// noModelSelectedError names the ways to give a launch a model, and the models
// this key routes. The CLI never sends `default`, the bound-config
// placeholder, so a launch with nothing to run stops here instead. When the
// list cannot be read, the error says where to read it.
func noModelSelectedError(ctx context.Context) error {
	msg := "no model selected: pass --model MODEL, run --pick to choose one, or set default_model in the config file"
	catalog, err := launcher.FetchCatalog(ctx)
	if err != nil {
		return fmt.Errorf("%s; run `prizmal --list` for the names this key routes", msg)
	}
	if names := launcher.LaunchableNames(catalog); len(names) > 0 {
		return fmt.Errorf("%s; this key routes: %s", msg, strings.Join(names, ", "))
	}
	return fmt.Errorf("%s; run `prizmal --list` for the names this key routes", msg)
}

// pickDefaultModel opens the picker, saves the choice as the default, and
// returns without launching anything.
//
// It shares the credential gate and the catalog fetch with a launch, and gates
// before the fetch for the reason --list does: a remote switch with no key
// answers 401, and the operator reads that as a rejected key rather than a
// missing one.
//
// A machine with no config file gets one, holding the default alone.
//
// ask is the launch's way of reaching the operator, carried from the command
// that started it: whether a person is there to answer, and the menu that asks
// them.
func pickDefaultModel(cfg *config.Config, ask launcher.Asking) error {
	// The menu needs a person. Refusing up front beats opening a full-screen list on a
	// pipe, which would read whatever arrives on stdin as keystrokes.
	if !ask.CanAsk() {
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

	picked, err := ask.Pick(launcher.ModelRows(catalog))
	if err != nil {
		return err
	}
	chosen := picked.Model

	if err := saveDefaultModel(cfg, chosen); err != nil {
		return err
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
// first positional; everything after it is the harness's. A `--` separator is
// kept, because takeModelFlag reads it: a --model after it is the harness's.
// launch removes the separators with dropSeparators before dispatch.
func splitLaunchInvocation(args []string) (string, []string) {
	if len(args) == 0 {
		return "", nil
	}
	return args[0], slices.Clone(args[1:])
}

// withConfiguredArgs adds the arguments the config's harnesses section holds
// for the integration, except the flags the launch already carries. cfg may be
// nil on a machine with no config file.
func withConfiguredArgs(cfg *config.Config, name string, extra []string) ([]string, error) {
	if cfg == nil {
		return extra, nil
	}
	configured, err := launcher.SplitArgs(cfg.Harnesses[name].Args)
	if err != nil {
		return nil, fmt.Errorf("%s: harnesses.%s.args: %w", config.PrizmalFileName, name, err)
	}
	return launcher.MergeMissingFlags(extra, configured), nil
}

// dropSeparators removes every `--` separator from the harness arguments. The
// old parser ended flag parsing at `--` before the harness ever saw it, and
// this keeps that behaviour.
func dropSeparators(extra []string) []string {
	out := make([]string, 0, len(extra))
	for _, arg := range extra {
		if arg != "--" {
			out = append(out, arg)
		}
	}
	return out
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

// takeModelFlag removes every --model from the harness arguments and returns
// the values it found, in order. It rewrites extra in place, so the caller's
// slice is the harness's argument list with the model flags gone.
//
// prizmal owns the model decision: it resolves the model from --model, the
// saved default, or the picker, and hands the harness that value. A --model
// the operator typed after the integration name is the same question asked in a
// second place, so it is answered here rather than forwarded. Forwarding it let
// the harness's own flag outrank the settings prizmal wrote, which is how a
// launch lost its 1M window and warned that the model was unknown.
//
// -m is prizmal's short form of --model, so it is taken the same way. Tokens
// after a `--` separator are harness text, not prizmal's flags, so the scan
// stops there. A trailing --model or -m with no value is left for the harness to
// reject: the operator aimed it at the harness, not at prizmal.
func takeModelFlag(extra *[]string) []string {
	var found []string
	args := *extra
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--":
			out = append(out, args[i:]...)
			*extra = out
			return found
		case args[i] == "--model" || args[i] == "-m":
			if i+1 == len(args) {
				out = append(out, args[i])
				break
			}
			found = append(found, args[i+1])
			i++
		case strings.HasPrefix(args[i], "--model="):
			found = append(found, strings.TrimPrefix(args[i], "--model="))
		default:
			out = append(out, args[i])
		}
	}
	*extra = out
	return found
}

// reconcileModel settles the model when both prizmal's --model and a --model
// after the integration name are present.
//
// Two different names are two answers to one question, and silently picking the
// first is how a launch runs a model the operator did not ask for. One name,
// written once or twice, is a single decision and proceeds.
func reconcileModel(flagModel string, harnessModels []string) (string, error) {
	if len(harnessModels) == 0 {
		return flagModel, nil
	}
	harness := harnessModels[0]
	for _, m := range harnessModels[1:] {
		if m != harness {
			return "", fmt.Errorf(
				"conflicting --model: %q and %q name different models; pass one model, before or after the integration name",
				harness, m)
		}
	}
	if flagModel != "" && flagModel != harness {
		return "", fmt.Errorf(
			"conflicting --model: %q before the integration name and %q after it name different models; pass one model",
			flagModel, harness)
	}
	return harness, nil
}

// ensureHarnessUsable installs the named harness when it is missing and checks
// that the installed one can serve a launch. An unknown name is left for
// launch() to report.
func ensureHarnessUsable(name string) error {
	spec, err := registry.LookupIntegrationSpec(name)
	if err != nil {
		return nil
	}
	return registry.EnsureIntegrationInstalled(spec.Name, spec.Runner)
}

// launch configures the named integration and hands it to the harness.
//
// ask is how it reaches the operator when nothing else names a model. It is
// threaded from the command that started the launch so the launch names the way
// it asks, rather than reaching for a package variable.
func launch(name string, extraArgs []string, cfg *config.Config, ask launcher.Asking) error {
	spec, err := registry.LookupIntegrationSpec(name)
	if err != nil {
		return fmt.Errorf("%w\nRun `prizmal` to see available integrations", err)
	}

	runner := spec.Runner
	if err := launcher.SelectAPI(spec.Name, runner, apiFlag); err != nil {
		return err
	}

	// A first-run browser sign-in can leave device mode on before any harness
	// was named. A runner without a refresh contract must use its ordinary key
	// sources instead, so device mode cannot outrank a switch key it already
	// holds. (launch itself never enters device mode; the enterDeviceMode gate
	// in the root command applies the same rule when a harness is named.)
	if envconfig.DeviceMode() && !launcher.SupportsDeviceMode(runner) {
		envconfig.SetDeviceMode(false)
	}

	// Before anything writes a config, take any Switch key out of the
	// retained backups: a rotated or foreign key the write-time scrub cannot
	// see, or a copy of a file this build no longer touches. It runs on
	// launch, --persist and --restore alike, and says what it removed.
	for _, path := range fileutil.SweepBackups() {
		fmt.Fprintf(os.Stderr, "Removed a backup that held a Switch key: %s\n", path)
	}

	// --subagent-model has a meaning for Claude Code and Codex only. A harness
	// without a subagent setting would ignore it and run a model the operator
	// did not mean, so refuse it.
	if subagentModel != "" && !restore {
		if sub, ok := runner.(launcher.SubagentModelRunner); !ok || !sub.TakesSubagentModel() {
			return fmt.Errorf("--subagent-model is not supported for %s: it applies to Claude Code and Codex", spec.Name)
		}
	}

	// Restore mode: undo configuration changes and exit. It never resolves a
	// model, which is why it sits above: restoring a configuration has nothing
	// to launch and must not depend on a reachable switch.
	if restore {
		r, ok := runner.(launcher.Restorer)
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
		if m, ok := runner.(launcher.RestoreMesager); ok {
			removed = m.RestoreSuccessMessage()
		}
		fmt.Fprintf(os.Stderr, "%s\n", restoreReport(displayName, removed, outcome))
		return nil
	}

	if err := registry.EnsureIntegrationInstalled(spec.Name, runner); err != nil {
		return err
	}

	// prizmal owns the model decision for a runner that opts in, so a --model
	// the operator typed after the integration name becomes prizmal's own flag.
	// It is consumed before the harness is dispatched, and it takes the rank the
	// flag form has: above the saved default, and equal to a --model written
	// before the integration name. Two different names are refused rather than
	// silently losing.
	//
	// A runner that does not opt in keeps the flag: pi reads a
	// provider-qualified --model as a provider choice, so consuming it would
	// change which provider runs.
	harnessModel := ""
	if owner, ok := runner.(launcher.OwningModelFlag); ok && owner.OwnsModelFlag() {
		if harnessModels := takeModelFlag(&extraArgs); len(harnessModels) > 0 {
			harnessModel, err = reconcileModel(model, harnessModels)
			if err != nil {
				return err
			}
		}
	}
	extraArgs = dropSeparators(extraArgs)
	if extraArgs, err = withConfiguredArgs(cfg, spec.Name, extraArgs); err != nil {
		return err
	}

	// Every launch runs a real model: the model flag in either position, the
	// saved default, or a choice the operator makes here. A harness run that
	// prints its answer and exits has no session for a menu to start, so it
	// never asks, and a launch with no model stops with the names it can use.
	headless := launcher.RunsHeadless(runner, extraArgs)
	vocab := launcher.HarnessVocabulary(runner)
	chosen, catalog, err := resolveLaunchModelFor(cfg, harnessModel, ask, headless, vocab)
	if err != nil {
		return err
	}

	// A name copied off a picker row reads without the claude- prefix the
	// row's label drops, and the catalog carries the prefixed name that
	// routes. A Codex row's word (sol) becomes the tier's alias (frontier),
	// because the Switch routes the alias and no Codex word. A name the
	// catalog carries is left as it is.
	chosen = launcher.RouteNameFor(catalog, chosen, vocab)
	subagentModel = launcher.RouteNameFor(catalog, subagentModel, vocab)

	// A name the Switch does not route is refused here, with the way to list
	// the names, instead of reaching the Switch as a request it answers with an
	// error. The subagent model is a name the launch sends too.
	tierWords := launcher.ResolvesTierWords(runner)
	if err := launcher.CheckRoutable(chosen, catalog, tierWords); err != nil {
		return err
	}
	if err := launcher.CheckSubagentRoutable(subagentModel, catalog, tierWords); err != nil {
		return fmt.Errorf("--subagent-model: %w", err)
	}

	// Only the runners that show their own picker need the rest of the
	// catalog. pi and cline read models[0] as the launch's model, so handing
	// them every model would change the default they write.
	models := launchModels(chosen, catalog, runnerShowsModelList(runner))

	if persistOnly {
		editor, ok := runner.(launcher.Editor)
		if !ok {
			return fmt.Errorf("%s does not support standalone configuration", spec.Name)
		}
		if err := editor.Edit(models); err != nil {
			return fmt.Errorf("failed to configure %s: %w", spec.Name, err)
		}
		fmt.Fprintf(os.Stderr, "%s configuration updated.%s\n", launcher.AnsiGreen, launcher.AnsiReset)
		return nil
	}

	// Announce the key source, then gate an unauthenticated remote launch.
	// Both sit after the restore and persist-only exits: they describe the
	// launch itself, and --restore must never be gated.
	announceKeySource(os.Stderr)
	if err := requireCredentialForRemote(); err != nil {
		return err
	}

	// A device's sign-in deadline is worth a warning before it arrives, so the
	// operator re-approves on their own time. Claude Code, Codex and Pi run in
	// device mode, and the warning belongs on the launch it affects.
	if envconfig.DeviceMode() {
		if ct, err := device.LoadCachedToken(); err == nil {
			if w := device.ReauthWarning(ct.ReauthBy, time.Now()); w != "" {
				fmt.Fprintf(os.Stderr, "%s%s%s\n", launcher.AnsiYellow, w, launcher.AnsiReset)
			}
		}
	}

	if shouldUseRelay(spec.Name) {
		var relayOpts []relay.Option
		if spec.Name == "cline" {
			// Cline's /model picker fetches the list with no Authorization
			// header, so the relay answers that one request itself.
			relayOpts = append(relayOpts, relay.WithUnauthenticatedModelList())
		}
		r, err := newLaunchRelay(envconfig.DeviceMode(), envconfig.APIKey(), relayOpts...)
		if err != nil {
			return err
		}
		envconfig.SetRelay(r.URL(), r.Placeholder())
		defer func() {
			envconfig.ClearRelay()
			r.Close()
		}()
		fmt.Fprintf(os.Stderr, "relaying through %s; the harness holds a per-launch placeholder\n", r.URL())
	}

	// Editor integrations persist their configuration via Edit before launch.
	// Relay mode is set first, so an endpoint written here is the loopback URL.
	if editor, ok := runner.(launcher.Editor); ok {
		if err := editor.Edit(models); err != nil {
			return fmt.Errorf("failed to configure %s: %w", spec.Name, err)
		}
	}

	return runner.Run(chosen, models, extraArgs)
}

func shouldUseRelay(integration string) bool {
	return relayFlag || integration == "cline" || integration == "kilo"
}
