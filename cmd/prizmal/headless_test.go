package main

import (
	"testing"

	launcher "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/registry"
)

// Each harness that can run one prompt and exit says so from its own
// arguments, so a launch with no model skips the picker before a run nobody
// watches. The arguments are the ones written after the integration name.
func TestEveryHarnessReportsItsHeadlessRun(t *testing.T) {
	for _, tc := range []struct {
		harness  string
		args     []string
		headless bool
	}{
		{"claude", []string{"-p", "hi"}, true},
		{"claude", []string{"--print", "hi"}, true},
		{"claude", []string{"--resume", "abc"}, false},
		{"claude", nil, false},

		{"codex", []string{"exec", "hi"}, true},
		{"codex", []string{"e", "hi"}, true},
		{"codex", []string{"review", "--uncommitted"}, true},
		{"codex", []string{"-c", "x=y", "-s", "read-only", "exec", "hi"}, true},
		{"codex", []string{"--sandbox=read-only", "exec", "hi"}, true},
		// A value of a flag is not the subcommand, and a prompt that mentions
		// exec is an interactive session's first message.
		{"codex", []string{"-C", "exec", "hi"}, false},
		{"codex", []string{"fix the exec path"}, false},
		{"codex", []string{"resume", "--last"}, false},
		{"codex", nil, false},

		{"opencode", []string{"run", "hi"}, true},
		{"opencode", []string{"--agent", "build", "run", "hi"}, true},
		{"opencode", []string{"some/project"}, false},
		{"opencode", nil, false},

		{"pi", []string{"--print", "hi"}, true},
		{"pi", []string{"-p", "hi"}, true},
		{"pi", []string{"hi"}, false},
		{"pi", nil, false},

		{"kilo", []string{"run", "hi"}, true},
		{"kilo", []string{"--model", "run"}, false},
		{"kilo", nil, false},

		// Cline runs a prompt to the end unless --tui opens the interface.
		{"cline", []string{"hi"}, true},
		{"cline", []string{"-y", "--cwd", "/tmp", "hi"}, true},
		{"cline", []string{"--tui", "hi"}, false},
		{"cline", []string{"-i", "hi"}, false},
		{"cline", []string{"--cwd", "/tmp"}, false},
		{"cline", nil, false},
	} {
		spec, err := registry.LookupIntegrationSpec(tc.harness)
		if err != nil {
			t.Fatal(err)
		}
		if got := launcher.RunsHeadless(spec.Runner, tc.args); got != tc.headless {
			t.Errorf("%s %q: headless = %v, want %v", tc.harness, tc.args, got, tc.headless)
		}
	}
}

// Every integration in the registry answers the question, so a new harness
// with a one-shot mode cannot open the picker in front of it by omission.
func TestEveryIntegrationDeclaresItsHeadlessRun(t *testing.T) {
	for _, spec := range registry.ListAllIntegrationSpecs() {
		if _, ok := spec.Runner.(launcher.HeadlessRunner); !ok {
			t.Errorf("%s does not say which of its runs are headless", spec.Name)
		}
	}
}
