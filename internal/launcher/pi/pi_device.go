package pi

import (
	_ "embed"
	"encoding/json"
	"fmt"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"os"
	"path/filepath"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
)

// piDeviceExtensionSource is the Pi extension a device-mode launch loads. It
// registers the Switch as a provider whose credential comes from this binary's
// device-token helper, which Pi runs whenever it needs the key.
//
//go:embed pi_extension/prizmal-device.ts
var piDeviceExtensionSource []byte

// piDeviceConfigEnv names the sidecar config the launch writes and the
// extension reads. The provider id, endpoint and models live there rather than
// in the extension source, so the source is a constant and holds no launch
// data.
const piDeviceConfigEnv = "PRIZMAL_PI_CONFIG"

// piCredentialCommandEnv names the variable carrying the credential command
// the extension runs. The launch sets it to the quoted path of the running
// prizmal binary followed by "auth token".
const piCredentialCommandEnv = "PRIZMAL_PI_CREDENTIAL_COMMAND"

// piDeviceExtensionName is the file name the extension is written under inside
// the launch's private directory. Pi loads an extension from an explicit path
// whatever its name, so the name only has to be recognizable in a directory
// listing.
const piDeviceExtensionName = "prizmal-device.ts"

// piDeviceConfigName is the sidecar file the extension reads.
const piDeviceConfigName = "prizmal-device.json"

// A device-mode launch needs a Pi that loads an extension by path, registers a
// provider from it, and resolves that provider's configured apiKey per request
// rather than caching it. The oldest of those contracts to land is the
// per-request resolution, which the apiKey environment reference already
// requires: piEnvKeyReferenceVersion is the floor, and EnsureInstalled
// updates a Pi below it before the launch goes on to write this extension.

// piDeviceLaunch is the directory a device-mode launch wrote the extension and
// its config into, and the paths handed to Pi.
type piDeviceLaunch struct {
	dir           string
	extensionPath string
	configPath    string
}

// writePiDeviceExtension lays down the extension and the sidecar config for
// one launch, in a private directory removed when the launch returns.
//
// The extension ships inside the binary rather than as a Pi package so a
// device-mode launch needs no registry fetch and no npm install, and so the
// credential command it runs is exactly the binary the operator launched. A
// package would add a distribution step, a version to keep in step with the
// CLI, and a network fetch in the path of a launch that has none today.
func writePiDeviceExtension(model string, models []launch.LaunchModel) (*piDeviceLaunch, error) {
	dir, err := os.MkdirTemp("", "prizmal-pi-")
	if err != nil {
		return nil, fmt.Errorf("create the pi extension directory: %w", err)
	}

	extensionPath := filepath.Join(dir, piDeviceExtensionName)
	if err := os.WriteFile(extensionPath, piDeviceExtensionSource, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("write the pi extension: %w", err)
	}

	configPath := filepath.Join(dir, piDeviceConfigName)
	if err := os.WriteFile(configPath, piDeviceConfigJSON(model, models), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("write the pi extension config: %w", err)
	}

	return &piDeviceLaunch{dir: dir, extensionPath: extensionPath, configPath: configPath}, nil
}

// clean removes the launch directory. A failure here leaves a directory of a
// few kilobytes in the system temp directory, which is not worth failing a
// finished session over.
func (l *piDeviceLaunch) clean() {
	if l == nil || l.dir == "" {
		return
	}
	_ = os.RemoveAll(l.dir)
}

// piDeviceModel is one model entry in the sidecar config.
type piDeviceModel struct {
	ID            string `json:"id"`
	ContextWindow int    `json:"contextWindow"`
	MaxTokens     int    `json:"maxTokens"`
	Reasoning     bool   `json:"reasoning,omitempty"`
	Vision        bool   `json:"vision,omitempty"`
}

// piDeviceConfig is the sidecar config the extension reads.
type piDeviceConfig struct {
	ProviderID   string          `json:"providerID"`
	ProviderName string          `json:"providerName"`
	BaseURL      string          `json:"baseURL"`
	Models       []piDeviceModel `json:"models"`
}

// piDeviceConfigJSON builds the sidecar config for a launch.
//
// The models are the ones the launch resolved, in the order the launch wrote
// them, so a device-mode launch and a switch-key launch of the same model list
// show the same rows. Pi reads models[0] as the model a bare launch selects,
// so the launched model leads.
//
// Each row carries the window and the output budget, resolved the same way
// createConfig resolves them for the models.json entry, so the two launches
// size the same model the same way.
func piDeviceConfigJSON(model string, models []launch.LaunchModel) []byte {
	rows := make([]piDeviceModel, 0, len(models))
	seen := make(map[string]bool, len(models))
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
	if len(rows) == 0 && model != "" {
		rows = append(rows, piDeviceModel{
			ID:            model,
			ContextWindow: piFallbackContextWindow,
			MaxTokens:     piFallbackMaxOutputTokens,
		})
	}

	config := piDeviceConfig{
		ProviderID:   piProviderID,
		ProviderName: "Prizmal",
		BaseURL:      envconfig.Host().String() + "/v1",
		Models:       rows,
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		// The struct holds only strings, ints and bools, so marshalling
		// cannot fail; an empty object keeps Pi from reading a stale file.
		return []byte("{}")
	}
	return data
}
