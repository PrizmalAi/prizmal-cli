package cline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

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

	// The generated server reads its target and model from these variables.
	// The key is never among them: the server inherits Cline's environment,
	// where the launch puts it as OPENAI_API_KEY.
	clineSearchURLEnv   = "PRIZMAL_CLINE_SEARCH_URL"
	clineSearchModelEnv = "PRIZMAL_CLINE_SEARCH_MODEL"
)

func clineMCPSettingsPath(dataDir string) string {
	return filepath.Join(dataDir, "settings", clineMCPSettingsFile)
}

// clineSearchServerPath is the prizmal-owned file the MCP entry runs.
func clineSearchServerPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", "prizmal", "cline", clineSearchServerName+".mjs"), nil
}

// writeClineSearchServer generates the MCP server and returns its path. The
// file is rewritten on every launch so an older prizmal's version never
// lingers.
func writeClineSearchServer() (string, error) {
	path, err := clineSearchServerPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	return path, os.WriteFile(path, []byte(clineSearchServerSource()), 0o644)
}

// clineSearchEntry is the mcpServers entry the launch writes.
func clineSearchEntry(serverPath, model string) map[string]any {
	return map[string]any{
		"transport": map[string]any{
			"type":    "stdio",
			"command": "node",
			"args":    []any{serverPath},
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
func writeClineMCPSettings(dataDir, serverPath, model, backup string) error {
	path := clineMCPSettingsPath(dataDir)
	config, err := readClineConfig(path)
	if err != nil {
		return err
	}
	servers, _ := config["mcpServers"].(map[string]any)
	if servers == nil {
		servers = make(map[string]any)
	}
	servers[clineSearchServerName] = clineSearchEntry(serverPath, model)
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

// clineSearchServerSource renders the MCP server. It imports only
// node:readline and uses the global fetch, so it runs on the Node.js Cline
// already needs, with nothing to install.
func clineSearchServerSource() string {
	return strings.NewReplacer(
		"__SERVER__", clineSearchServerName,
		"__TOOL__", clineSearchToolName,
		"__URL_ENV__", clineSearchURLEnv,
		"__MODEL_ENV__", clineSearchModelEnv,
	).Replace(clineSearchServerTemplate)
}

const clineSearchServerTemplate = `// Generated by prizmal on launch. Edits are overwritten.
//
// An MCP stdio server that routes web search through the Prizmal Switch. The
// Switch's /v1 base and the model arrive in __URL_ENV__ and __MODEL_ENV__;
// the key is the OPENAI_API_KEY Cline runs with.
import { createInterface } from "node:readline"

const tool = {
  name: "__TOOL__",
  description:
    "Search the web through the Prizmal Switch. Returns a summary of current sources with their URLs. " +
    "Use it for anything that postdates the model's training data, or that needs a citable source.",
  inputSchema: {
    type: "object",
    properties: { query: { type: "string", description: "What to search the web for." } },
    required: ["query"],
  },
}

function send(message) {
  process.stdout.write(JSON.stringify({ jsonrpc: "2.0", ...message }) + "\n")
}

function collectText(payload) {
  if (!payload || typeof payload !== "object") return ""
  if (typeof payload.output_text === "string" && payload.output_text) return payload.output_text
  const parts = []
  for (const item of Array.isArray(payload.output) ? payload.output : []) {
    for (const chunk of Array.isArray(item && item.content) ? item.content : []) {
      if (chunk && typeof chunk.text === "string" && chunk.text) parts.push(chunk.text)
    }
  }
  return parts.join("\n\n")
}

function failure(text) {
  return { content: [{ type: "text", text }], isError: true }
}

async function search(query) {
  const base = (process.env.__URL_ENV__ || "").replace(/\/+$/, "")
  if (!base) return failure("prizmal search is unconfigured: __URL_ENV__ is unset. Relaunch through the prizmal CLI.")
  if (typeof query !== "string" || !query.trim()) return failure("prizmal search needs a non-empty query.")

  const headers = { "content-type": "application/json" }
  if (process.env.OPENAI_API_KEY) headers["authorization"] = "Bearer " + process.env.OPENAI_API_KEY

  let response
  try {
    response = await fetch(base + "/responses", {
      method: "POST",
      headers,
      body: JSON.stringify({
        model: process.env.__MODEL_ENV__ || "",
        tools: [{ type: "web_search" }],
        input:
          "Search the web and answer with a short factual summary followed by the source URLs.\n\nQuery: " + query,
      }),
    })
  } catch (err) {
    return failure("prizmal search could not reach the Switch: " + (err && err.message ? err.message : err))
  }
  if (!response.ok) {
    let detail = ""
    try {
      detail = (await response.text()).slice(0, 500)
    } catch {}
    return failure("prizmal search: the Switch answered " + response.status + " on /responses." + (detail ? " Response: " + detail : ""))
  }
  let payload
  try {
    payload = await response.json()
  } catch {
    return failure("prizmal search: the Switch answered with a body that is not JSON.")
  }
  return { content: [{ type: "text", text: collectText(payload) || "No results for that query." }] }
}

async function handle(message) {
  const { id, method, params } = message
  const isRequest = id !== undefined && id !== null
  switch (method) {
    case "initialize":
      return send({
        id,
        result: {
          protocolVersion: (params && params.protocolVersion) || "2025-06-18",
          capabilities: { tools: {} },
          serverInfo: { name: "__SERVER__", version: "1" },
        },
      })
    case "ping":
      return isRequest && send({ id, result: {} })
    case "tools/list":
      return send({ id, result: { tools: [tool] } })
    case "tools/call": {
      if (!params || params.name !== tool.name) {
        return send({ id, error: { code: -32602, message: "unknown tool: " + (params && params.name) } })
      }
      return send({ id, result: await search(params.arguments && params.arguments.query) })
    }
    default:
      // Notifications such as notifications/initialized get no answer.
      if (isRequest) send({ id, error: { code: -32601, message: "method not found: " + method } })
  }
}

createInterface({ input: process.stdin }).on("line", (line) => {
  if (!line.trim()) return
  let message
  try {
    message = JSON.parse(line)
  } catch {
    return send({ id: null, error: { code: -32700, message: "parse error" } })
  }
  handle(message).catch((err) => {
    if (message.id !== undefined && message.id !== null) {
      send({ id: message.id, error: { code: -32603, message: String(err && err.message ? err.message : err) } })
    }
  })
})
`
