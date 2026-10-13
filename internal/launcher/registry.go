package launch

import (
	"fmt"
	"slices"
	"strings"
)

// IntegrationInstallSpec describes how launcher should detect and guide installation.
// It holds what the registry alone knows — where to point an operator who must
// install by hand, and the function that installs automatically. Whether the
// binary is already there is not in here: that is a fact about the runner, and
// the runner answers it through Installed.
type IntegrationInstallSpec struct {
	EnsureInstalled func() error
	URL             string
	Command         []string
}

// IntegrationSpec is the canonical registry entry for one integration.
type IntegrationSpec struct {
	Name        string
	Runner      Runner
	Aliases     []string
	Hidden      bool
	Description string
	Install     IntegrationInstallSpec
}

var launcherIntegrationOrder = []string{"claude", "codex", "cline", "opencode", "pi"}

var integrationSpecs = []*IntegrationSpec{
	{
		Name:        "claude",
		Runner:      &Claude{},
		Description: "Anthropic's coding tool with subagents",
		Install: IntegrationInstallSpec{
			EnsureInstalled: func() error {
				_, err := claudeInstaller.EnsureInstalled()
				return err
			},
			URL: "https://code.claude.com/docs/en/quickstart",
		},
	},
	{
		Name:        "cline",
		Runner:      &Cline{},
		Description: "Autonomous coding agent with parallel execution",
		Install: IntegrationInstallSpec{
			EnsureInstalled: func() error {
				_, err := clineInstaller.EnsureInstalled()
				return err
			},
			Command: []string{"npm", "install", "-g", "cline@latest"},
		},
	},
	{
		Name:        "codex",
		Runner:      &Codex{},
		Description: "OpenAI's open-source coding agent",
		Install: IntegrationInstallSpec{
			URL:     "https://developers.openai.com/codex/cli/",
			Command: []string{"npm", "install", "-g", "@openai/codex"},
		},
	},
	{
		Name:        "opencode",
		Runner:      &OpenCode{},
		Description: "Anomaly's open-source coding agent",
		Install: IntegrationInstallSpec{
			EnsureInstalled: func() error {
				_, err := openCodeInstaller.EnsureInstalled()
				return err
			},
			URL: "https://opencode.ai",
		},
	},
	{
		Name:        "pi",
		Runner:      &Pi{},
		Description: "Minimal AI agent toolkit with plugin support",
		Install: IntegrationInstallSpec{
			EnsureInstalled: func() error {
				_, err := piInstaller.EnsureInstalled()
				return err
			},
			Command: []string{"npm", "install", "-g", "@earendil-works/pi-coding-agent@latest"},
		},
	},
}

var integrationSpecsByName map[string]*IntegrationSpec

func init() {
	rebuildIntegrationSpecIndexes()
}

func rebuildIntegrationSpecIndexes() {
	integrationSpecsByName = make(map[string]*IntegrationSpec, len(integrationSpecs))

	canonical := make(map[string]bool, len(integrationSpecs))
	for _, spec := range integrationSpecs {
		key := strings.ToLower(spec.Name)
		if key == "" {
			panic("launch: integration spec missing name")
		}
		if canonical[key] {
			panic(fmt.Sprintf("launch: duplicate integration name %q", key))
		}
		canonical[key] = true
		integrationSpecsByName[key] = spec
	}

	seenAliases := make(map[string]string)
	for _, spec := range integrationSpecs {
		for _, alias := range spec.Aliases {
			key := strings.ToLower(alias)
			if key == "" {
				panic(fmt.Sprintf("launch: integration %q has empty alias", spec.Name))
			}
			if canonical[key] {
				panic(fmt.Sprintf("launch: alias %q collides with canonical integration name", key))
			}
			if owner, exists := seenAliases[key]; exists {
				panic(fmt.Sprintf("launch: alias %q collides between %q and %q", key, owner, spec.Name))
			}
			seenAliases[key] = spec.Name
			integrationSpecsByName[key] = spec
		}
	}

	orderSeen := make(map[string]bool, len(launcherIntegrationOrder))
	for _, name := range launcherIntegrationOrder {
		key := strings.ToLower(name)
		if orderSeen[key] {
			panic(fmt.Sprintf("launch: duplicate launcher order entry %q", key))
		}
		orderSeen[key] = true

		spec, ok := integrationSpecsByName[key]
		if !ok {
			panic(fmt.Sprintf("launch: unknown launcher order entry %q", key))
		}
		if spec.Name != key {
			panic(fmt.Sprintf("launch: launcher order entry %q must use canonical name, not alias", key))
		}
		if spec.Hidden {
			panic(fmt.Sprintf("launch: hidden integration %q cannot appear in launcher order", key))
		}
	}
}

// ListAllIntegrationSpecs returns every registered integration, including
// hidden ones, in registry declaration order. Used by tests that must cover
// the full registry surface.
func ListAllIntegrationSpecs() []IntegrationSpec {
	all := make([]IntegrationSpec, 0, len(integrationSpecs))
	for _, spec := range integrationSpecs {
		all = append(all, *spec)
	}
	return all
}

// LookupIntegrationSpec resolves either a canonical integration name or alias to its spec.
func LookupIntegrationSpec(name string) (*IntegrationSpec, error) {
	spec, ok := integrationSpecsByName[strings.ToLower(name)]
	if !ok {
		return nil, fmt.Errorf("unknown integration: %s", name)
	}
	return spec, nil
}

// ListVisibleIntegrationSpecs returns the canonical integrations that should appear in interactive UIs.
func ListVisibleIntegrationSpecs() []IntegrationSpec {
	visible := make([]IntegrationSpec, 0, len(integrationSpecs))
	for _, spec := range integrationSpecs {
		if spec.Hidden {
			continue
		}
		if supported, ok := spec.Runner.(SupportedIntegration); ok && supported.Supported() != nil {
			continue
		}
		visible = append(visible, *spec)
	}

	orderRank := make(map[string]int, len(launcherIntegrationOrder))
	for i, name := range launcherIntegrationOrder {
		orderRank[name] = i + 1
	}

	slices.SortFunc(visible, func(a, b IntegrationSpec) int {
		aRank, bRank := orderRank[a.Name], orderRank[b.Name]
		if aRank > 0 && bRank > 0 {
			return aRank - bRank
		}
		if aRank > 0 {
			return -1
		}
		if bRank > 0 {
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})

	return visible
}

// integration is resolved registry metadata used by launcher state and install checks.
// It combines immutable registry spec data with computed runtime traits.
type integration struct {
	spec            *IntegrationSpec
	installed       bool
	autoInstallable bool
}

// integrationFor resolves an integration name into the canonical spec plus
// derived launcher/install traits used across registry and launch flows.
//
// Installation is asked of the runner itself rather than of a closure in the
// registry. The registry used to answer this by constructing a second adapter
// — a fresh &Claude{} — to call a private method on one it was already
// holding, so the answer came from an instance that never launched anything.
func integrationFor(name string) (integration, error) {
	spec, err := LookupIntegrationSpec(name)
	if err != nil {
		return integration{}, err
	}

	// A runner that does not report installation counts as present, which is
	// what a spec with no check used to mean: nothing can vouch that it is
	// missing, so the launch proceeds and the runner's own ensure path reports
	// a missing binary in the terms that harness uses.
	installed := true
	if probe, ok := spec.Runner.(Installed); ok {
		installed = probe.Installed()
	}

	return integration{
		spec:            spec,
		installed:       installed,
		autoInstallable: spec.Install.EnsureInstalled != nil,
	}, nil
}

// EnsureIntegrationInstalled installs auto-installable integrations when missing.
func EnsureIntegrationInstalled(name string, runner Runner) error {
	integration, err := integrationFor(name)
	if err != nil {
		return fmt.Errorf("%s is not installed", runner)
	}

	if supported, ok := runner.(SupportedIntegration); ok {
		if err := supported.Supported(); err != nil {
			return err
		}
	}

	if integration.installed {
		return nil
	}
	if integration.autoInstallable {
		return integration.spec.Install.EnsureInstalled()
	}

	switch {
	case integration.spec.Install.URL != "":
		return fmt.Errorf("%s is not installed, install from %s", integration.spec.Name, integration.spec.Install.URL)
	case len(integration.spec.Install.Command) > 0:
		return fmt.Errorf("%s is not installed, install with: %s", integration.spec.Name, strings.Join(integration.spec.Install.Command, " "))
	default:
		return fmt.Errorf("%s is not installed", runner)
	}
}
