package pi

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

// piDeviceExtensionSource is the Pi extension every launch loads. It registers
// the Switch as a provider for the life of the child, with the credential the
// launch mode calls for, and sets a turn's cost to the charge the Switch sent.
//
//go:embed pi_extension/prizmal-device.ts
var piDeviceExtensionSource []byte

// piDeviceConfigEnv names the sidecar config the launch writes and the
// extension reads. The provider id, endpoint, models and key reference live
// there rather than in the extension source, so the source is a constant and
// holds no launch data.
const piDeviceConfigEnv = "PRIZMAL_PI_CONFIG"

// piCredentialCommandEnv names the variable carrying the credential command
// the extension runs. The launch sets it to the quoted path of the running
// prizmal binary followed by "auth token", in device mode only.
const piCredentialCommandEnv = "PRIZMAL_PI_CREDENTIAL_COMMAND"

// piDeviceExtensionName is the file name the extension is written under inside
// the launch's private directory. Pi loads an extension from an explicit path
// whatever its name, so the name only has to be recognizable in a directory
// listing.
const piDeviceExtensionName = "prizmal-device.ts"

// piDeviceConfigName is the sidecar file the extension reads.
const piDeviceConfigName = "prizmal-device.json"

// piAgentDirName is the directory inside the launch directory that Pi runs
// with as its agent directory.
const piAgentDirName = "agent"

// A launch needs a Pi that loads an extension by path, registers a provider
// from it, and resolves that provider's configured apiKey per request rather
// than caching it. The oldest of those contracts to land is the per-request
// resolution, which piEnvKeyReferenceVersion is the floor for, and a launch
// updates a Pi below it before it goes on to write the extension.

// piLaunch is the directory one launch wrote for Pi: the extension, its
// sidecar config, and the agent directory Pi runs with, all removed when the
// launch returns.
type piLaunch struct {
	dir           string
	extensionPath string
	configPath    string
	agentDir      string
	device        bool
}

// piLaunchFor lays down everything one launch hands Pi, in a private directory
// removed when the launch returns.
//
// The extension ships inside the binary rather than as a Pi package so a
// launch needs no registry fetch and no npm install, and so the credential
// command it runs is exactly the binary the operator launched. A package would
// add a distribution step, a version to keep in step with the CLI, and a
// network fetch in the path of a launch that has none today.
//
// Every mode writes the sidecar, because every mode registers the provider
// from the extension: a switch-key launch names $PRIZMAL_SWITCH_KEY there,
// which the extension's apiKey refers to, and a device-mode launch names no
// key at all, because its apiKey is the credential command.
//
// The agent directory is the other half of writing nothing: Pi persists a
// /model switch, a setting and a trust decision into its agent directory, so
// the launch points it at a directory of its own that dies with the session. A
// bare pi afterwards reads the user's files, untouched.
func piLaunchFor(model string, models []launch.LaunchModel) (*piLaunch, error) {
	dir, err := os.MkdirTemp("", "prizmal-pi-")
	if err != nil {
		return nil, fmt.Errorf("create the pi launch directory: %w", err)
	}

	extensionPath := filepath.Join(dir, piDeviceExtensionName)
	if err := os.WriteFile(extensionPath, piDeviceExtensionSource, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("write the pi extension: %w", err)
	}

	userAgentDir := piUserAgentDir()
	device := envconfig.DeviceMode() && !envconfig.RelayMode()
	configPath := filepath.Join(dir, piDeviceConfigName)
	sidecar := piDeviceConfigJSON(model, models, device, piUserProviderModels(userAgentDir))
	if err := os.WriteFile(configPath, sidecar, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("write the pi extension config: %w", err)
	}

	agentDir, err := writePiAgentDir(filepath.Join(dir, piAgentDirName), userAgentDir, model)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("write the pi agent directory: %w", err)
	}

	return &piLaunch{dir: dir, extensionPath: extensionPath, configPath: configPath, agentDir: agentDir, device: device}, nil
}

// clean removes the launch directory. A failure here leaves a directory of a
// few kilobytes in the system temp directory, which is not worth failing a
// finished session over.
func (l *piLaunch) clean() {
	if l == nil || l.dir == "" {
		return
	}
	_ = os.RemoveAll(l.dir)
}

// piDeviceModel is one model entry in the sidecar config.
type piDeviceModel struct {
	ID            string `json:"id"`
	Name          string `json:"name,omitempty"`
	ContextWindow int    `json:"contextWindow"`
	MaxTokens     int    `json:"maxTokens"`
	Reasoning     bool   `json:"reasoning,omitempty"`
	Vision        bool   `json:"vision,omitempty"`
}

// piDeviceConfig is the sidecar config the extension reads.
type piDeviceConfig struct {
	ProviderID   string `json:"providerID"`
	ProviderName string `json:"providerName"`
	BaseURL      string `json:"baseURL"`
	// Headers go on every request. The sidecar lives for one launch, so it
	// holds the running version itself rather than a variable reference.
	Headers map[string]string `json:"headers"`
	// APIKey is the value the extension registers as the provider's apiKey: a
	// "$NAME" reference the child's environment resolves. A device-mode launch
	// leaves it empty, because its apiKey is the credential command.
	APIKey string          `json:"apiKey,omitempty"`
	Models []piDeviceModel `json:"models"`
}

// piUserProviderModels reads the operator's own model entries under the
// prizmal provider in their models.json. Pi replaces a provider's model list
// with the extension's when the extension registers one, so the launch carries
// these rows into the sidecar to keep them in Pi's picker.
//
// A missing file has no rows, and an unreadable one has none either: the launch
// leaves that file to the bare pi that reads it, and does not refuse to start
// over entries it was only copying.
func piUserProviderModels(userAgentDir string) []map[string]any {
	config, err := launch.ReadJSONFile(filepath.Join(userAgentDir, "models.json"))
	if err != nil || config == nil {
		return nil
	}
	providers, _ := config["providers"].(map[string]any)
	provider, ok := providers[piProviderID].(map[string]any)
	if !ok {
		return nil
	}
	entries, _ := provider["models"].([]any)
	var rows []map[string]any
	for _, entry := range entries {
		row, ok := entry.(map[string]any)
		if !ok || isManagedModel(row) {
			continue
		}
		if id, _ := row["id"].(string); id != "" {
			rows = append(rows, row)
		}
	}
	return rows
}

// piUserModelRow converts one of the operator's models.json entries into the
// sidecar's row shape, keeping the fields the sidecar carries and falling back
// to the launch's own window and output budget for the rest, the same defaults
// createConfig writes for the rows prizmal manages.
func piUserModelRow(entry map[string]any) (piDeviceModel, bool) {
	id, _ := entry["id"].(string)
	if id == "" {
		return piDeviceModel{}, false
	}
	row := piDeviceModel{ID: id, ContextWindow: piFallbackContextWindow, MaxTokens: piFallbackMaxOutputTokens}
	if name, ok := entry["name"].(string); ok {
		row.Name = name
	}
	if window, ok := entry["contextWindow"].(float64); ok && window > 0 {
		row.ContextWindow = int(window)
	}
	if maxTokens, ok := entry["maxTokens"].(float64); ok && maxTokens > 0 {
		row.MaxTokens = int(maxTokens)
	}
	if reasoning, ok := entry["reasoning"].(bool); ok {
		row.Reasoning = reasoning
	}
	if inputs, ok := entry["input"].([]any); ok {
		for _, input := range inputs {
			if name, _ := input.(string); name == "image" {
				row.Vision = true
			}
		}
	}
	return row, true
}

// piDeviceConfigJSON builds the sidecar config for a launch.
//
// The rows lead with the operator's own models under the prizmal provider, in
// their file order, and then carry every model the launch resolved. That is the
// order a --persist writes them in, so a launch's picker lists the same rows in
// the same places as a persisted config's, and the launched model leads the
// catalog rows, which is where Pi reads a bare launch's model from.
//
// Each row carries the window and the output budget, resolved the same way
// createConfig resolves them for the models.json entry, so the two sizes agree.
func piDeviceConfigJSON(model string, models []launch.LaunchModel, device bool, userModels []map[string]any) []byte {
	rows := make([]piDeviceModel, 0, len(models)+len(userModels))
	seen := make(map[string]bool, len(models)+len(userModels))
	for _, entry := range userModels {
		row, ok := piUserModelRow(entry)
		if !ok || seen[row.ID] {
			continue
		}
		seen[row.ID] = true
		rows = append(rows, row)
	}
	for _, m := range models {
		if m.Name == "" || seen[m.Name] {
			continue
		}
		seen[m.Name] = true
		rows = append(rows, piDeviceModel{
			ID:            m.Name,
			ContextWindow: piModelContextWindow(m),
			MaxTokens:     piFallbackMaxOutputTokens,
			Reasoning:     m.HasCapability("thinking"),
			Vision:        m.HasCapability("vision"),
		})
	}
	if len(models) == 0 && model != "" && !seen[model] {
		rows = append(rows, piDeviceModel{
			ID:            model,
			ContextWindow: piFallbackContextWindow,
			MaxTokens:     piFallbackMaxOutputTokens,
		})
		seen[model] = true
	}

	config := piDeviceConfig{
		ProviderID:   piProviderID,
		ProviderName: "Prizmal",
		BaseURL:      envconfig.OpenAIBaseURL(false),
		Headers:      map[string]string{envconfig.ClientVersionHeader: envconfig.ClientVersion()},
		Models:       rows,
	}
	if !device {
		// The switch-key launch's key rides the child environment, and the
		// sidecar names the variable rather than holding the key, exactly as
		// the models.json entry an older build wrote used to.
		config.APIKey = piAPIKeyReference
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		// The struct holds only strings, ints and bools, so marshalling
		// cannot fail; an empty object keeps Pi from reading a stale file.
		return []byte("{}")
	}
	return data
}
