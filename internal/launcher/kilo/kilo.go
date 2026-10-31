package kilo

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/opencode"
)

// Kilo implements launch.Runner for the Kilo Code CLI. Kilo is a fork of
// OpenCode, so the launch passes its whole config in KILO_CONFIG_CONTENT, the
// way the OpenCode launch does, and writes no Kilo file.
type Kilo struct {
	api string // one of the apiPackages keys; empty means chat completions
}

// apiPackages maps a Kilo API mode to the AI SDK package that speaks it. Chat
// completions is the default. Responses and Anthropic Messages are opt-in:
// each package appends its own path (/responses, /messages) to the /v1 base.
var apiPackages = map[string]string{
	launch.APIChat:      "@ai-sdk/openai-compatible",
	launch.APIResponses: "@ai-sdk/openai",
	launch.APIAnthropic: "@ai-sdk/anthropic",
}

// SetAPI chooses the wire API for the launch. An empty name keeps the default.
func (k *Kilo) SetAPI(name string) error {
	if name != "" {
		if _, ok := apiPackages[name]; !ok {
			return fmt.Errorf("Kilo does not support API %q: it supports chat, responses, anthropic", name)
		}
	}
	k.api = name
	return nil
}

// SupportedAPIs lists the wire APIs Kilo speaks to the Switch, chat first.
func (k *Kilo) SupportedAPIs() []string {
	return []string{launch.APIChat, launch.APIResponses, launch.APIAnthropic}
}

func (k *Kilo) String() string { return "Kilo Code" }

// SupportsDeviceMode reports that Kilo can run from an enrolled device. The
// launch always starts a loopback relay, which refreshes the bearer for each
// request, so the config carries a per-launch placeholder instead of a key.
func (k *Kilo) SupportsDeviceMode() bool { return true }

// RunsHeadless reports that Kilo's `run` subcommand runs one message and
// exits. The subcommand is the first positional argument: a value of an option
// before it is not one.
func (k *Kilo) RunsHeadless(args []string) bool {
	return launch.FirstPositional(args, kiloValueFlags) == "run"
}

// kiloValueFlags are Kilo's options that take a value, so the subcommand scan
// does not read a value as the subcommand.
var kiloValueFlags = map[string]bool{
	"-m": true, "--model": true,
	"-s": true, "--session": true,
	"--prompt": true, "--agent": true,
	"--port": true, "--hostname": true,
	"--mdns-domain": true, "--log-level": true,
	"--cors": true,
}

// KiloInstaller is Kilo's install story: npm puts `kilo` on PATH on every
// platform, so Node.js is the only dependency.
var KiloInstaller = launch.Installer{
	Name:         "kilo",
	DisplayName:  "Kilo Code",
	Prompt:       "Kilo Code is not installed. Install with npm?",
	Locate:       locateKilo,
	Dependencies: KiloInstallerDependencies,
	Command:      KiloInstallerCommand,
}

// locateKilo returns the bare command name when npm has put `kilo` on PATH.
func locateKilo(string) (string, error) {
	if _, err := exec.LookPath("kilo"); err != nil {
		return "", launch.ErrHarnessAbsent
	}
	return "kilo", nil
}

// KiloInstallerDependencies lists what Kilo's installer needs.
func KiloInstallerDependencies(string) []launch.Dependency {
	return []launch.Dependency{launch.NpmDependency}
}

// KiloInstallerCommand is the npm install Kilo ships, on every platform.
func KiloInstallerCommand(string) (string, []string, error) {
	return "npm", []string{"install", "-g", "@kilocode/cli@latest"}, nil
}

func (k *Kilo) Installed() bool { return KiloInstaller.Installed() }

func (k *Kilo) Run(model string, models []launch.LaunchModel, args []string) error {
	bin, err := KiloInstaller.EnsureInstalled()
	if err != nil {
		return err
	}
	content, err := buildInlineConfigFor(k.api, model, models)
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, launchArgs(model, args)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = launch.ChildEnv([]string{"KILO_CONFIG_DIR"}, k.envVars(model, content))
	return cmd.Run()
}

// envVars are the variables the launch adds to Kilo's environment. The search
// tool is an extra, not a precondition: a launch that cannot write it still
// runs, without web search. Kilo reads one config directory from
// KILO_CONFIG_DIR, so childEnv drops any inherited value.
func (k *Kilo) envVars(model, content string) []string {
	env := []string{"KILO_CONFIG_CONTENT=" + content}
	dir, err := searchDir()
	if err != nil || opencode.WriteSearchTool(dir) != nil {
		return env
	}
	env = append(env, "KILO_CONFIG_DIR="+dir)
	return append(env, opencode.SearchToolEnv(envconfig.OpenAIBaseURL(false), model, envconfig.APIKey())...)
}

// searchDir is the prizmal-owned directory handed to Kilo as KILO_CONFIG_DIR.
func searchDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", "prizmal", "kilo"), nil
}

// launchArgs names the launch's model on Kilo's command line, so Kilo does not
// fall back to its own default; the inline config only registers the provider.
// The `run` subcommand takes the option after it, not before.
func launchArgs(model string, args []string) []string {
	flag := []string{"--model", "prizmal/" + model}
	for i, arg := range args {
		if arg == "run" && launch.FirstPositional(args[:i+1], kiloValueFlags) == "run" {
			out := append([]string{}, args[:i+1]...)
			out = append(out, flag...)
			return append(out, args[i+1:]...)
		}
	}
	return append(flag, args...)
}

// buildInlineConfig is the JSON for KILO_CONFIG_CONTENT. The launch's model
// comes first in the list, then the rest of the catalog.
func buildInlineConfig(model string, catalog []launch.LaunchModel) (string, error) {
	return buildInlineConfigFor("", model, catalog)
}

// buildInlineConfigFor builds the config for one API mode; "" is chat.
func buildInlineConfigFor(api, model string, catalog []launch.LaunchModel) (string, error) {
	if api == "" {
		api = "chat"
	}
	if model == "" {
		return "", fmt.Errorf("kilo: a model is required")
	}
	models := []launch.LaunchModel{launch.FallbackLaunchModel(model)}
	if found, ok := launch.FindCatalogModel(catalog, model); ok {
		models[0] = found
	}
	for _, m := range catalog {
		if !launch.ModelNamesSame(m.Name, model) {
			models = append(models, m)
		}
	}

	config := map[string]any{
		"provider": map[string]any{
			"prizmal": map[string]any{
				"npm":  apiPackages[api],
				"name": "Prizmal",
				"options": map[string]any{
					"baseURL": envconfig.OpenAIBaseURL(false),
					"apiKey":  envconfig.APIKey(),
				},
				"models": modelEntries(models),
			},
		},
		"model": "prizmal/" + model,
		// Only the Prizmal provider is on offer. Without this, Kilo's /models
		// also lists the models of its own gateway, fetched over the network.
		"enabled_providers": []string{"prizmal"},
		// Kilo's built-in websearch tool posts to hosted backends outside the
		// Switch, so both blocks turn it off, as the OpenCode launch does.
		"tools":      map[string]any{"websearch": false},
		"permission": map[string]any{"websearch": "deny"},
	}
	data, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// modelEntries builds OpenCode's provider entries and adjusts the input
// modalities for Kilo. Kilo withholds an attachment whose type the entry does
// not list, so each entry follows the model's own input_modalities: text,
// image for the image capability, pdf for the document capability. A model
// that reports no list keeps text and image, and gets no pdf. The reasoning
// flag is not an input modality, so a reasoning model with no list is still a
// model with no list.
func modelEntries(models []launch.LaunchModel) map[string]any {
	entries := opencode.ModelEntries(models)
	for _, m := range models {
		entry, _ := entries[m.Name].(map[string]any)
		// Kilo builds its --variant levels itself from "reasoning": true, so the
		// variants and options OpenCode's entry adds are dropped.
		delete(entry, "variants")
		delete(entry, "options")
		if m.KnowsInputs() {
			continue
		}
		entry["modalities"] = map[string]any{
			"input":  []string{"text", "image"},
			"output": []string{"text"},
		}
	}
	return entries
}
