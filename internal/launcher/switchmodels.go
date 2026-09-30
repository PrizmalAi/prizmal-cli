package launch

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

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/model"
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

// switchCatalogEntry is the subset of a GET /v1/models entry the launcher
// reads. The Switch serves more per entry (name, display_name, owned_by,
// reasoning, output_modalities); only the parts an entry builder or a listing
// needs are kept, and the label fields are not among them.
type switchCatalogEntry struct {
	ID              string   `json:"id"`
	InputModalities []string `json:"input_modalities"`
	Tier            string   `json:"tier"`
	Description     string   `json:"description"`
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
//
// The :latest tag is deliberately left on. It is a real tag a caller can route
// by, so stripping it would print a name that resolves to a different model
// than the one listed.
func (e switchCatalogEntry) routableName() string {
	return strings.TrimSuffix(e.ID, oneMillionSuffix)
}

type switchCatalogResponse struct {
	Data []switchCatalogEntry `json:"data"`
}

// modalityCapabilities maps the Switch's input-modality vocabulary onto the
// launcher's.
//
// "text" maps to CapabilityCompletion so that a model the Switch calls
// text-only still comes back with a non-empty capability list. That is what
// separates "the Switch says text-only" from "the Switch said nothing": the
// second leaves the list empty, and every entry builder reads an empty list as
// unknown. Nothing else in the tree reads CapabilityCompletion, so the mapping
// carries no behavior of its own.
//
// "file" is the Switch's document modality, the one that says a router config
// can take a PDF. It is handed out independently of "image": the catalogue has
// rows with both, and rows with image and no file.
//
// "video" is served by the Switch and deliberately missing here: it has no
// capability in the vocabulary and no entry builder has anywhere to put it.
var modalityCapabilities = map[string]model.Capability{
	"text":  model.CapabilityCompletion,
	"image": model.CapabilityVision,
	"file":  model.CapabilityDocument,
	"audio": model.CapabilityAudio,
}

// capabilitiesFromModalities translates one entry's input_modalities array.
// An unrecognised modality is dropped rather than guessed at.
func capabilitiesFromModalities(modalities []string) []model.Capability {
	var capabilities []model.Capability
	for _, modality := range modalities {
		c, ok := modalityCapabilities[strings.ToLower(strings.TrimSpace(modality))]
		if !ok || slices.Contains(capabilities, c) {
			continue
		}
		capabilities = append(capabilities, c)
	}
	return capabilities
}

// knownTier returns the tier the Switch named, lowercased, or "" for a name
// that is not one of the Claude tiers.
func knownTier(tier string) string {
	tier = strings.ToLower(strings.TrimSpace(tier))
	if _, ok := tierProfiles[modelTier(tier)]; ok {
		return tier
	}
	return ""
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

// parseSwitchCatalog turns a GET /v1/models body into LaunchModels carrying
// only a name and the capabilities its input modalities imply.
func parseSwitchCatalog(data []byte) ([]LaunchModel, error) {
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
		models = append(models, LaunchModel{
			Name:         name,
			Capabilities: capabilitiesFromModalities(entry.InputModalities),
			Tier:         knownTier(entry.Tier),
			Description:  cleanDescription(entry.Description),
		})
	}
	return models, nil
}

// ListSwitchModels returns the names the configured switch key can route by:
// the models its tenant serves, in the order the Switch lists them.
//
// It is the non-launch counterpart to WithSwitchCapabilities, and it inverts
// that function's posture on purpose. A launch degrades gracefully because a
// failed capability fetch must not block a harness. A listing has no such
// excuse. An empty list is indistinguishable from "this key routes to nothing",
// so a fetch that fails returns the error and the caller fails the command.
//
// The list is the tenant's, resolved by the Switch from the bearer key alone.
// No request parameter selects a tenant, so one key can never read another's.
func ListSwitchModels(ctx context.Context) ([]string, error) {
	catalog, err := fetchSwitchCatalog(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(catalog))
	for _, m := range catalog {
		if m.Name != "" {
			names = append(names, m.Name)
		}
	}
	return names, nil
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
// switch key as a bearer token.
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
	return parseSwitchCatalog(body)
}

// findSwitchCatalogModel looks a launch model's name up in the catalog,
// tolerating the implicit :latest tag the way the rest of the launcher does.
func findSwitchCatalogModel(catalog []LaunchModel, name string) (LaunchModel, bool) {
	for _, entry := range catalog {
		if launchModelMatches(entry.Name, name) {
			return entry, true
		}
	}
	return LaunchModel{}, false
}

// WithSwitchCapabilities fills in each model's capabilities from the Switch's
// GET /v1/models catalog, which is the only place the CLI learns what a router
// config can actually take as input.
//
// It is best effort by design. With no credential it does not call the
// endpoint at all, and on any failure it writes one warning line to warn and
// returns the models untouched, so a launch never fails because the capability
// fetch failed. A model the catalog does not list keeps empty capabilities,
// which the entry builders read as unknown rather than as text-only.
//
// The warning names the endpoint and the failure. It must never name the key.
func WithSwitchCapabilities(ctx context.Context, models []LaunchModel, warn io.Writer) []LaunchModel {
	if len(models) == 0 || envconfig.APIKey() == "" {
		return models
	}

	catalog, err := fetchSwitchCatalog(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(warn, "warning: could not read model capabilities from %s: %v\n", envconfig.BaseURL(), err)
		return models
	}

	populated := slices.Clone(models)
	for i, m := range populated {
		if entry, ok := findSwitchCatalogModel(catalog, m.Name); ok {
			populated[i].Capabilities = entry.Capabilities
		}
	}
	return populated
}
