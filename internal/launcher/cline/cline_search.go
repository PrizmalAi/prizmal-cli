package cline

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/fileutil"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

// Cline's own web tools never reach the Switch, so the launch registers an
// MCP server of its own. Cline loads stdio servers from
// <data-dir>/settings/cline_mcp_settings.json and offers each tool to the
// model as <server>__<tool>, so this one is offered as prizmal_search__search.
const (
	clineSearchServerName = "prizmal_search"
	clineSearchToolName   = "search"
	clineMCPSettingsFile  = "cline_mcp_settings.json"

	// The search server reads its target and model from these variables.
	// The key is never among them: the server inherits Cline's environment,
	// where the launch puts it as OPENAI_API_KEY.
	clineSearchURLEnv   = "PRIZMAL_CLINE_SEARCH_URL"
	clineSearchModelEnv = "PRIZMAL_CLINE_SEARCH_MODEL"
)

func clineMCPSettingsPath(dataDir string) string {
	return filepath.Join(dataDir, "settings", clineMCPSettingsFile)
}

// clineSearchCommand is the binary and arguments the MCP entry runs: this
// prizmal binary's hidden `mcp search` subcommand, so search needs nothing
// beyond prizmal itself.
func clineSearchCommand() (string, []any, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", nil, err
	}
	return exe, []any{"mcp", "search"}, nil
}

// removeLegacyClineSearchServer deletes the Node server an earlier prizmal
// generated. The entry no longer points at it, so it is only clutter.
func removeLegacyClineSearchServer() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	_ = os.Remove(filepath.Join(home, ".cache", "prizmal", "cline", clineSearchServerName+".mjs"))
}

// clineSearchEntry is the mcpServers entry the launch writes.
func clineSearchEntry(command string, args []any, model string) map[string]any {
	return map[string]any{
		"transport": map[string]any{
			"type":    "stdio",
			"command": command,
			"args":    args,
			"env": map[string]any{
				clineSearchURLEnv:   envconfig.OpenAIBaseURL(false),
				clineSearchModelEnv: model,
			},
		},
	}
}

// writeClineMCPSettings merges prizmal's entry into the MCP settings file and
// leaves every other server as it was. A prizmal_search entry already there is
// replaced; Restore puts a pre-launch one back from the backup.
func writeClineMCPSettings(dataDir, model, backup string) error {
	command, args, err := clineSearchCommand()
	if err != nil {
		return err
	}
	path := clineMCPSettingsPath(dataDir)
	config, err := readClineConfig(path)
	if err != nil {
		return err
	}
	servers, _ := config["mcpServers"].(map[string]any)
	if servers == nil {
		servers = make(map[string]any)
	}
	servers[clineSearchServerName] = clineSearchEntry(command, args, model)
	config["mcpServers"] = servers

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.WriteWithBackup(path, data, backup)
}

// clineSearchEntryIsPrizmal reports whether an mcpServers entry is one the
// launch wrote, judged by the variable only prizmal sets on it.
func clineSearchEntryIsPrizmal(entry any) bool {
	m, _ := entry.(map[string]any)
	transport, _ := m["transport"].(map[string]any)
	env, _ := transport["env"].(map[string]any)
	_, ok := env[clineSearchURLEnv]
	return ok
}

// clineMCPPointsAtPrizmal reports whether an MCP settings copy carries the
// launch's entry, so a backup taken after a launch is never read as the
// pre-launch copy.
func clineMCPPointsAtPrizmal(config map[string]any) bool {
	servers, _ := config["mcpServers"].(map[string]any)
	return clineSearchEntryIsPrizmal(servers[clineSearchServerName])
}

// clineRestoreMCPSettings takes prizmal's entry out of the MCP settings. A
// prizmal_search entry the user had before the first launch comes back from
// the backup. With no pre-launch copy, prizmal created the file, so it goes
// when nothing but an empty mcpServers is left in it.
func clineRestoreMCPSettings(path, backup string) (launch.RestoreOutcome, error) {
	config, err := clineReadExistingConfig(path)
	if err != nil || config == nil {
		return launch.RestoreOutcome{}, err
	}
	servers, _ := config["mcpServers"].(map[string]any)
	if !clineSearchEntryIsPrizmal(servers[clineSearchServerName]) {
		return launch.RestoreOutcome{}, nil
	}
	delete(servers, clineSearchServerName)

	previous := launch.PreLaunchCopy(backup, clineMCPSettingsFile, clineMCPPointsAtPrizmal)
	var put []string
	if before, _ := previous["mcpServers"].(map[string]any); before != nil {
		if entry, ok := before[clineSearchServerName]; ok {
			servers[clineSearchServerName] = entry
			put = append(put, "mcpServers."+clineSearchServerName)
		}
	}

	if previous == nil && len(servers) == 0 && len(config) == 1 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return launch.RestoreOutcome{}, err
		}
		return launch.RestoreOutcome{Removed: true}, nil
	}
	config["mcpServers"] = servers
	if err := clineWriteJSONFile(path, config); err != nil {
		return launch.RestoreOutcome{}, err
	}
	return launch.RestoreOutcome{Removed: true, Reinstated: put}, nil
}
