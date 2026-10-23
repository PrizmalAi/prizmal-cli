package cline

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/fileutil"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher"
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

// Cline implements launch.Runner and Editor for the Cline CLI integration
type Cline struct{}

func (c *Cline) String() string { return "Cline" }

// SupportsDeviceMode reports that Cline can run from an enrolled device: the
// launch starts a loopback relay that refreshes the bearer for every request,
// including one refresh and replay after an upstream 401.
func (c *Cline) SupportsDeviceMode() bool { return true }

// RunsHeadless reports that Cline runs a prompt to the end unless --tui asks
// for the interface. A command word such as `auth` or `config` is not a launch
// of a model, so it is not a headless one either.
func (c *Cline) RunsHeadless(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == "-i" || arg == "--tui" {
			return false
		}
	}
	prompt := launch.FirstPositional(args, clineValueFlags)
	return prompt != "" && !slices.Contains(clineCommands, prompt)
}

// clineValueFlags are Cline's options that take a value.
var clineValueFlags = map[string]bool{
	"--id": true,
	"-P":   true, "--provider": true,
	"-k": true, "--key": true,
	"-m": true, "--model": true,
	"-s": true, "--system": true,
	"-t": true, "--timeout": true,
	"--config": true, "--data-dir": true, "--hooks-dir": true, "--cwd": true, "-c": true,
}

// clineCommands are Cline's command words. A launch that names one is asking
// Cline a question rather than running a task.
var clineCommands = []string{
	"auth", "config", "plugin", "skill", "connect", "mcp", "doctor", "history",
	"h", "hook", "schedule", "hub", "dashboard", "update", "version", "kanban",
}

func (c *Cline) Installed() bool {
	_, err := exec.LookPath("cline")
	return err == nil
}

func (c *Cline) Run(model string, _ []launch.LaunchModel, args []string) (runErr error) {
	bin, err := ClineInstaller.EnsureInstalled()
	if err != nil {
		return err
	}
	if err := c.writeArgDataDir(model, args); err != nil {
		return err
	}

	if envconfig.RelayMode() {
		dataDir, _, err := clineDataDir(args, os.Getenv, os.UserHomeDir)
		if err != nil {
			return err
		}
		relayBaseURL := envconfig.OpenAIBaseURL(false)
		relayHost := envconfig.UnversionedBaseURL()
		defer func() {
			restoreErr := restoreClineDirectURLs(dataDir, relayBaseURL, relayHost)
			if restoreErr != nil {
				runErr = errors.Join(runErr, fmt.Errorf("restore Cline endpoint after relayed session: %w", restoreErr))
			}
		}()
		if err := writeClineRelayURLs(dataDir, relayBaseURL, relayHost); err != nil {
			return err
		}
	}

	launchArgs := clineLaunchArgs(model, args)
	cmd := exec.Command(bin, launchArgs...)
	cmd.Env = c.childEnv()
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// envVars carries the key to cline on the child environment. cline's
// openai-compatible provider reads OPENAI_API_KEY when its providers.json
// settings hold no apiKey, which is how Edit writes them.
// childEnv is the environment cline runs in: the inherited environment with
// the launch's own variables in place of any inherited copy.
func (c *Cline) childEnv() []string { return c.childEnvWithKey(envconfig.APIKey()) }

func (c *Cline) childEnvWithKey(apiKey string) []string {
	return launch.ChildEnv(nil, clineEnvVars(apiKey))
}

func (c *Cline) envVars() []string {
	return clineEnvVars(envconfig.APIKey())
}

func clineEnvVars(apiKey string) []string {
	return []string{"OPENAI_API_KEY=" + apiKey}
}

// dataDir is the Cline data directory Edit, Paths and Restore use. The shared
// launch path calls Edit before Run and passes it no harness arguments, so this
// reads only CLINE_DATA_DIR, CLINE_DIR/data, then ~/.cline/data.
func (c *Cline) dataDir() (string, bool, error) {
	return clineDataDir(nil, os.Getenv, os.UserHomeDir)
}

// writeArgDataDir writes the launch files into the tree Cline will read from
// its own flags. It leaves the flags as they are, and does nothing when that
// tree is the one Edit already wrote.
func (c *Cline) writeArgDataDir(model string, args []string) error {
	if model == "" {
		return nil
	}
	want, wantRelocated, err := clineDataDir(args, os.Getenv, os.UserHomeDir)
	if err != nil {
		return err
	}
	have, _, err := c.dataDir()
	if err != nil {
		return err
	}
	if filepath.Clean(want) == filepath.Clean(have) {
		return nil
	}
	return writeClineDataDir(want, wantRelocated, model)
}

func clineDataDir(args []string, getenv func(string) string, home func() (string, error)) (string, bool, error) {
	dataDir, configDir := clinePathArgs(args)
	var dir string
	switch {
	case dataDir != "":
		dir = dataDir
	case strings.TrimSpace(getenv("CLINE_DATA_DIR")) != "":
		dir = strings.TrimSpace(getenv("CLINE_DATA_DIR"))
	case configDir != "":
		dir = filepath.Join(configDir, "data")
	case strings.TrimSpace(getenv("CLINE_DIR")) != "":
		dir = filepath.Join(strings.TrimSpace(getenv("CLINE_DIR")), "data")
	default:
		h, err := home()
		if err != nil {
			return "", false, err
		}
		return filepath.Join(h, ".cline", "data"), false, nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", false, err
	}
	return abs, true, nil
}

// clinePathArgs returns the last --data-dir and --config values before `--`.
// Other value flags are skipped with their values, so a prompt or system text
// that happens to read "--data-dir" is not mistaken for an option.
func clinePathArgs(args []string) (dataDir, configDir string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		name, value, inline := strings.Cut(arg, "=")
		if !strings.HasPrefix(name, "-") || !clineValueFlags[name] {
			continue
		}
		if !inline {
			if i+1 >= len(args) {
				break
			}
			i++
			value = args[i]
		}
		switch name {
		case "--data-dir":
			dataDir = value
		case "--config":
			configDir = value
		}
	}
	return dataDir, configDir
}

// ClineInstaller is Cline's install story. It is the plain one: npm puts the
// binary on PATH when it works, so there is no fallback directory to read and
// no per-OS branch to take. Node.js is the only thing the installer needs, on
// every platform.
var ClineInstaller = launch.Installer{
	Name:         "cline",
	DisplayName:  "Cline",
	Prompt:       "Cline is not installed. Install with npm?",
	Locate:       locateCline,
	Dependencies: ClineInstallerDependencies,
	Command:      ClineInstallerCommand,
}

// locateCline returns the cline binary when npm has put it on PATH. It returns
// the bare command name, not the path LookPath resolved, and not "not found" as
// an error to report: the exec that follows resolves the name through PATH the
// same way, and the value is what every cline launch has always exec'd.
func locateCline(string) (string, error) {
	if _, err := exec.LookPath("cline"); err != nil {
		return "", launch.ErrHarnessAbsent
	}
	return "cline", nil
}

// ClineInstallerDependencies lists what Cline's installer needs. npm installs
// cline on every platform alike, so Node.js is the whole list.
func ClineInstallerDependencies(string) []launch.Dependency {
	return []launch.Dependency{launch.NpmDependency}
}

// ClineInstallerCommand is the npm install Cline ships, on every platform.
func ClineInstallerCommand(string) (string, []string, error) {
	return "npm", []string{"install", "-g", "cline@latest"}, nil
}

func clineLaunchArgs(model string, extra []string) []string {
	return extra
}

func (c *Cline) Paths() []string {
	dataDir, _, err := c.dataDir()
	if err != nil {
		return nil
	}

	var paths []string
	for _, p := range []string{
		clineProvidersPath(dataDir),
		clineLegacyGlobalStatePath(dataDir),
	} {
		if _, err := os.Stat(p); err == nil {
			paths = append(paths, p)
		}
	}
	return paths
}

func (c *Cline) Edit(models []launch.LaunchModel) error {
	if len(models) == 0 {
		return nil
	}

	dataDir, relocated, err := c.dataDir()
	if err != nil {
		return err
	}
	return writeClineDataDir(dataDir, relocated, models[0].Name)
}

func writeClineDataDir(dataDir string, relocated bool, model string) error {
	providersPath := clineProvidersPath(dataDir)
	legacyPath := clineLegacyGlobalStatePath(dataDir)
	backup := clineBackupIntegration(dataDir, relocated)

	providersConfig, err := readClineConfig(providersPath)
	if err != nil {
		return err
	}
	legacyConfig, err := readClineConfig(legacyPath)
	if err != nil {
		return err
	}

	if err := writeClineProvidersConfigWithBackup(providersPath, providersConfig, model, backup); err != nil {
		return err
	}
	return writeClineLegacyGlobalStateWithBackup(legacyPath, legacyConfig, model, backup)
}

// clineBackupIntegration keeps a relocated tree's pre-launch copies apart from
// the default tree's and from every other relocated tree.
func clineBackupIntegration(dataDir string, relocated bool) string {
	if !relocated {
		return "cline"
	}
	sum := sha256.Sum256([]byte(filepath.Clean(dataDir)))
	return filepath.Join("cline", "data-dirs", hex.EncodeToString(sum[:8]))
}

func clineProvidersPath(dataDir string) string {
	return filepath.Join(dataDir, "settings", "providers.json")
}

func clineLegacyGlobalStatePath(dataDir string) string {
	return filepath.Join(dataDir, "globalState.json")
}

// clineProviderHost is the endpoint spelling Cline's globalState.json keys
// carry: the host, without the /v1 its providers.json entry ends with. It is
// the same unversioned spelling Claude Code's ANTHROPIC_BASE_URL needs.
func clineProviderHost() string {
	return envconfig.UnversionedBaseURL()
}

// clineProviderBaseURL is the endpoint in the Cline provider entry itself,
// which carries the version.
func clineProviderBaseURL() string {
	return envconfig.OpenAIBaseURL(false)
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
	return writeClineProvidersConfigWithBackup(configPath, config, model, "cline")
}

func writeClineProvidersConfigWithBackup(configPath string, config map[string]any, model, backup string) error {
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
	return fileutil.WriteWithBackup(configPath, data, backup)
}

func writeClineLegacyGlobalStateWithBackup(configPath string, config map[string]any, model, backup string) error {
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
	return fileutil.WriteWithBackup(configPath, data, backup)
}

func (c *Cline) Models() []string {
	dataDir, _, err := c.dataDir()
	if err != nil {
		return nil
	}

	if model := clineProviderModel(dataDir); model != "" {
		return []string{model}
	}

	config, err := fileutil.ReadJSON(clineLegacyGlobalStatePath(dataDir))
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

func clineProviderModel(dataDir string) string {
	config, err := fileutil.ReadJSON(clineProvidersPath(dataDir))
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
func (c *Cline) Restore() (launch.RestoreOutcome, error) {
	dataDir, relocated, err := c.dataDir()
	if err != nil {
		return launch.RestoreOutcome{}, err
	}
	backup := clineBackupIntegration(dataDir, relocated)

	providers, err := clineRestoreProviders(clineProvidersPath(dataDir), backup)
	if err != nil {
		return launch.RestoreOutcome{}, err
	}
	state, err := clineRestoreLegacyGlobalState(clineLegacyGlobalStatePath(dataDir), backup)
	if err != nil {
		return launch.RestoreOutcome{}, err
	}
	return providers.Join(state), nil
}

func (c *Cline) RestoreSuccessMessage() string { return clineRestoreSuccess }

func clineRestoreProviders(configPath, backup string) (launch.RestoreOutcome, error) {
	config, err := clineReadExistingConfig(configPath)
	if err != nil || config == nil {
		return launch.RestoreOutcome{}, err
	}

	providers, _ := config["providers"].(map[string]any)
	if providers == nil {
		return launch.RestoreOutcome{}, nil
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
		previous := launch.PreLaunchCopy(backup, "providers.json", clineProvidersPointAtPrizmal)
		put = launch.Reinstate(config, previous, "lastUsedProvider")
		changed = true
	}

	if !changed {
		return launch.RestoreOutcome{}, nil
	}
	config["providers"] = providers
	if err := clineWriteJSONFile(configPath, config); err != nil {
		return launch.RestoreOutcome{}, err
	}
	return launch.RestoreOutcome{Removed: true, Reinstated: put}, nil
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
func clineRestoreLegacyGlobalState(configPath, backup string) (launch.RestoreOutcome, error) {
	config, err := clineReadExistingConfig(configPath)
	if err != nil || config == nil {
		return launch.RestoreOutcome{}, err
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
		return launch.RestoreOutcome{}, nil
	}
	previous := launch.PreLaunchCopy(backup, "globalState.json", clineGlobalStatePointsAtPrizmal)
	put := launch.Reinstate(config, previous, cleared...)

	if err := clineWriteJSONFile(configPath, config); err != nil {
		return launch.RestoreOutcome{}, err
	}
	return launch.RestoreOutcome{Removed: true, Reinstated: put}, nil
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
	return launch.ReadJSONFile(configPath)
}

func clineWriteJSONFile(configPath string, config map[string]any) error {
	return launch.WriteJSONFile0600(configPath, config)
}
