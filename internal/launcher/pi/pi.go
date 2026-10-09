package pi

import (
	"encoding/json"
	"fmt"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/fileutil"
	"golang.org/x/mod/semver"
)

// Pi implements Runner and Editor for Pi (Pi Coding Agent) integration
type Pi struct{}

const (
	piNpmPackage       = "@earendil-works/pi-coding-agent"
	piLegacyNpmPackage = "@mariozechner/pi-coding-agent"
)

// piProviderID is the provider id this integration writes into
// models.json (Edit) and removes from it (Restore), and the id the launch
// selects with --provider. pi only honors settings.json's defaultProvider
// when the provider entry passes its schema validation — an entry it
// rejects (an empty apiKey, for instance) is dropped and the launch falls
// back to pi's built-in provider — so the command must carry the
// selection explicitly rather than rely on the settings file.
const piProviderID = "prizmal"

// piAPIKeyReference is the apiKey Edit writes into the provider entry. pi
// reads a "$NAME" value from its own environment, and Run puts the key there,
// so models.json names the variable and never holds the key itself.
const piAPIKeyReference = "$" + envconfig.KeyEnvVar

// piRestoredProviderID aliases piProviderID for Restore; keeping one name
// makes a rename of either a compile error rather than a silent split.
const piRestoredProviderID = piProviderID

// piFallbackContextWindow is the window a launch declares for every model,
// because GET /v1/models carries no context length (the Switch sends id,
// input_modalities, tier and description), so nothing can learn the real one.
// Every model the Switch serves has a 1M window today. Pi budgets the session
// and compacts from this number, so pi's own default of 128000 would compact a
// session at a fraction of what the Switch accepts. $HARNESS_CONTEXT_LENGTH
// overrides it for an operator who knows better. The same reasoning and number
// as codexFallbackContextWindow.
const piFallbackContextWindow = 1_000_000

// piFallbackMaxOutputTokens is the output budget a launch declares for every
// model: the whole window, the largest output any route can take. Pi sends it
// as the request's max output tokens, capped by the room left in the window,
// so pi's own default of 16384 would cut a long answer short. The Switch caps a
// request to the limit of the route that serves it, so a high value here asks
// for the most the route allows and never exceeds it.
const piFallbackMaxOutputTokens = piFallbackContextWindow

// piModelContextWindow is the window a launch declares for one model: a length
// the model itself carries, else the window every model the Switch serves has,
// with $HARNESS_CONTEXT_LENGTH winning over both. Both pi launch paths resolve
// it through here, so a device launch and a switch-key launch agree.
func piModelContextWindow(model launch.LaunchModel) int {
	if ctxLen := envconfig.ContextLength(); ctxLen > 0 {
		return ctxLen
	}
	if model.ContextLength > 0 {
		return model.ContextLength
	}
	return piFallbackContextWindow
}

// There is no pi default model. A launch resolves its model before it reaches
// Pi — from --model, from the saved default, or from the picker — so Pi never
// substitutes a name of its own. The reserved prizmal/default placeholder it
// used to fall back on is sunset.

func (p *Pi) String() string { return "Pi" }

// SupportsDeviceMode reports that Pi can run from an enrolled device: the
// launch loads an extension whose provider resolves its credential by running
// this binary's device-token helper, so a device token that expires mid-session
// is replaced rather than sent again. A launch updates a Pi too old to load
// that extension before it gets to this point, so what this reports is what a
// launch of the installed Pi does.
func (p *Pi) SupportsDeviceMode() bool { return true }

func (p *Pi) Run(model string, models []launch.LaunchModel, args []string) error {
	fmt.Fprintf(os.Stderr, "\n%sPreparing Pi...%s\n", launch.AnsiGray, launch.AnsiReset)
	if err := ensureNpmInstalled(); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "%sChecking Pi installation...%s\n", launch.AnsiGray, launch.AnsiReset)
	bin, err := EnsureInstalled()
	if err != nil {
		return err
	}

	// A device-mode launch hands Pi an extension whose provider runs `prizmal
	// auth token` for each credential it needs, so a session that outlives one
	// device token keeps working. The launch directory holds that extension and
	// its config for the life of the child process and goes when it exits.
	extension, err := piDeviceLaunchFor(model, models)
	if err != nil {
		return err
	}
	defer extension.clean()

	fmt.Fprintf(os.Stderr, "\n%sLaunching Pi...%s\n\n", launch.AnsiGray, launch.AnsiReset)

	// A launch always arrives with a model: main resolves one from --model,
	// the saved default, or the picker before it dispatches. Pi injects no
	// name of its own, so an empty model here would select the provider's
	// own default rather than a model the operator chose.
	cmd := exec.Command(bin, piLaunchArgs(model, extension, args)...)
	cmd.Env = append(os.Environ(), p.envVars(extension)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// piDeviceLaunchFor writes the device extension and its config for a launch.
// It returns nil when the process is not in device mode, which is every
// switch-key launch: those select the provider entry Edit wrote and take the
// key from the environment as before.
func piDeviceLaunchFor(model string, models []launch.LaunchModel) (*piDeviceLaunch, error) {
	if !envconfig.DeviceMode() {
		return nil, nil
	}
	extension, err := writePiDeviceExtension(model, models)
	if err != nil {
		return nil, err
	}
	return extension, nil
}

// envVars is the environment Pi is launched with, before the inherited
// variables are merged in.
//
// A switch-key launch carries the key under the name piAPIKeyReference points
// at. A device-mode launch carries no key: the extension it loads runs
// `prizmal auth token` whenever Pi needs the credential, so the variables name
// the command that prints a fresh device token and the config that command's
// provider was built from. Nothing here holds key material, and nothing the
// Switch would take as a fixed credential reaches the child.
//
// The command is a separate prizmal process, and it resolves the Switch host
// from scratch: --url, then $PRIZMAL_SWITCH_URL, then the config file, then
// production. A launch aimed at a non-default host by --url would otherwise
// let that process fall back to production and mint a token the launch's own
// Switch cannot use, so the launch's resolved URL is pinned for the child.
func (p *Pi) envVars(extension *piDeviceLaunch) []string {
	if extension == nil {
		return []string{envconfig.KeyEnvVar + "=" + envconfig.APIKey()}
	}
	return launch.EnsureHelperBaseURL([]string{
		piCredentialCommandEnv + "=" + piCredentialCommand(),
		piDeviceConfigEnv + "=" + extension.configPath,
	}, envconfig.BaseURL())
}

func ensureNpmInstalled() error {
	if _, err := exec.LookPath("npm"); err != nil {
		return fmt.Errorf("npm (Node.js) is required to launch pi\n\nInstall it first:\n  https://nodejs.org/\n\nThen re-run:\n  prizmal pi")
	}
	return nil
}

// piLaunchArgs assembles the pi command line: the device extension when there
// is one, the prizmal provider selection, then the user's passthrough args.
//
// A device-mode launch loads its extension by path, so the provider that
// command names is registered by the extension and resolves its own credential
// per request. A switch-key launch loads nothing and relies on the provider
// entry Edit wrote.
//
// When the user's args already select a provider — --provider (pi spells it
// long-form only; -p is --print), --provider=…, or a provider-qualified --model
// like openai/gpt-5-mini — nothing is injected, because pi resolves a
// provider-qualified model against the --provider flag rather than its own
// prefix, and a duplicate flag would override the user's choice. Only
// pi's own option section is scanned: after pi's `--` separator every
// token is a file or message text, not a flag.
func piLaunchArgs(model string, extension *piDeviceLaunch, extra []string) []string {
	var args []string
	if extension != nil {
		args = append(args, "--extension", extension.extensionPath)
	}
	if userSelectsProvider(extra) {
		return append(args, extra...)
	}
	args = append(args, "--provider", piProviderID, "--model", model)
	return append(args, extra...)
}

// piCredentialCommand is the command the device extension runs for each
// credential: the quoted path of the running prizmal binary followed by its
// device-token helper. The path is quoted because pi runs the string through a
// shell, and an install path with a space would otherwise split into two words.
func piCredentialCommand() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return strconv.Quote(exe) + " auth token"
}

// userSelectsProvider reports whether the user's passthrough args already
// select a provider. Scanning stops at pi's `--` separator; pi accepts no
// short form for --provider.
func userSelectsProvider(args []string) bool {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return false
		}
		if arg == "--provider" || strings.HasPrefix(arg, "--provider=") {
			return true
		}
		if arg == "--model" && i+1 < len(args) && isProviderQualifiedModel(args[i+1]) {
			return true
		}
		if strings.HasPrefix(arg, "--model=") && isProviderQualifiedModel(strings.TrimPrefix(arg, "--model=")) {
			return true
		}
	}
	return false
}

// isProviderQualifiedModel reports whether a --model value names its
// provider explicitly (pi supports both "provider/id" and bare ids).
func isProviderQualifiedModel(value string) bool {
	_, id, ok := strings.Cut(value, "/")
	return ok && id != ""
}

func EnsureInstalled() (string, error) {
	if _, err := exec.LookPath("pi"); err == nil {
		install, pkgErr := installedPiPackageInfo()
		if pkgErr != nil {
			fmt.Fprintf(os.Stderr, "%sCould not verify which Pi package is installed: %v%s\n", launch.AnsiYellow, pkgErr, launch.AnsiReset)
			fmt.Fprintf(os.Stderr, "Pi will still launch. To switch to the official package manually:\n  npm uninstall -g %s\n  npm install -g %s\n\n", piLegacyNpmPackage, piNpmPackage)
			return "pi", nil
		}

		if install.packageName == piLegacyNpmPackage {
			fmt.Fprintf(os.Stderr, "%sUpdating Pi...%s\n", launch.AnsiGray, launch.AnsiReset)
			if err := migrateLegacyPiPackage(install.npmPrefix); err != nil {
				return "", err
			}
			if err := requirePiOnPath(); err != nil {
				return "", err
			}
		} else if install.packageName == piNpmPackage && piPredatesEnvKeyReference(install.version) {
			fmt.Fprintf(os.Stderr, "%sUpdating Pi...%s\n", launch.AnsiGray, launch.AnsiReset)
			if err := installPiPackageWithPrefix(install.npmPrefix); err != nil {
				return "", err
			}
		}
		return "pi", nil
	}

	if _, err := exec.LookPath("npm"); err != nil {
		return "", fmt.Errorf("pi is not installed and required dependencies are missing\n\nInstall the following first:\n  npm (Node.js): https://nodejs.org/\n\nThen re-run:\n  prizmal pi")
	}

	install, pkgErr := installedPiPackageInfo()
	if pkgErr == nil && install.packageName == piLegacyNpmPackage {
		fmt.Fprintf(os.Stderr, "%sUpdating Pi...%s\n", launch.AnsiGray, launch.AnsiReset)
		if err := migrateLegacyPiPackage(install.npmPrefix); err != nil {
			return "", err
		}
		if err := requirePiOnPath(); err != nil {
			return "", err
		}
		return "pi", nil
	}
	if pkgErr == nil && install.packageName == piNpmPackage {
		fmt.Fprintf(os.Stderr, "%sInstalling Pi...%s\n", launch.AnsiGray, launch.AnsiReset)
		if err := installPiPackageWithPrefix(install.npmPrefix); err != nil {
			return "", err
		}
		if err := requirePiOnPath(); err != nil {
			return "", err
		}
		return "pi", nil
	}

	ok, err := launch.ConfirmPrompt("Install Pi with npm?")
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("pi installation cancelled")
	}

	fmt.Fprintf(os.Stderr, "\nInstalling Pi...\n")
	if err := installPiPackage(); err != nil {
		return "", err
	}

	if err := requirePiOnPath(); err != nil {
		return "", err
	}

	fmt.Fprintf(os.Stderr, "%sPi installed successfully%s\n\n", launch.AnsiGreen, launch.AnsiReset)
	return "pi", nil
}

func requirePiOnPath() error {
	if _, err := exec.LookPath("pi"); err != nil {
		return fmt.Errorf("pi was installed but the binary was not found on PATH\n\nYou may need to restart your shell")
	}
	return nil
}

func installPiPackage() error {
	return installPiPackageWithPrefix("")
}

func installPiPackageWithPrefix(prefix string) error {
	if err := runQuietCommand("npm", npmArgs(prefix, "install", "-g", piNpmPackage+"@latest")...); err != nil {
		return fmt.Errorf("failed to install pi: %w", err)
	}
	return nil
}

func migrateLegacyPiPackage(prefix string) error {
	if err := installPiPackageForced(prefix); err != nil {
		return err
	}

	installed, err := npmPackageInstalledWithPrefix(piNpmPackage, prefix)
	if err != nil {
		return fmt.Errorf("failed to verify official pi package: %w", err)
	}
	if !installed {
		return fmt.Errorf("failed to verify official pi package")
	}

	if err := uninstallLegacyPiPackageWithPrefix(prefix); err != nil {
		return err
	}
	return installPiPackageWithPrefix(prefix)
}

func installPiPackageForced(prefix string) error {
	if err := runQuietCommand("npm", npmArgs(prefix, "install", "-g", piNpmPackage+"@latest", "--force")...); err != nil {
		return fmt.Errorf("failed to install pi: %w", err)
	}
	return nil
}

func uninstallLegacyPiPackageWithPrefix(prefix string) error {
	if err := runQuietCommand("npm", npmArgs(prefix, "uninstall", "-g", piLegacyNpmPackage)...); err != nil {
		return fmt.Errorf("failed to remove legacy pi package: %w", err)
	}
	return nil
}

func runQuietCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	msg := strings.TrimSpace(string(out))
	if msg == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, msg)
}

type piPackageInstall struct {
	packageName string
	npmPrefix   string
	version     string
}

// piEnvKeyReferenceVersion is the first pi release that reads a "$NAME"
// apiKey from its environment. Earlier releases send piAPIKeyReference
// itself as the key.
const piEnvKeyReferenceVersion = "v0.77.0"

// piPredatesEnvKeyReference reports whether an installed pi is too old to
// resolve piAPIKeyReference. A version it cannot parse counts as new enough,
// so an odd install is launched as it is rather than reinstalled every time.
func piPredatesEnvKeyReference(version string) bool {
	v := "v" + strings.TrimPrefix(version, "v")
	return semver.IsValid(v) && semver.Compare(v, piEnvKeyReferenceVersion) < 0
}

func installedPiPackageInfo() (piPackageInstall, error) {
	if _, err := exec.LookPath("npm"); err != nil {
		return piPackageInstall{}, err
	}

	if bin, err := exec.LookPath("pi"); err == nil {
		install, err := piPackageInstallFromBinary(bin)
		if err == nil && install.packageName != "" {
			return install, nil
		}
	}

	installed, err := npmPackageInstalled(piLegacyNpmPackage)
	if err != nil {
		return piPackageInstall{}, err
	}
	if installed {
		return piPackageInstall{packageName: piLegacyNpmPackage}, nil
	}

	installed, err = npmPackageInstalled(piNpmPackage)
	if err != nil {
		return piPackageInstall{}, err
	}
	if installed {
		return piPackageInstall{packageName: piNpmPackage}, nil
	}

	return piPackageInstall{}, nil
}

func piPackageInstallFromBinary(bin string) (piPackageInstall, error) {
	realPath, err := filepath.EvalSymlinks(bin)
	if err != nil {
		realPath = bin
	}

	dir := filepath.Dir(realPath)
	for {
		packageJSON := filepath.Join(dir, "package.json")
		data, err := os.ReadFile(packageJSON)
		if err == nil {
			var payload struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			}
			if json.Unmarshal(data, &payload) == nil && (payload.Name == piLegacyNpmPackage || payload.Name == piNpmPackage) {
				return piPackageInstall{packageName: payload.Name, npmPrefix: npmPrefixForPackageRoot(dir), version: payload.Version}, nil
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return piPackageInstall{}, nil
}

func npmPrefixForPackageRoot(packageRoot string) string {
	return npmPrefixForPackageRootForGOOS(filepath.Clean(packageRoot), runtime.GOOS, string(filepath.Separator))
}

func npmPrefixForPackageRootForGOOS(packageRoot, goos, separator string) string {
	packageRoot = strings.TrimRight(packageRoot, separator)
	nodeModules := separator + "node_modules" + separator
	idx := strings.LastIndex(packageRoot, nodeModules)
	if idx == -1 {
		return ""
	}

	rootDir := packageRoot[:idx]
	if pathBaseForSeparator(rootDir, separator) == "lib" {
		// Unix npm global root is <prefix>/lib/node_modules.
		return pathDirForSeparator(rootDir, separator)
	}
	if goos == "windows" {
		// Windows npm global root is usually <prefix>\node_modules.
		return rootDir
	}
	return ""
}

func pathBaseForSeparator(path, separator string) string {
	path = strings.TrimRight(path, separator)
	idx := strings.LastIndex(path, separator)
	if idx == -1 {
		return path
	}
	return path[idx+len(separator):]
}

func pathDirForSeparator(path, separator string) string {
	path = strings.TrimRight(path, separator)
	idx := strings.LastIndex(path, separator)
	if idx == -1 {
		return ""
	}
	if idx == 0 {
		return separator
	}
	return path[:idx]
}

func npmPackageInstalled(pkg string) (bool, error) {
	return npmPackageInstalledWithPrefix(pkg, "")
}

func npmPackageInstalledWithPrefix(pkg, prefix string) (bool, error) {
	cmd := exec.Command("npm", npmArgs(prefix, "ls", "-g", pkg, "--depth=0", "--json")...)
	out, err := cmd.Output()

	var payload struct {
		Dependencies map[string]json.RawMessage `json:"dependencies"`
	}

	if parseErr := json.Unmarshal(out, &payload); parseErr == nil {
		_, ok := payload.Dependencies[pkg]
		if ok {
			return true, nil
		}
		return false, nil
	}

	if err == nil {
		return false, nil
	}

	if exitErr, ok := err.(*exec.ExitError); ok {
		msg := strings.TrimSpace(string(exitErr.Stderr))
		if msg == "" {
			msg = strings.TrimSpace(string(out))
		}
		if msg == "" {
			return false, err
		}
		return false, fmt.Errorf("%w: %s", err, msg)
	}

	return false, err
}

func npmArgs(prefix string, args ...string) []string {
	if prefix == "" {
		return args
	}
	return append([]string{"--prefix", prefix}, args...)
}

func (p *Pi) Paths() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	var paths []string
	modelsPath := filepath.Join(home, ".pi", "agent", "models.json")
	if _, err := os.Stat(modelsPath); err == nil {
		paths = append(paths, modelsPath)
	}
	settingsPath := filepath.Join(home, ".pi", "agent", "settings.json")
	if _, err := os.Stat(settingsPath); err == nil {
		paths = append(paths, settingsPath)
	}
	return paths
}

func (p *Pi) Edit(models []launch.LaunchModel) error {
	if len(models) == 0 {
		return nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	configPath := filepath.Join(home, ".pi", "agent", "models.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return err
	}

	config := make(map[string]any)
	if data, err := os.ReadFile(configPath); err == nil {
		_ = json.Unmarshal(data, &config)
	}

	providers, ok := config["providers"].(map[string]any)
	if !ok {
		providers = make(map[string]any)
	}

	piProvider, ok := configProvider(providers, piProviderID)
	if !ok {
		piProvider = map[string]any{
			"baseUrl": envconfig.Host().String() + "/v1",
			"api":     "openai-completions",
			"apiKey":  piAPIKeyReference,
		}
	} else {
		// Re-assert the current endpoint and key reference, so a provider
		// an older build wrote with the key itself loses it.
		piProvider["baseUrl"] = envconfig.Host().String() + "/v1"
		piProvider["api"] = "openai-completions"
		piProvider["apiKey"] = piAPIKeyReference
	}

	existingModels, ok := piProvider["models"].([]any)
	if !ok {
		existingModels = make([]any, 0)
	}

	// Build set of selected models to track which need to be added
	selectedSet := make(map[string]bool, len(models))
	for _, m := range models {
		selectedSet[m.Name] = true
	}

	// Build new models list:
	// 1. Keep user-managed models (no _launch marker) - untouched
	// 2. Keep launch-managed models (_launch marker) that are still selected,
	//    except stale cloud entries that should be rebuilt below
	// 3. Add new launch-managed models
	var newModels []any
	for _, m := range existingModels {
		if modelObj, ok := m.(map[string]any); ok {
			if id, ok := modelObj["id"].(string); ok {
				// User-managed model (no _launch marker) - always preserve
				if !isManagedModel(modelObj) {
					newModels = append(newModels, m)
				} else if selectedSet[id] {
					// Rebuild stale managed cloud entries so createConfig refreshes
					// the whole entry instead of patching it in place.
					if !hasContextWindow(modelObj) {
						if _, ok := launch.LookupCloudModelLimit(id); ok {
							continue
						}
					}
					newModels = append(newModels, m)
					selectedSet[id] = false
				}
			}
		}
	}

	// Add newly selected models that weren't already in the list
	for _, model := range models {
		if selectedSet[model.Name] {
			newModels = append(newModels, createConfig(model))
		}
	}

	piProvider["models"] = newModels
	providers[piProviderID] = piProvider
	config["providers"] = providers

	configData, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	if err := fileutil.WriteWithBackup(configPath, configData, "pi"); err != nil {
		return err
	}

	// Update settings.json with default provider and model
	settingsPath := filepath.Join(home, ".pi", "agent", "settings.json")
	settings := make(map[string]any)
	if data, err := os.ReadFile(settingsPath); err == nil {
		_ = json.Unmarshal(data, &settings)
	}

	settings["defaultProvider"] = piProviderID
	settings["defaultModel"] = models[0].Name

	settingsData, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.WriteWithBackup(settingsPath, settingsData, "pi")
}

// piRestoreSuccess is printed after a successful `prizmal --restore pi`.
const piRestoreSuccess = "Pi launch configuration removed."

// Restore removes everything Pi.Edit added: the prizmal provider entry in
// models.json (where builds before the key moved to the environment wrote
// the key itself) and the default provider and
// model it repointed in settings.json, which go back to what the user had
// before the first launch. Providers and settings prizmal does not manage
// are left exactly as they were.
//
// The writes here deliberately bypass fileutil.WriteWithBackup. That helper
// copies the file it is about to overwrite into ~/.prizmal/backup, which would
// deposit the key being removed one directory over.
func (p *Pi) Restore() (launch.RestoreOutcome, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return launch.RestoreOutcome{}, err
	}

	models, err := piRestoreModels(filepath.Join(home, ".pi", "agent", "models.json"))
	if err != nil {
		return launch.RestoreOutcome{}, err
	}
	settings, err := piRestoreSettings(filepath.Join(home, ".pi", "agent", "settings.json"))
	if err != nil {
		return launch.RestoreOutcome{}, err
	}
	return models.Join(settings), nil
}

func (p *Pi) RestoreSuccessMessage() string { return piRestoreSuccess }

func piRestoreModels(configPath string) (launch.RestoreOutcome, error) {
	config, err := piReadJSONFile(configPath)
	if err != nil || config == nil {
		return launch.RestoreOutcome{}, err
	}

	providers, ok := config["providers"].(map[string]any)
	if !ok {
		return launch.RestoreOutcome{}, nil
	}
	if _, ok := providers[piRestoredProviderID]; !ok {
		return launch.RestoreOutcome{}, nil
	}
	delete(providers, piRestoredProviderID)
	config["providers"] = providers

	if err := piWriteJSONFile(configPath, config); err != nil {
		return launch.RestoreOutcome{}, err
	}
	return launch.RestoreOutcome{Removed: true}, nil
}

// piRestoreSettings puts defaultProvider and defaultModel back to what the
// user had before the first launch, read from the backup Edit's write took.
// With no such copy left, the two keys go, so nothing points at the provider
// entry piRestoreModels just deleted.
func piRestoreSettings(settingsPath string) (launch.RestoreOutcome, error) {
	settings, err := piReadJSONFile(settingsPath)
	if err != nil || settings == nil {
		return launch.RestoreOutcome{}, err
	}

	if !piSettingsPointAtPrizmal(settings) {
		return launch.RestoreOutcome{}, nil
	}
	delete(settings, "defaultProvider")
	delete(settings, "defaultModel")
	previous := launch.PreLaunchCopy("pi", "settings.json", piSettingsPointAtPrizmal)
	put := launch.Reinstate(settings, previous, "defaultProvider", "defaultModel")

	if err := piWriteJSONFile(settingsPath, settings); err != nil {
		return launch.RestoreOutcome{}, err
	}
	return launch.RestoreOutcome{Removed: true, Reinstated: put}, nil
}

func piSettingsPointAtPrizmal(settings map[string]any) bool {
	provider, _ := settings["defaultProvider"].(string)
	return provider == piRestoredProviderID
}

// piReadJSONFile returns nil, nil when the file is absent — there is nothing
// to restore — and an error only when it exists but cannot be used.
func piReadJSONFile(path string) (map[string]any, error) {
	return launch.ReadJSONFile(path)
}

func piWriteJSONFile(path string, config map[string]any) error {
	return launch.WriteJSONFile0600(path, config)
}

func (p *Pi) Models() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	configPath := filepath.Join(home, ".pi", "agent", "models.json")
	config, err := fileutil.ReadJSON(configPath)
	if err != nil {
		return nil
	}

	providers, _ := config["providers"].(map[string]any)
	piProvider, _ := configProvider(providers, piProviderID)
	models, _ := piProvider["models"].([]any)

	var result []string
	for _, m := range models {
		if modelObj, ok := m.(map[string]any); ok {
			if id, ok := modelObj["id"].(string); ok {
				result = append(result, id)
			}
		}
	}
	slices.Sort(result)
	return result
}

// isManagedModel reports whether a model config entry is managed by prizmal
func isManagedModel(cfg map[string]any) bool {
	if v, ok := cfg["_launch"].(bool); ok && v {
		return true
	}
	return false
}

// configProvider returns the provider map for the given id.
func configProvider(providers map[string]any, id string) (map[string]any, bool) {
	m, ok := providers[id].(map[string]any)
	return m, ok
}

func hasContextWindow(cfg map[string]any) bool {
	switch v := cfg["contextWindow"].(type) {
	case float64:
		return v > 0
	case int:
		return v > 0
	case int64:
		return v > 0
	default:
		return false
	}
}

// createConfig builds Pi model config with capability detection.
func createConfig(model launch.LaunchModel) map[string]any {
	cfg := map[string]any{
		"id":      model.Name,
		"_launch": true,
	}

	// Set input types based on vision capability
	if model.HasCapability("vision") {
		cfg["input"] = []string{"text", "image"}
	} else {
		cfg["input"] = []string{"text"}
	}

	// Set reasoning based on thinking capability
	if model.HasCapability("thinking") {
		cfg["reasoning"] = true
	}

	// A launch states the window rather than leaving it to pi's default. Every
	// model the Switch serves has a 1M window, and GET /v1/models carries no
	// context length, so nothing here can learn the real one. Pi budgets and
	// compacts from the declared window, so pi's own 128000 default would
	// compact a session at a fraction of what the Switch accepts. A window the
	// model itself carries wins, and $HARNESS_CONTEXT_LENGTH, the operator's
	// own statement, wins over both.
	cfg["contextWindow"] = piModelContextWindow(model)
	cfg["maxTokens"] = piFallbackMaxOutputTokens

	return cfg
}
