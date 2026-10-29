package launch

// This file is the Switch catalog: the tenant's models, exactly as the Switch's
// GET /v1/models serves them. Everything about those models lives here — the
// fetch, the parse, the once-per-process cache, the Claude tier aliases the CLI
// supplies because the Switch does not list them, and the one rule for deciding
// whether two model names are the same model. A caller outside this file does
// not decide any of that: it picks a reader by the posture it can live with,
// and the reader hides the rest.
//
// The three readers differ in what they do when the fetch fails, and the
// difference is deliberate rather than incidental:
//
//   - FetchCatalog fails. A menu cannot degrade: rows silently missing would
//     offer choices the tenant may not serve, and a harness told to show the
//     tenant's models while showing fewer would misrepresent it.
//   - BestEffortCatalog warns once and returns nothing. A launch that already
//     has a model does not depend on the list; the list only adds capabilities
//     and picker rows, so a failure here costs those extras and must not cost
//     the launch. The CLI points at arbitrary provider endpoints, and one that
//     serves no /v1/models is a supported target, not a broken one.
//   - ListSwitchModels fails. An empty listing is indistinguishable from "this
//     key routes to nothing", so the fetch has to reach the caller as an error
//     or the command would print a successful listing that lost its rows.
//
// One fetch serves all three, and the outcome is remembered: a launch asks the
// Switch for the list up to three times over (capabilities, the choice, the
// launched harness's own /model rows), and those are the same list.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"

	"github.com/PrizmalAi/prizmal-cli/internal/claudecode"
	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/model"
	"github.com/PrizmalAi/prizmal-cli/internal/tiers"
)

const (
	// switchCatalogTimeout bounds the capability fetch. It runs on the launch
	// path in front of the harness, so a slow Switch must cost seconds, not a
	// hung launch.
	switchCatalogTimeout = 5 * time.Second

	// switchCatalogMaxBytes caps the response read. The catalog is a handful
	// of router configs; anything larger is not one.
	switchCatalogMaxBytes = 1 << 20
)

// ModelNameWithoutSuffix strips the [1m] context-budget suffix from a model
// name and nothing else. It is the one place the decoration is spelled, because
// a name printed or sent must be the routable one: Claude Code writes the
// suffix and Switch decorates its ids with it, and a caller that rolls its own
// TrimSuffix is one edit away from trimming a different one.
//
// Every tag is deliberately kept. ":latest" is a real tag a caller can route
// by, so stripping it would print a name that resolves to a different model
// than the one listed. Deciding that two names match anyway is a separate
// question, answered by ModelNamesSame.
func ModelNameWithoutSuffix(name string) string {
	return claudecode.RoutableName(name)
}

// ModelNamesSame reports whether two model names refer to the same model,
// tolerating the :latest tag and the [1m] context-budget suffix on either
// side. It is the only name-matching rule in the launcher: a caller that
// compares names itself has to decide again whether a decoration is part of
// the name, and getting that wrong makes a launch silently lose a model's
// capabilities, tier and description.
//
// An identical pair answers first, which is the common case and also the
// identity an operator typed. Everything else falls through to the decorated
// comparison, because the Switch decorates every catalog id with the suffix
// while a launch sends the bare name, so a bare comparison never matches.
func ModelNamesSame(candidate, name string) bool {
	if candidate == name {
		return true
	}
	return strings.TrimSuffix(ModelNameWithoutSuffix(candidate), ":latest") ==
		strings.TrimSuffix(ModelNameWithoutSuffix(name), ":latest")
}

// SwitchCatalogEntry is the subset of a GET /v1/models entry the launcher
// reads. The Switch serves more per entry (name, display_name, owned_by,
// output_modalities); only the parts an entry builder or a listing
// needs are kept, and the label fields are not among them.
type SwitchCatalogEntry struct {
	ID              string   `json:"id"`
	InputModalities []string `json:"input_modalities"`
	Tier            string   `json:"tier"`
	Tiers           []string `json:"tiers"`
	Description     string   `json:"description"`
	Reasoning       bool     `json:"reasoning"`
}

// genericTiers reads the generic tiers an entry holds: every valid value of
// `tiers` when the Switch sent one, and otherwise `tier` when it names a
// generic tier. A Switch that predates `tiers` reports one tier per config.
func (e SwitchCatalogEntry) genericTiers() []tiers.Tier {
	values := e.Tiers
	if len(values) == 0 {
		values = []string{e.Tier}
	}
	var held []tiers.Tier
	for _, value := range values {
		if tier, ok := tiers.Parse(value); ok && !slices.Contains(held, tier) {
			held = append(held, tier)
		}
	}
	return held
}

// routableName is the name a caller passes back to route, which is the id with
// Claude Code's [1m] decoration removed.
//
// The id is the field to read, not name or display_name. In the OpenAI listing
// convention the id is the identifier a client sends back, and name is a label
// for people: OpenRouter answers {"id":"openai/gpt-4o-mini","name":"OpenAI:
// GPT-4o-mini"}, where the name routes nothing. This CLI points at arbitrary
// endpoints, so that shape is reachable, and only the id is routable in both.
//
// The Switch obeys the same convention and additionally sends name and
// display_name holding the undecorated spelling. It sets them by decorating the
// id, so `TrimSuffix(id, "[1m]")` is exactly the name it sends, which means
// reading the id loses nothing. The [1m] suffix is Claude Code's own
// context-budget instruction, added for the client's benefit, and it is not
// part of any model id.
func (e SwitchCatalogEntry) routableName() string {
	return ModelNameWithoutSuffix(e.ID)
}

type switchCatalogResponse struct {
	Data []SwitchCatalogEntry `json:"data"`
}

// cleanDescription keeps only the text of a description the switch sent. The
// picker draws it in the operator's terminal and Claude Code draws it in
// /model, so escape sequences and control characters are dropped, and runs of
// white space, newlines included, become one space.
func cleanDescription(desc string) string {
	desc = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, ansi.Strip(desc))
	return strings.Join(strings.Fields(desc), " ")
}

// ParseSwitchCatalog turns a GET /v1/models body into LaunchModels carrying
// only a name and the capabilities its input modalities imply.
func ParseSwitchCatalog(data []byte) ([]LaunchModel, error) {
	var catalog switchCatalogResponse
	if err := json.Unmarshal(data, &catalog); err != nil {
		return nil, fmt.Errorf("parse model catalog: %w", err)
	}
	models := make([]LaunchModel, 0, len(catalog.Data))
	for _, entry := range catalog.Data {
		name := entry.routableName()
		if name == "" {
			continue
		}
		capabilities := CapabilitiesFromModalities(entry.InputModalities)
		if entry.Reasoning {
			capabilities = append(capabilities, model.CapabilityThinking)
		}
		m := LaunchModel{
			Name:         name,
			Capabilities: capabilities,
			Tiers:        entry.genericTiers(),
			Description:  cleanDescription(entry.Description),
		}
		// A legacy Claude word is read only when the entry reports no generic
		// tier, so one config never offers the same tier under two aliases.
		if len(m.Tiers) == 0 {
			m.Tier = claudecode.KnownTier(entry.Tier)
		}
		models = append(models, m)
	}
	return models, nil
}

// switchCatalogURL builds the catalog URL for a Switch base URL, tolerating a
// base that already carries the /v1 suffix. Doubling it would ask for
// /v1/v1/models, which 404s.
func switchCatalogURL(base string) string {
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, "/v1") {
		return base + "/models"
	}
	return base + "/v1/models"
}

// fetchSwitchCatalog GETs the Switch's model catalog with the configured
// switch key as a bearer token. It is the uncached reader: every other read of
// the catalog in this file goes through cachedCatalog, which calls this once.
func fetchSwitchCatalog(ctx context.Context) ([]LaunchModel, error) {
	ctx, cancel := context.WithTimeout(ctx, switchCatalogTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		switchCatalogURL(envconfig.ConnectableHost().String()), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+envconfig.APIKey())
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// The status line only. A response body from an auth failure is not
		// somewhere to go looking for detail to print.
		return nil, fmt.Errorf("model catalog request returned %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, switchCatalogMaxBytes))
	if err != nil {
		return nil, err
	}
	return ParseSwitchCatalog(body)
}

// modelCatalog is the Switch catalog as this process fetched it: at most once
// per launch, whatever the entry point.
//
// One launch asks the switch for the list up to three times over: to fill in
// capabilities, to offer a choice, and to build the launched harness's own
// /model rows. Those are the same list, so the fetch is cached and the second
// and third readers reuse it.
//
// What is cached is the Switch's own list, not the tier aliases: the aliases
// are an offering the CLI makes to a menu, and `prizmal --list` must print
// what the tenant serves and nothing the CLI invented.
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

// cachedCatalog returns the tenant's catalog, fetched at most once per process.
// A failure is remembered along with the answer, so a launch does not retry a
// switch that already refused it.
func cachedCatalog(ctx context.Context) ([]LaunchModel, error) {
	if modelCatalog.fetched {
		return modelCatalog.models, modelCatalog.err
	}
	modelCatalog.fetched = true

	catalog, err := fetchSwitchCatalog(ctx)
	if err != nil {
		modelCatalog.err = err
		return nil, err
	}
	modelCatalog.models = catalog
	return modelCatalog.models, nil
}

// withClaudeTiers puts every tier alias first, then the switch's own entries,
// so an operator can pick a tier or a named router config.
//
// The switch lists router configs, not the tier aliases its admin writes for
// each tier. The aliases still route, so the CLI supplies them itself. A tier
// the switch lists too appears once, in its tier's place, with the
// capabilities the switch gave it.
//
// There are two vocabularies of alias. A Switch that reports generic tiers
// (frontier, core, flash, premier) gets a row per generic alias. A Switch that
// still reports the legacy Claude words gets the claude-tier-<family> rows it
// always had. ParseSwitchCatalog reads one or the other per config, so a tier
// is never offered under both.
//
// A config the switch tags with a tier holds that tier's alias, so both names
// route to it. The tier row stands for it: the row takes the config's
// description and capabilities, and the config is folded out of the rows. One
// config may hold every generic tier, and then each tier gets a row and the
// config folds into the first. The switch gives a tier one holder. Should it
// send two, only the first is folded, and the other keeps its own row.
func withClaudeTiers(catalog []LaunchModel) []LaunchModel {
	models := make([]LaunchModel, 0, len(tiers.All)+len(claudecode.Tiers)+len(catalog))
	holders := make(map[string]string, len(tiers.All)+len(claudecode.Tiers))
	takeHolder := func(entry *LaunchModel, holder LaunchModel) {
		if holders[holder.Name] == "" {
			holders[holder.Name] = entry.Name
		}
		entry.HeldBy = holder.Name
		if entry.Description == "" {
			entry.Description = holder.Description
		}
		if len(entry.Capabilities) == 0 {
			entry.Capabilities = holder.Capabilities
		}
		// The row's subtitle names the config it stands for. The config is
		// folded out of the rows, so without its name here a name `--list`
		// prints goes missing from every picker with no trace of where it went.
		entry.Description = strings.TrimSpace(holder.Name + " · " + entry.Description)
		entry.Description = strings.TrimSpace(strings.TrimSuffix(entry.Description, "·"))
	}

	for _, tier := range tiers.All {
		name := tier.Alias()
		entry, listed := FindCatalogModel(catalog, name)
		// A listed config of that name is the tier's row only when the Switch
		// says it holds the tier. Otherwise it is a config like any other.
		if listed && !slices.Contains(entry.Tiers, tier) {
			continue
		}
		i := slices.IndexFunc(catalog, func(m LaunchModel) bool {
			return slices.Contains(m.Tiers, tier) && !ModelNamesSame(m.Name, name)
		})
		if !listed && i < 0 {
			continue
		}
		entry.Name = name
		if !listed {
			entry.Tiers = []tiers.Tier{tier}
			entry.AddedTier = true
		}
		if i >= 0 {
			takeHolder(&entry, catalog[i])
		}
		models = append(models, entry)
	}

	for _, tier := range claudecode.Tiers {
		name := claudecode.TierModel(tier)
		entry, listed := FindCatalogModel(catalog, name)
		i := slices.IndexFunc(catalog, func(m LaunchModel) bool { return m.Tier == string(tier) })
		// A tier no config holds has no alias: the Switch refuses the name, so
		// no row offers it.
		if !listed && i < 0 {
			continue
		}
		entry.Name = name
		entry.AddedTier = !listed
		if i >= 0 {
			takeHolder(&entry, catalog[i])
		}
		models = append(models, entry)
	}

	tierRows := len(models)
	for _, entry := range catalog {
		if slices.ContainsFunc(models[:tierRows], func(tier LaunchModel) bool {
			return ModelNamesSame(entry.Name, tier.Name)
		}) {
			continue
		}
		entry.FoldedInto = holders[entry.Name]
		models = append(models, entry)
	}
	return models
}

// WithTierRows is the catalog as a menu reads it: the tenant's configs with
// the tier aliases they hold put first.
func WithTierRows(catalog []LaunchModel) []LaunchModel { return withClaudeTiers(catalog) }

// HoldsTier reports whether the catalog has the tier's alias, which it has
// when some config holds the tier: the generic alias the family runs on, or
// the legacy claude-tier-<family> alias.
func HoldsTier(catalog []LaunchModel, tier claudecode.Tier) bool {
	generic, hasGeneric := claudecode.GenericTier(tier)
	return slices.ContainsFunc(catalog, func(m LaunchModel) bool {
		if ModelNamesSame(m.Name, claudecode.TierModel(tier)) {
			return true
		}
		return hasGeneric && ModelNamesSame(m.Name, generic.Alias()) && slices.Contains(m.Tiers, generic)
	})
}

// CheckRoutable refuses a model name the Switch would refuse. The catalog is
// the list of names the key routes, the tier aliases the tenant holds among
// them, so a name outside it is a router config the tenant has no copy of, and
// the Switch answers it with an error instead of a model.
//
// The rule is the Switch's router-config selection, not the listing alone: a
// name routes when
//
//   - it is a name the listing carries;
//   - it is a Claude vendor id whose lineage maps onto a tier the tenant
//     holds, because the Switch rewrites `claude-<family>...` to
//     `claude-tier-<family>`. A lineage whose holder this CLI cannot read,
//     mythos, is passed: the CLI carries no tier for it, and a catalogue it
//     cannot see a holder in is no evidence of a missing one.
//
// tierWords says the harness resolves a tier word (`sonnet`, `best`,
// `opusplan`) to its own id for the tier before it sends it, and the launch
// maps that id onto the tenant's alias. Such a word routes when the tenant
// holds its tier. It does not when the tier is unheld: the launch's own
// mapping points the word at the launch model, which is the word itself.
//
// `default` and its legacy `prizmal/default` spelling are refused whatever the
// catalogue lists. They name the config the key is bound to, the Switch is
// dropping that binding, and a launch never falls back to them.
//
// An unread catalog passes everything. The launch path reads the catalog on a
// best effort, and an endpoint that serves no model list is a supported target,
// so the absence of a list is not evidence of a wrong name.
func CheckRoutable(name string, catalog []LaunchModel, tierWords bool) error {
	if name == "" {
		return nil
	}
	// The bound-config placeholder is refused whatever the catalogue says,
	// including when it could not be read: no catalogue makes the name right.
	bare := ModelNameWithoutSuffix(name)
	if IsReservedModelName(bare) {
		return fmt.Errorf("model %q names the router config the key is bound to, and the Switch is dropping that binding; "+
			"run `prizmal --list` for the names it routes", name)
	}
	if len(catalog) == 0 {
		return nil
	}
	if _, ok := FindCatalogModel(catalog, name); ok {
		return nil
	}
	if tierWords {
		if tier, ok := claudecode.TierWord(bare); ok {
			if HoldsTier(catalog, tier) {
				return nil
			}
			return fmt.Errorf("model %q runs the %s tier, which this Switch holds no router config for; "+
				"run `prizmal --list` for the names it does route", name, tier)
		}
	}
	if family := claudecode.VendorFamily(bare); family != "" {
		if !TierKnown(family) {
			return nil
		}
		if HoldsTier(catalog, claudecode.Tier(family)) {
			return nil
		}
		return fmt.Errorf("model %q is a %s id, and this Switch holds no router config for the %s tier; "+
			"run `prizmal --list` for the names it does route", name, family, family)
	}
	return fmt.Errorf("model %q is not a name this Switch routes; run `prizmal --list` for the names it does", name)
}

// TierKnown reports whether this CLI carries the tier a Switch family names.
func TierKnown(family string) bool {
	return slices.Contains(claudecode.Tiers, claudecode.Tier(family))
}

// CheckSubagentRoutable is CheckRoutable for a subagent model. A tier word is
// always a name the launch can send: it resolves through the same chain, and
// an unheld tier's word lands on the launch model, which the launch check
// already accepted.
func CheckSubagentRoutable(name string, catalog []LaunchModel, tierWords bool) error {
	if tierWords {
		if _, ok := claudecode.TierWord(ModelNameWithoutSuffix(name)); ok {
			return nil
		}
	}
	return CheckRoutable(name, catalog, tierWords)
}

// FindCatalogModel looks a model name up in a list of catalog entries, using
// the launcher's one name-matching rule. It replaces the exact-match lookup
// that used to sit in this package beside the tolerant one: two lookups for one
// question is how a name quietly stops matching.
//
// An entry that differs from the name by no more than a decoration comes back
// whole, with the capabilities, tier and description the Switch gave it. That
// matters for the launch-side lookups, where a bare fallback entry is
// indistinguishable from a model the Switch says nothing about: an unknown
// capabilities list is what makes every entry builder fall back to its
// permissive default.
func FindCatalogModel(catalog []LaunchModel, name string) (LaunchModel, bool) {
	for _, entry := range catalog {
		if ModelNamesSame(entry.Name, name) {
			return entry, true
		}
	}
	return LaunchModel{}, false
}

// menuCatalog returns the tenant's catalog with the Claude tier aliases folded
// in, and fails when the tenant lists nothing.
//
// The empty listing is a failure here and not an empty slice of four tier rows,
// because those rows are aliases a tenant's configs hold: with nothing listed,
// nothing holds them, and a menu of them would offer choices the tenant does
// not serve.
func menuCatalog(ctx context.Context) ([]LaunchModel, error) {
	catalog, err := cachedCatalog(ctx)
	if err != nil {
		return nil, err
	}
	if len(catalog) == 0 {
		return nil, ErrNoModels
	}
	return withClaudeTiers(catalog), nil
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
	return menuCatalog(ctx)
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
	catalog, err := menuCatalog(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(warn, "warning: %v\n", CatalogError(err))
		return nil
	}
	return catalog
}

// ListSwitchModels returns the names the configured switch key can route by:
// the models its tenant serves, in the order the Switch lists them.
//
// It is the non-launch counterpart to BestEffortCatalog, and it inverts that
// function's posture on purpose. A launch degrades gracefully because a failed
// capability fetch must not block a harness. A listing has no such excuse. An
// empty list is indistinguishable from "this key routes to nothing", so a
// fetch that fails returns the error and the caller fails the command.
//
// It reads the folded list a menu gets, not the Switch's raw one. The four
// Claude tier aliases route but are not rows the Switch lists, and
// `prizmal --model <tier alias> claude` launches one: a listing that omitted
// them under-reported by four names an operator could use, while the README
// promised it printed the models this key serves. One question, one answer.
//
// The bound-config placeholder the Switch lists as `default` is left out. It
// selects the config the key is bound to, the Switch is dropping that binding,
// and a launch refuses the name: printing it would offer what a launch rejects.
//
// The list is the tenant's, resolved by the Switch from the bearer key alone.
// No request parameter selects a tenant, so one key can never read another's.
func ListSwitchModels(ctx context.Context) ([]string, error) {
	catalog, err := menuCatalog(ctx)
	if err != nil {
		return nil, err
	}
	return LaunchableNames(catalog), nil
}

// LaunchableNames returns the names in catalog that a launch can send, in the
// order the list carries them. The bound-config placeholder is left out: a
// launch never sends it, so a menu that offered it or a listing that printed it
// would name something every launch refuses.
func LaunchableNames(catalog []LaunchModel) []string {
	names := make([]string, 0, len(catalog))
	for _, m := range catalog {
		if m.Name == "" || IsReservedModelName(m.Name) {
			continue
		}
		names = append(names, m.Name)
	}
	return names
}

// CatalogError describes a failed catalog fetch in one line: the endpoint, the
// source of the key the request sent, and the failure, which carries the HTTP
// status when the Switch answered. Naming the source is what lets an operator
// read a 401 as the wrong key for this host. It never names the key itself.
func CatalogError(err error) error {
	return fmt.Errorf("could not read models from %s with api key from %s: %w",
		envconfig.BaseURL(), envconfig.APIKeySource(), err)
}
