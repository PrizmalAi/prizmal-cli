package cline

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/fileutil"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

const (
	clineSearchPluginName = "prizmal_search"
	clineSearchToolName   = "prizmal_search__search"
	clineSearchOwnerFile  = "prizmal-owner.json"
	clineSearchStateFile  = "prizmal-search.json"

	clineSearchURLEnv   = "PRIZMAL_CLINE_SEARCH_URL"
	clineSearchModelEnv = "PRIZMAL_CLINE_SEARCH_MODEL"
)

type clinePluginInstall func(pluginRoot, sourcePath string) (installPath string, err error)
type clinePluginUninstall func(pluginRoot, installPath string) error

var (
	installClinePlugin   clinePluginInstall   = runClinePluginInstall
	uninstallClinePlugin clinePluginUninstall = runClinePluginUninstall
)

type clineSearchState struct {
	InstallPath string `json:"installPath"`
	PluginRoot  string `json:"pluginRoot"`
	SourcePath  string `json:"sourcePath"`
}

type clinePluginOwner struct {
	Name   string `json:"name"`
	Tool   string `json:"tool"`
	Marker string `json:"marker"`
}

var clineSearchOwner = clinePluginOwner{
	Name:   clineSearchPluginName,
	Tool:   clineSearchToolName,
	Marker: "Written by prizmal. Restore removes this plugin.",
}

// clinePluginRoot is the Cline configuration tree whose plugins directory
// discovers plugins. It deliberately ignores the data directory: Cline always
// discovers plugins beside its configuration, and redirecting CLINE_DIR to an
// isolated data tree would hide every unrelated plugin under ~/.cline.
func clinePluginRoot(args []string, getenv func(string) string, home func() (string, error)) (string, error) {
	_, configDir := clinePathArgs(args)
	if configDir == "" {
		configDir = strings.TrimSpace(getenv("CLINE_DIR"))
	}
	if configDir != "" {
		return filepath.Abs(configDir)
	}
	h, err := home()
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Join(h, ".cline"))
}

func (c *Cline) pluginRoot() (string, error) {
	return clinePluginRoot(nil, os.Getenv, os.UserHomeDir)
}

func clineSearchSourceDir(pluginRoot string) string {
	return filepath.Join(pluginRoot, ".prizmal", "cline-search", clineSearchPluginName)
}

func clineSearchStatePath(pluginRoot string) string {
	return filepath.Join(pluginRoot, ".prizmal", clineSearchStateFile)
}

func clineSearchOwnerPath(dir string) string {
	return filepath.Join(dir, clineSearchOwnerFile)
}

// writeClineSearchSource writes the plugin package the install command copies.
// The tool reads its endpoint, model and credential at call time from the Cline
// process environment, so no key is written into these files.
func writeClineSearchSource(pluginRoot string) (string, error) {
	source := clineSearchSourceDir(pluginRoot)
	if err := os.MkdirAll(source, 0o755); err != nil {
		return "", err
	}
	owner, err := json.MarshalIndent(clineSearchOwner, "", "  ")
	if err != nil {
		return "", err
	}
	if err := fileutil.WriteWithBackup(clineSearchOwnerPath(source), owner, "cline-search"); err != nil {
		return "", err
	}
	toolPath := filepath.Join(source, "index.js")
	if err := fileutil.WriteWithBackup(toolPath, []byte(clineSearchPluginSource), "cline-search"); err != nil {
		return "", err
	}
	return source, nil
}

// writeClineSearch installs and records the plugin for one data tree. Before
// replacing an existing install at the generated path, it verifies the owner
// marker. A plugin from another source, including a user's own
// prizmal_search, has a different generated path and is never touched.
func writeClineSearch(pluginRoot string) error {
	return withClineSearchLock(pluginRoot, func() error {
		return writeClineSearchLocked(pluginRoot)
	})
}

func writeClineSearchLocked(pluginRoot string) error {
	source, err := writeClineSearchSource(pluginRoot)
	if err != nil {
		return err
	}
	installPath := clineInstalledPluginPath(pluginRoot, source)
	if _, err := os.Lstat(installPath); err == nil {
		if !clineSearchOwnsInstall(installPath) {
			return fmt.Errorf("Cline plugin path %s exists without prizmal's owner marker", installPath)
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	installed, err := installClinePlugin(pluginRoot, source)
	if err != nil {
		return fmt.Errorf("install Cline search plugin: %w", err)
	}
	if !clineSearchOwnsInstall(installed) {
		return fmt.Errorf("Cline installed %s without prizmal's owner marker", installed)
	}

	state := clineSearchState{
		InstallPath: installed,
		PluginRoot:  pluginRoot,
		SourcePath:  source,
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(clineSearchStatePath(pluginRoot)), 0o755); err != nil {
		return err
	}
	backup := clinePluginBackupIntegration(pluginRoot)
	if err := fileutil.WriteWithBackup(clineSearchStatePath(pluginRoot), data, backup); err != nil {
		return err
	}
	return nil
}

func clinePluginBackupIntegration(pluginRoot string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(pluginRoot)))
	return filepath.Join("cline", "plugin-roots", hex.EncodeToString(sum[:8]))
}

// withClineSearchLock serializes plugin replacement across prizmal processes.
// Several Cline data trees may share one configuration root, and Cline installs
// the plugin for that root. A stale lock older than two minutes is discarded.
func withClineSearchLock(pluginRoot string, fn func() error) error {
	lockDir := filepath.Join(pluginRoot, ".prizmal", "cline-search.lock")
	if err := os.MkdirAll(filepath.Dir(lockDir), 0o755); err != nil {
		return err
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		err := os.Mkdir(lockDir, 0o700)
		if err == nil {
			defer func() { _ = os.Remove(lockDir) }()
			return fn()
		}
		if !os.IsExist(err) {
			return err
		}
		info, statErr := os.Stat(lockDir)
		if statErr == nil && time.Since(info.ModTime()) > 2*time.Minute {
			_ = os.Remove(lockDir)
			continue
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for Cline plugin install lock %s", lockDir)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// clineInstalledPluginPath mirrors Cline 3.0.73's local plugin destination:
// <config>/plugins/_installed/local/<source-basename>-<sha256(source)[:12]>.
func clineInstalledPluginPath(pluginRoot, source string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(source)))
	name := strings.Trim(filepath.Base(source), "-_.")
	if name == "" {
		name = "plugin"
	}
	return filepath.Join(pluginRoot, "plugins", "_installed", "local", name+"-"+hex.EncodeToString(sum[:])[:12])
}

func clineSearchOwnsInstall(installPath string) bool {
	found := false
	_ = filepath.WalkDir(installPath, func(path string, entry fs.DirEntry, err error) error {
		if found || err != nil || entry.IsDir() || entry.Name() != clineSearchOwnerFile {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		var owner clinePluginOwner
		found = json.Unmarshal(data, &owner) == nil && owner == clineSearchOwner
		return nil
	})
	return found
}

func runClinePluginInstall(pluginRoot, sourcePath string) (string, error) {
	cmd := exec.Command("cline", "plugin", "install", "--force", "--json", sourcePath)
	cmd.Env = launch.ChildEnv([]string{"CLINE_DIR"}, []string{"CLINE_DIR=" + pluginRoot})
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	var result struct {
		InstallPath string `json:"installPath"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return "", fmt.Errorf("parse cline plugin install output: %w", err)
	}
	if result.InstallPath == "" {
		return "", fmt.Errorf("cline plugin install returned no installPath")
	}
	return result.InstallPath, nil
}

func runClinePluginUninstall(pluginRoot, installPath string) error {
	cmd := exec.Command("cline", "plugin", "uninstall", "--json", installPath)
	cmd.Env = launch.ChildEnv([]string{"CLINE_DIR"}, []string{"CLINE_DIR=" + pluginRoot})
	return cmd.Run()
}

func (c *Cline) restoreClineSearch(pluginRoot string) (launch.RestoreOutcome, error) {
	path := clineSearchStatePath(pluginRoot)
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return launch.RestoreOutcome{}, nil
	}
	if err != nil {
		return launch.RestoreOutcome{}, err
	}
	var state clineSearchState
	if err := json.Unmarshal(raw, &state); err != nil {
		return launch.RestoreOutcome{}, fmt.Errorf("parse Cline search state: %w", err)
	}
	if !clineSearchOwnsInstall(state.InstallPath) {
		return launch.RestoreOutcome{}, fmt.Errorf("refusing to remove Cline plugin %s without prizmal's owner marker", state.InstallPath)
	}
	root := filepath.Clean(pluginRoot)
	installPath := filepath.Clean(state.InstallPath)
	rel, err := filepath.Rel(root, installPath)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return launch.RestoreOutcome{}, fmt.Errorf("Cline plugin path %s is outside %s", installPath, root)
	}
	err = withClineSearchLock(root, func() error {
		if err := uninstallClinePlugin(root, installPath); err != nil {
			return fmt.Errorf("uninstall Cline search plugin: %w", err)
		}
		if clineSearchOwnsInstall(state.SourcePath) {
			if err := os.RemoveAll(state.SourcePath); err != nil {
				return err
			}
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	})
	if err != nil {
		return launch.RestoreOutcome{}, err
	}
	return launch.RestoreOutcome{Removed: true}, nil
}

func clineSearchPaths(pluginRoot string) []string {
	paths := []string{
		clineSearchOwnerPath(clineSearchSourceDir(pluginRoot)),
		filepath.Join(clineSearchSourceDir(pluginRoot), "index.js"),
		clineSearchStatePath(pluginRoot),
	}
	raw, err := os.ReadFile(clineSearchStatePath(pluginRoot))
	if err == nil {
		var state clineSearchState
		if json.Unmarshal(raw, &state) == nil && clineSearchOwnsInstall(state.InstallPath) {
			_ = filepath.WalkDir(state.InstallPath, func(path string, entry fs.DirEntry, err error) error {
				if err == nil && !entry.IsDir() {
					paths = append(paths, path)
				}
				return nil
			})
		}
	}
	slices.Sort(paths)
	return slices.Compact(paths)
}

func clineSearchEnv(model string) []string {
	return []string{
		clineSearchURLEnv + "=" + envconfig.OpenAIBaseURL(false),
		clineSearchModelEnv + "=" + model,
	}
}

// clineSearchPluginSource is dependency-free and runs in Cline's plugin
// worker. It intentionally declares only its own tool, so Cline decides which
// model requests receive it. The title and summary paths do not include session
// tools.
const clineSearchPluginSource = `// Generated by prizmal on launch. Edits are overwritten.
//
// Register a Switch-routed web search tool in Cline. The endpoint, model and
// credential are read when the tool runs, from the environment prizmal gives
// the Cline process.

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

export default {
  name: "prizmal_search",
  manifest: { capabilities: ["tools"] },
  setup(api) {
    api.registerTool({
      name: "prizmal_search__search",
      description:
        "Search the web through PrizmalSwitch. Returns a summary of current sources with their URLs. " +
        "Use it for anything that postdates the model's training data, or that needs a citable source.",
      inputSchema: {
        type: "object",
        properties: { query: { type: "string", description: "What to search the web for." } },
        required: ["query"],
      },
      async execute(args, ctx) {
        const base = (process.env.PRIZMAL_CLINE_SEARCH_URL || "").replace(/\/+$/, "")
        if (!base) {
          throw new Error("prizmal search is unconfigured: PRIZMAL_CLINE_SEARCH_URL is unset. Relaunch through the prizmal CLI.")
        }
        if (!args || typeof args.query !== "string" || !args.query.trim()) {
          throw new Error("prizmal search needs a non-empty query.")
        }

        const headers = { "content-type": "application/json" }
        if (process.env.OPENAI_API_KEY) headers.authorization = "Bearer " + process.env.OPENAI_API_KEY
        const response = await fetch(base + "/responses", {
          method: "POST",
          headers,
          signal: ctx && ctx.signal,
          body: JSON.stringify({
            model: process.env.PRIZMAL_CLINE_SEARCH_MODEL || "",
            tools: [{ type: "web_search" }],
            input:
              "Search the web and answer with a short factual summary followed by the source URLs.\n\nQuery: " +
              args.query,
          }),
        })
        if (!response.ok) {
          let detail = ""
          try {
            detail = (await response.text()).slice(0, 500)
          } catch {}
          throw new Error(
            "prizmal search: the Switch answered " +
              response.status +
              " on /responses." +
              (detail ? " Response: " + detail : ""),
          )
        }
        const output = collectText(await response.json())
        return output || "No results for that query."
      },
    })
  },
}
`
