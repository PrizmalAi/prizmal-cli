package launch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/PrizmalAi/prizmal-cli/internal/claudecode"
	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"golang.org/x/term"
)

// ErrNoModel is returned when a launch has no model and no way to ask for one.
var ErrNoModel = errors.New("no model selected")

// subagentModel holds the dedicated subagent model for this process. It is
// package state because a launch resolves its model before the runner runs,
// and a subagent model selects an environment variable the runner sets rather
// than a second launch argument.
var subagentModel string

// SetSubagentModel records the dedicated subagent model for this launch.
// main.go wires it from --subagent-model.
func SetSubagentModel(model string) { subagentModel = model }

// selectedSubagentModel returns the dedicated subagent model, or "" when
// subagents inherit the session model.
func selectedSubagentModel() string { return subagentModel }

// modelCatalog is the launch's model list, fetched at most once per launch.
//
// One launch asks the switch for the list up to three times over: to fill in
// capabilities, to offer a choice, and to build the launched harness's own
// /model rows. Those are the same list, so the fetch is cached and the second
// and third readers reuse it.
var modelCatalog struct {
	fetched bool
	models  []LaunchModel
	err     error
}

// ResetModelCatalog clears the cached catalog. Tests that point envconfig at a
// different server call it between cases. A real launch needs it only once: a
// process serves one launch, so the cache cannot go stale under it.
func ResetModelCatalog() {
	modelCatalog.fetched = false
	modelCatalog.models = nil
	modelCatalog.err = nil
}

// FetchCatalog returns the models the configured switch key can route by.
//
// It differs from the capability fill in posture on purpose. A launch degrades
// when the capability fetch fails, because a missing capability must not block
// a harness. This is the call a picker makes, and a picker cannot degrade: a
// menu with rows silently missing offers choices the tenant may not serve, and
// a harness told to show the tenant's models while showing fewer would
// misrepresent it. So the error is returned and the caller decides.
//
// The list is the tenant's, resolved by the switch from the bearer key alone.
func FetchCatalog(ctx context.Context) ([]LaunchModel, error) {
	return catalogModels(ctx)
}

// catalogModels fetches the catalog once and remembers the outcome, failure
// included, so a launch does not retry a switch that already refused it.
func catalogModels(ctx context.Context) ([]LaunchModel, error) {
	if modelCatalog.fetched {
		return modelCatalog.models, modelCatalog.err
	}
	modelCatalog.fetched = true

	catalog, err := fetchSwitchCatalog(ctx)
	if err == nil && len(catalog) == 0 {
		// The tier rows are aliases a tenant's configs hold. With nothing
		// listed, nothing holds them, so the menu would offer choices the
		// tenant does not serve.
		err = ErrNoModels
	}
	if err != nil {
		modelCatalog.err = err
		return nil, err
	}
	modelCatalog.models = withClaudeTiers(catalog)
	return modelCatalog.models, nil
}

// withClaudeTiers puts every Claude tier alias first, then the switch's own
// entries, so an operator can pick a tier or a named router config.
//
// The switch lists router configs, not the tier aliases its admin writes for
// each tier. The aliases still route, so the CLI supplies them itself. A tier
// the switch lists too appears once, in its tier's place, with the
// capabilities the switch gave it.
//
// A config the switch tags with a tier holds that tier's alias, so both names
// route to it. The tier row stands for it: the row takes the config's
// description and capabilities, and the config is folded out of the rows. The
// switch gives a tier one holder. Should it send two, only the first is folded,
// and the other keeps its own row.
func withClaudeTiers(catalog []LaunchModel) []LaunchModel {
	models := make([]LaunchModel, 0, len(claudecode.Tiers)+len(catalog))
	holders := make(map[string]bool, len(claudecode.Tiers))
	for _, tier := range claudecode.Tiers {
		name := claudecode.TierModel(tier)
		entry, ok := findSwitchCatalogModel(catalog, name)
		if !ok {
			entry = LaunchModel{Name: name}
		}
		entry.Name = name
		if i := slices.IndexFunc(catalog, func(m LaunchModel) bool { return m.Tier == string(tier) }); i >= 0 {
			holder := catalog[i]
			holders[holder.Name] = true
			if entry.Description == "" {
				entry.Description = holder.Description
			}
			if len(entry.Capabilities) == 0 {
				entry.Capabilities = holder.Capabilities
			}
		}
		models = append(models, entry)
	}
	for _, entry := range catalog {
		if slices.ContainsFunc(models[:len(claudecode.Tiers)], func(tier LaunchModel) bool {
			return launchModelMatches(entry.Name, tier.Name)
		}) {
			continue
		}
		if holders[entry.Name] {
			entry.FoldedInto = claudecode.TierModel(claudecode.Tier(entry.Tier))
		}
		models = append(models, entry)
	}
	return models
}

// BestEffortCatalog returns the tenant's models, or nil when they could not be
// read, warning once on warn when a fetch was attempted and failed.
//
// This is the launch path's reader. A launch that already has a model from
// --model or from the saved default does not depend on the list: the list only
// adds capabilities and, for a runner that shows them, picker rows. So a
// failure here costs those extras and must not cost the launch. The CLI points
// at arbitrary provider endpoints, and an OpenAI-compatible endpoint that
// serves no /v1/models is a supported target, not a broken one.
//
// With no credential it does not call the endpoint at all, which is the
// credential gate's intent: an unauthenticated launch never touches a remote
// switch, and loopback needs no key.
//
// The picker's reader is FetchCatalog, and it fails instead. A menu cannot
// degrade: rows silently missing would offer choices the tenant may not serve.
func BestEffortCatalog(ctx context.Context, warn io.Writer) []LaunchModel {
	if envconfig.APIKey() == "" {
		return nil
	}
	catalog, err := catalogModels(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(warn, "warning: %v\n", CatalogError(err))
		return nil
	}
	return catalog
}

// CatalogError describes a failed catalog fetch in one line: the endpoint, the
// source of the key the request sent, and the failure, which carries the HTTP
// status when the Switch answered. Naming the source is what lets an operator
// read a 401 as the wrong key for this host. It never names the key itself.
func CatalogError(err error) error {
	return fmt.Errorf("could not read models from %s with api key from %s: %w",
		envconfig.BaseURL(), envconfig.APIKeySource(), err)
}

// StdinIsTerminal reports whether stdin is an interactive terminal. A launch
// with no model to send can only ask for one when a person is there to answer.
//
// It is a variable so tests can drive the interactive paths without a terminal,
// in the same way ModelPickerMenu and DefaultConfirmPrompt are.
var StdinIsTerminal = func() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// LaunchModels builds the model list a runner is handed: the model the launch
// runs, carrying the capabilities the catalog gives it, followed by the rest
// of the tenant's catalog when the runner shows those in its own picker.
//
// The launched model comes first, so a runner that reads models[0] as the
// launch's own model — pi and cline both do — still finds it there.
//
// This is the launch path's only reader of the catalog, so a launch that
// fetches it once pays for it once.
func LaunchModels(chosen string, catalog []LaunchModel, includeCatalog bool) []LaunchModel {
	models := []LaunchModel{{Name: chosen}}
	if entry, ok := findSwitchCatalogModel(catalog, chosen); ok {
		models[0].Capabilities = entry.Capabilities
		models[0].Tier = entry.Tier
		models[0].Description = entry.Description
	}
	if !includeCatalog {
		return models
	}
	for _, entry := range catalog {
		if launchModelMatches(entry.Name, chosen) {
			continue
		}
		models = append(models, entry)
	}
	return models
}
