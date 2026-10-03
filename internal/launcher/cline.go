package launch

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/fileutil"
)

// clineApiProvider is the Cline provider type we register as. Cline's
// OpenAI Compatible provider accepts any OpenAI-compatible endpoint with a
// base URL + API key + model id, so we use it instead of the Ollama provider
// type.
const clineApiProvider = "openai-compatible"

// clineLegacyProvider is the key earlier versions of this code stored the
// provider entry under. Cline resolves the active provider as
// providers[lastUsedProvider] and only accepts its own known provider ids, so
// "prizmal" is never a valid selection. It is kept for reading existing
// installs and is migrated away on the next write.
const clineLegacyProvider = "prizmal"

// Cline implements Runner and Editor for the Cline CLI integration
type Cline struct{}

func (c *Cline) String() string { return "Cline" }

func (c *Cline) Installed() bool {
	_, err := exec.LookPath("cline")
	return err == nil
}

func (c *Cline) Run(model string, _ []LaunchModel, args []string) error {
	bin, err := ensureClineInstalled()
	if err != nil {
		return err
	}

	launchArgs := clineLaunchArgs(model, args)
	cmd := exec.Command(bin, launchArgs...)
	cmd.Env = append(os.Environ(), c.envVars()...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// envVars carries the key to cline on the child environment. cline's
// openai-compatible provider reads OPENAI_API_KEY when its providers.json
// settings hold no apiKey, which is how Edit writes them.
func (c *Cline) envVars() []string {
	return []string{"OPENAI_API_KEY=" + envconfig.APIKey()}
}

func ensureClineInstalled() (string, error) {
	if _, err := exec.LookPath("cline"); err == nil {
		return "cline", nil
	}

	if _, err := exec.LookPath("npm"); err != nil {
		return "", fmt.Errorf("cline is not installed and required dependencies are missing\n\nInstall the following first:\n  npm (Node.js): https://nodejs.org/\n\nThen re-run:\n  prizmal cline")
	}

	ok, err := ConfirmPrompt("Cline is not installed. Install with npm?")
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("cline installation cancelled")
	}

	fmt.Fprintf(os.Stderr, "\nInstalling Cline...\n")
	cmd := exec.Command("npm", "install", "-g", "cline@latest")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to install cline: %w", err)
	}

	if _, err := exec.LookPath("cline"); err != nil {
		return "", fmt.Errorf("cline was installed but the binary was not found on PATH\n\nYou may need to restart your shell")
	}

	fmt.Fprintf(os.Stderr, "%sCline installed successfully%s\n\n", ansiGreen, ansiReset)
	return "cline", nil
}

func clineLaunchArgs(model string, extra []string) []string {
	return extra
}

func (c *Cline) Paths() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	var paths []string
	for _, p := range []string{
		clineProvidersPath(home),
		clineLegacyGlobalStatePath(home),
	} {
		if _, err := os.Stat(p); err == nil {
			paths = append(paths, p)
		}
	}
	return paths
}

func (c *Cline) Edit(models []LaunchModel) error {
	if len(models) == 0 {
		return nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	providersPath := clineProvidersPath(home)
	legacyPath := clineLegacyGlobalStatePath(home)

	providersConfig, err := readClineConfig(providersPath)
	if err != nil {
		return err
	}
	legacyConfig, err := readClineConfig(legacyPath)
	if err != nil {
		return err
	}

	if err := writeClineProvidersConfig(providersPath, providersConfig, models[0].Name); err != nil {
		return err
	}
	return writeClineLegacyGlobalState(legacyPath, legacyConfig, models[0].Name)
}

func clineProvidersPath(home string) string {
	return filepath.Join(home, ".cline", "data", "settings", "providers.json")
}

func clineLegacyGlobalStatePath(home string) string {
	return filepath.Join(home, ".cline", "data", "globalState.json")
}

func clineProviderHost() string {
	return strings.TrimRight(envconfig.ConnectableHost().String(), "/")
}

func clineProviderBaseURL() string {
	return clineProviderHost() + "/v1"
}

func readClineConfig(configPath string) (map[string]any, error) {
	config := make(map[string]any)
	if data, err := os.ReadFile(configPath); err == nil {
		if err := json.Unmarshal(data, &config); err != nil {
			return nil, fmt.Errorf("failed to parse config: %w, at: %s", err, configPath)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return config, nil
}

func writeClineProvidersConfig(configPath string, config map[string]any, model string) error {
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return err
	}

	providers, _ := config["providers"].(map[string]any)
	if providers == nil {
		providers = make(map[string]any)
	}

	// Cline selects the active provider as providers[lastUsedProvider], so the
	// entry has to live under the same key we set lastUsedProvider to. Carry a
	// legacy-keyed entry over rather than dropping its metadata.
	provider, _ := providers[clineApiProvider].(map[string]any)
	if provider == nil {
		provider, _ = providers[clineLegacyProvider].(map[string]any)
	}
	if provider == nil {
		provider = make(map[string]any)
	}
	previous, _ := provider["settings"].(map[string]any)

	baseURL := clineProviderBaseURL()
	previousModel, _ := previous["model"].(string)
	previousBaseURL, _ := previous["baseUrl"].(string)
	previousTokenSource, _ := provider["tokenSource"].(string)

	// Replace the settings map rather than mutating it. cline honours a
	// `headers` record on this entry and puts it on every request, so a user's
	// own headers would otherwise travel to the Switch alongside the key. The
	// same replacement drops an apiKey an older build wrote: the key now
	// reaches cline on the environment (envVars). The entry object around
	// `settings` needs no such care: cline parses it as
	// {settings, updatedAt, tokenSource} and drops the rest.
	settings := map[string]any{
		"provider": clineApiProvider,
		"model":    model,
		"baseUrl":  baseURL,
	}
	provider["settings"] = settings

	if previousModel != model || previousBaseURL != baseURL || previousTokenSource != "manual" {
		provider["updatedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
	} else if _, ok := provider["updatedAt"].(string); !ok {
		provider["updatedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
	}
	provider["tokenSource"] = "manual"
	providers[clineApiProvider] = provider
	// Never leave the key material behind under a key cline cannot select.
	delete(providers, clineLegacyProvider)

	config["version"] = float64(1)
	config["lastUsedProvider"] = clineApiProvider
	config["providers"] = providers

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.WriteWithBackup(configPath, data, "cline")
}

func writeClineLegacyGlobalState(configPath string, config map[string]any, model string) error {
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return err
	}

	baseURL := clineProviderHost()
	config["openAiCompatibleBaseUrl"] = baseURL
	config["actModeApiProvider"] = clineApiProvider
	config["actModeOpenAiCompatibleModelId"] = model
	config["actModeOpenAiCompatibleBaseUrl"] = baseURL
	config["planModeApiProvider"] = clineApiProvider
	config["planModeOpenAiCompatibleModelId"] = model
	config["planModeOpenAiCompatibleBaseUrl"] = baseURL

	config["welcomeViewCompleted"] = true

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.WriteWithBackup(configPath, data, "cline")
}

func (c *Cline) Models() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	if model := clineProviderModel(home); model != "" {
		return []string{model}
	}

	config, err := fileutil.ReadJSON(clineLegacyGlobalStatePath(home))
	if err != nil {
		return nil
	}

	switch config["actModeApiProvider"] {
	case clineApiProvider, clineLegacyProvider:
	default:
		return nil
	}

	modelID, _ := config["actModeOpenAiCompatibleModelId"].(string)
	if modelID == "" {
		return nil
	}
	return []string{modelID}
}

func clineProviderModel(home string) string {
	config, err := fileutil.ReadJSON(clineProvidersPath(home))
	if err != nil {
		return ""
	}
	if lastUsed := config["lastUsedProvider"]; lastUsed != clineApiProvider && lastUsed != clineLegacyProvider {
		return ""
	}
	providers, _ := config["providers"].(map[string]any)
	provider, _ := providers[clineApiProvider].(map[string]any)
	if provider == nil {
		provider, _ = providers[clineLegacyProvider].(map[string]any)
	}
	if provider == nil {
		return ""
	}
	settings, _ := provider["settings"].(map[string]any)
	model, _ := settings["model"].(string)
	return model
}

// clineRestoreSuccess is printed after a successful `prizmal --restore cline`.
const clineRestoreSuccess = "Cline launch configuration removed."

// Restore removes what Cline.Edit added, starting with the live key in
// providers.json — cline, like pi, has no environment channel for a custom
// provider, so the credential has to sit in its config while the harness runs
// and `--restore` is the way back off the disk.
//
// "Managed" is decided from the base URL, not the provider key: the entry
// lives under cline's own "openai-compatible" id, which a user may well be
// using for an endpoint of their own. Only an entry pointing at the Switch is
// prizmal's to touch, and an entry that still carries anything beyond what
// Edit writes keeps everything except the key and base URL.
//
// The writes here deliberately bypass fileutil.WriteWithBackup, for the same
// reason Pi.Restore does: that helper copies the file it is about to overwrite
// into ~/.prizmal/backup, which would deposit the key being removed one
// directory over.
func (c *Cline) Restore() (RestoreOutcome, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return RestoreOutcome{}, err
	}

	providers, err := clineRestoreProviders(clineProvidersPath(home))
	if err != nil {
		return RestoreOutcome{}, err
	}
	state, err := clineRestoreLegacyGlobalState(clineLegacyGlobalStatePath(home))
	if err != nil {
		return RestoreOutcome{}, err
	}
	return providers.join(state), nil
}

func (c *Cline) RestoreSuccessMessage() string { return clineRestoreSuccess }

func clineRestoreProviders(configPath string) (RestoreOutcome, error) {
	config, err := clineReadExistingConfig(configPath)
	if err != nil || config == nil {
		return RestoreOutcome{}, err
	}

	providers, _ := config["providers"].(map[string]any)
	if providers == nil {
		return RestoreOutcome{}, nil
	}

	changed := false
	removed := make(map[string]bool)

	// Cline can never select the legacy key, so only prizmal ever wrote it.
	if _, ok := providers[clineLegacyProvider]; ok {
		delete(providers, clineLegacyProvider)
		removed[clineLegacyProvider] = true
		changed = true
	}

	if entry, ok := providers[clineApiProvider].(map[string]any); ok && clineManagedEntry(entry) {
		if clinePrizmalShapedEntry(entry) {
			delete(providers, clineApiProvider)
			removed[clineApiProvider] = true
		} else {
			settings, _ := entry["settings"].(map[string]any)
			delete(settings, "apiKey")
			delete(settings, "baseUrl")
		}
		changed = true
	}

	// Only replace the selection when it names an entry that is now gone;
	// a surviving entry is the user's own choice to keep. It goes back to
	// the provider selected before the first launch, read from the backup
	// Edit's write took, and is simply cleared when no such copy is left.
	var put []string
	if lastUsed, _ := config["lastUsedProvider"].(string); removed[lastUsed] {
		delete(config, "lastUsedProvider")
		previous := preLaunchCopy("cline", "providers.json", clineProvidersPointAtPrizmal)
		put = reinstate(config, previous, "lastUsedProvider")
		changed = true
	}

	if !changed {
		return RestoreOutcome{}, nil
	}
	config["providers"] = providers
	if err := clineWriteJSONFile(configPath, config); err != nil {
		return RestoreOutcome{}, err
	}
	return RestoreOutcome{Removed: true, Reinstated: put}, nil
}

// clineProvidersPointAtPrizmal reports whether a providers.json selects an
// entry prizmal wrote: the legacy key, which only prizmal ever used, or the
// openai-compatible entry when it points at the Switch.
func clineProvidersPointAtPrizmal(config map[string]any) bool {
	lastUsed, _ := config["lastUsedProvider"].(string)
	if lastUsed == clineLegacyProvider {
		return true
	}
	if lastUsed != clineApiProvider {
		return false
	}
	providers, _ := config["providers"].(map[string]any)
	entry, _ := providers[clineApiProvider].(map[string]any)
	return clineManagedEntry(entry)
}

// clineManagedEntry reports whether a providers.json entry is one prizmal
// wrote, judged by the endpoint it points at.
func clineManagedEntry(entry map[string]any) bool {
	settings, _ := entry["settings"].(map[string]any)
	baseURL, _ := settings["baseUrl"].(string)
	return baseURL != "" && strings.TrimRight(baseURL, "/") == clineProviderBaseURL()
}

// clinePrizmalShapedEntry reports whether an entry holds nothing but the
// fields Cline.Edit writes. Anything extra belongs to the user — Edit adopts a
// pre-existing entry rather than replacing it — so the entry stays and only
// the credential fields come out.
func clinePrizmalShapedEntry(entry map[string]any) bool {
	settings, ok := entry["settings"].(map[string]any)
	if !ok {
		return false
	}
	for key := range settings {
		switch key {
		case "provider", "model", "baseUrl", "apiKey":
		default:
			return false
		}
	}
	for key := range entry {
		switch key {
		case "settings", "updatedAt", "tokenSource":
		default:
			return false
		}
	}
	return true
}

// clineRestoreLegacyGlobalState puts the mode pointers Cline.Edit aimed at
// the Switch back to what the user had before the first launch, read from
// the backup Edit's write took. They hold no key, but leaving them behind
// points cline at an endpoint whose credential has just been removed, so
// with no pre-launch copy left they are cleared. welcomeViewCompleted stays:
// it is the user's own onboarding state, not a route to prizmal.
func clineRestoreLegacyGlobalState(configPath string) (RestoreOutcome, error) {
	config, err := clineReadExistingConfig(configPath)
	if err != nil || config == nil {
		return RestoreOutcome{}, err
	}

	host := clineProviderHost()
	var cleared []string

	if clineManagedBaseURL(config["openAiCompatibleBaseUrl"], host) {
		delete(config, "openAiCompatibleBaseUrl")
		cleared = append(cleared, "openAiCompatibleBaseUrl")
	}

	for _, mode := range clineModes {
		if !clineModePointsAtPrizmal(config, mode, host) {
			continue
		}
		for _, key := range clineModeKeys(mode) {
			delete(config, key)
		}
		cleared = append(cleared, clineModeKeys(mode)...)
	}

	if len(cleared) == 0 {
		return RestoreOutcome{}, nil
	}
	previous := preLaunchCopy("cline", "globalState.json", clineGlobalStatePointsAtPrizmal)
	put := reinstate(config, previous, cleared...)

	if err := clineWriteJSONFile(configPath, config); err != nil {
		return RestoreOutcome{}, err
	}
	return RestoreOutcome{Removed: true, Reinstated: put}, nil
}

var clineModes = []string{"actMode", "planMode"}

// clineModeKeys names the three globalState.json keys Cline.Edit writes for
// one mode.
func clineModeKeys(mode string) []string {
	return []string{mode + "ApiProvider", mode + "OpenAiCompatibleModelId", mode + "OpenAiCompatibleBaseUrl"}
}

// clineModePointsAtPrizmal reports whether one mode's pointers select the
// Switch: a provider prizmal writes, at the Switch's own base URL.
func clineModePointsAtPrizmal(config map[string]any, mode, host string) bool {
	provider, _ := config[mode+"ApiProvider"].(string)
	if provider != clineApiProvider && provider != clineLegacyProvider {
		return false
	}
	return clineManagedBaseURL(config[mode+"OpenAiCompatibleBaseUrl"], host)
}

// clineGlobalStatePointsAtPrizmal reports whether any pointer in a
// globalState.json selects the Switch.
func clineGlobalStatePointsAtPrizmal(config map[string]any) bool {
	host := clineProviderHost()
	if clineManagedBaseURL(config["openAiCompatibleBaseUrl"], host) {
		return true
	}
	for _, mode := range clineModes {
		if clineModePointsAtPrizmal(config, mode, host) {
			return true
		}
	}
	return false
}

func clineManagedBaseURL(value any, host string) bool {
	url, _ := value.(string)
	return url != "" && strings.TrimRight(url, "/") == host
}

// clineReadExistingConfig returns nil, nil when the file is absent — there is
// nothing to restore — and an error only when it exists but cannot be used.
func clineReadExistingConfig(configPath string) (map[string]any, error) {
	return readJSONFile(configPath)
}

func clineWriteJSONFile(configPath string, config map[string]any) error {
	return writeJSONFile0600(configPath, config)
}
