package launch

import "errors"

// This file owns the model list a runner is handed: the model the launch runs,
// carrying what the catalog knows about it, plus the tenant's other models for
// the runners that show those in their own picker. It is the launch side of the
// catalog, and the only place that decides the order and the width of that
// list. The catalog itself — the bytes, the cache, the tier aliases and the
// name-matching rule — lives in catalog.go.

// ErrNoModel is returned when a launch has no model and no way to ask for one.
var ErrNoModel = errors.New("no model selected")

// LaunchModels builds the model list a runner is handed: the model the launch
// runs, carrying the capabilities the catalog gives it, followed by the rest
// of the tenant's catalog when the runner shows those in its own picker.
//
// The launched model comes first, so a runner that reads models[0] as the
// launch's own model — pi and cline both do — still finds it there.
//
// It takes a catalog rather than fetching one: the caller has already paid for
// the fetch, through whichever catalog reader its posture calls for, and the
// list it passes here may be nil because the launch degraded. A launch that
// lost the catalog still runs the model it was given, without capabilities.
func LaunchModels(chosen string, catalog []LaunchModel, includeCatalog bool) []LaunchModel {
	models := []LaunchModel{{Name: chosen}}
	if entry, ok := findCatalogModel(catalog, chosen); ok {
		models[0].Capabilities = entry.Capabilities
		models[0].Tier = entry.Tier
		models[0].Description = entry.Description
	}
	if !includeCatalog {
		return models
	}
	for _, entry := range catalog {
		if modelNamesSame(entry.Name, chosen) {
			continue
		}
		models = append(models, entry)
	}
	return models
}
