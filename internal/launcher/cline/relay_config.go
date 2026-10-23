package cline

import (
	"fmt"
	"strings"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

// writeClineRelayURLs changes only endpoint fields. The active provider and
// the act and plan mode selections stay as the launch wrote them.
func writeClineRelayURLs(dataDir, providerBaseURL, providerHost string) error {
	providersPath := clineProvidersPath(dataDir)
	providers, err := readClineConfig(providersPath)
	if err != nil {
		return err
	}
	provider, ok := activeClineProvider(providers)
	if !ok {
		return fmt.Errorf("Cline active provider is missing from %s", providersPath)
	}
	settings := clineProviderSettings(provider)
	settings["baseUrl"] = providerBaseURL
	provider["settings"] = settings
	if err := launch.WriteJSONFile0600(providersPath, providers); err != nil {
		return err
	}

	statePath := clineLegacyGlobalStatePath(dataDir)
	state, err := readClineConfig(statePath)
	if err != nil {
		return err
	}
	state["openAiCompatibleBaseUrl"] = providerHost
	for _, mode := range clineModes {
		state[mode+"OpenAiCompatibleBaseUrl"] = providerHost
	}
	return launch.WriteJSONFile0600(statePath, state)
}

// restoreClineDirectURLs replaces the loopback endpoint after Cline exits.
// Matching by URL leaves a provider the operator selected during the session
// untouched, along with every mode pointer.
func restoreClineDirectURLs(dataDir, relayBaseURL, relayHost string) error {
	providersPath := clineProvidersPath(dataDir)
	providers, err := launch.ReadJSONFile(providersPath)
	if err != nil {
		return err
	}
	if providers != nil {
		changed := false
		directBaseURL := envconfig.UpstreamOpenAIBaseURL(false)
		all, _ := providers["providers"].(map[string]any)
		for _, raw := range all {
			provider, _ := raw.(map[string]any)
			settings := clineProviderSettings(provider)
			current, _ := settings["baseUrl"].(string)
			if strings.TrimRight(current, "/") != strings.TrimRight(relayBaseURL, "/") {
				continue
			}
			settings["baseUrl"] = directBaseURL
			provider["settings"] = settings
			changed = true
		}
		if changed {
			if err := launch.WriteJSONFile0600(providersPath, providers); err != nil {
				return err
			}
		}
	}

	statePath := clineLegacyGlobalStatePath(dataDir)
	state, err := launch.ReadJSONFile(statePath)
	if err != nil {
		return err
	}
	if state == nil {
		return nil
	}
	changed := false
	directHost := envconfig.UpstreamUnversionedBaseURL()
	if strings.TrimRight(stringValue(state["openAiCompatibleBaseUrl"]), "/") == strings.TrimRight(relayHost, "/") {
		state["openAiCompatibleBaseUrl"] = directHost
		changed = true
	}
	for _, mode := range clineModes {
		key := mode + "OpenAiCompatibleBaseUrl"
		if strings.TrimRight(stringValue(state[key]), "/") != strings.TrimRight(relayHost, "/") {
			continue
		}
		state[key] = directHost
		changed = true
	}
	if changed {
		return launch.WriteJSONFile0600(statePath, state)
	}
	return nil
}

func activeClineProvider(config map[string]any) (map[string]any, bool) {
	name, _ := config["lastUsedProvider"].(string)
	if name == "" {
		name = clineApiProvider
	}
	providers, _ := config["providers"].(map[string]any)
	provider, ok := providers[name].(map[string]any)
	return provider, ok
}

func clineProviderSettings(provider map[string]any) map[string]any {
	settings, _ := provider["settings"].(map[string]any)
	if settings == nil {
		settings = make(map[string]any)
	}
	return settings
}

func stringValue(value any) string {
	s, _ := value.(string)
	return s
}
