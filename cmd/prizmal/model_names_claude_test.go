//go:build !windows

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/PrizmalAi/prizmal-cli/internal/stubserver"
)

// These tests drive the real Claude Code through prizmal against a stub that
// refuses a model name that is neither a router config nor an alias, the way
// the Switch will once it stops serving the key's bound config for one. They
// show that every request Claude Code made carried a name the tenant routes.
//
// Claude Code names a model in more places than its main loop:
//
//   - the session model, the /model picker's rows and the Default row: names
//     from the launch.
//   - a tier word (/model sonnet, a subagent's model, the small model behind
//     the background calls such as the session title): Claude Code resolves it
//     to its own vendor id. A tier the tenant holds maps to the tier's alias,
//     or reaches the Switch as a vendor id it maps to the alias. A tier the
//     tenant does not hold goes to the launch model.
//   - subagents: the session model, --subagent-model, or the tier word of the
//     built-in agent (Explore runs on the small model).
//   - compaction: the session model (claude_compact_e2e_test.go records it).

// namesHeldTiers is a tenant that holds the opus, sonnet and haiku tiers and
// not fable. The configs are named for what they do, not for the tier.
var namesHeldTiers = []stubserver.Entry{
	{ID: namesFrontier, Tier: "opus"},
	{ID: namesCore, Tier: "sonnet"},
	{ID: namesFlash, Tier: "haiku"},
}

// namesNoTiers is a tenant that holds none.
var namesNoTiers = []stubserver.Entry{{ID: namesFrontier}, {ID: namesCore}, {ID: namesFlash}}

// titlePrompt is long enough that Claude Code names the session with a call on
// its small model, which the stub sees as a request that asks for a title.
const titlePrompt = "Please read the project files and describe the layout of the repository for me"

const titleMarker = "Write the title"

func claudeNamesCase(name string, rec *stubserver.Recorder, entries []stubserver.Entry, args []string, steps ...baselineStep) baselineCase {
	return baselineCase{
		name: name, cols: 100, rows: 40,
		// Default permission mode, not auto: auto mode sends every tool call to
		// a classifier, which the stub cannot answer, and opens a billing
		// notice before a subagent runs.
		args: args, argsAfter: []string{"--permission-mode", "default"}, claude: true,
		entries: entries, recorder: rec, steps: steps,
	}
}

func runClaudeNames(t *testing.T, tc baselineCase) *stubserver.Recorder {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Claude Code launches in short mode")
	}
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux is not on PATH")
	}
	// The pinned release, as the baselines use: a newer one can open a dialog
	// that waits for a key before it runs a subagent.
	claudeDir, reason := pinnedClaudeDir(t)
	if reason != "" {
		if os.Getenv(harnessLaunchRequireEnv) == "require" || os.Getenv(baselineRequireEnv) == "require" {
			t.Fatal(reason)
		}
		t.Skip(reason)
	}
	prizmalBin := buildPrizmal(t)
	screen, _ := renderBaseline(t, tmuxPath, prizmalBin, claudeDir, tc)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("--- final screen ---\n%s", screen)
		}
	})
	return tc.recorder
}

// callContaining returns the first request whose body contains text, waiting
// up to timeout for a background call that lands after the reply.
func callContaining(rec *stubserver.Recorder, text string, timeout time.Duration) (stubserver.ModelCall, bool) {
	deadline := time.Now().Add(timeout)
	for {
		if call, ok := callWith(rec, text); ok {
			return call, true
		}
		if time.Now().After(deadline) {
			return stubserver.ModelCall{}, false
		}
		time.Sleep(250 * time.Millisecond)
	}
}

var (
	stepClaudeSend  = func(text string) baselineStep { return baselineStep{literal: text, waitFor: text[:min(len(text), 12)]} }
	stepClaudeEnter = baselineStep{key: "Enter", waitFor: stubserver.Reply}
)

func TestClaudeSendsOnlyRoutableNames(t *testing.T) {
	for _, tenant := range []struct {
		name    string
		entries []stubserver.Entry
	}{{"no-tiers", namesNoTiers}, {"held-tiers", namesHeldTiers}} {
		t.Run(tenant.name+"/turn-and-background-call", func(t *testing.T) {
			t.Parallel()
			rec := &stubserver.Recorder{}
			runClaudeNames(t, claudeNamesCase("turn", rec, tenant.entries, []string{"-m", namesFrontier, "claude"},
				stepClaudeReady, stepClaudeSend(titlePrompt), stepClaudeEnter))
			title, ok := callContaining(rec, titleMarker, 30*time.Second)
			if !ok {
				t.Fatalf("Claude Code made no background title call; it sent %q", modelsOf(rec))
			}
			assertServed(t, rec)
			t.Logf("the background call named %q; every name sent: %q", title.Model, modelsOf(rec))
		})

		// Every tier word the /model command takes resolves to a name the
		// tenant routes.
		for _, word := range []string{"haiku", "sonnet", "opus", "fable", "best", "opusplan"} {
			t.Run(tenant.name+"/model-"+word, func(t *testing.T) {
				t.Parallel()
				rec := &stubserver.Recorder{}
				runClaudeNames(t, claudeNamesCase("word-"+word, rec, tenant.entries, []string{"-m", namesFrontier, "claude"},
					stepClaudeReady,
					baselineStep{literal: "/model " + word, waitFor: "/model " + word},
					baselineStep{key: "Enter", waitFor: "Set model"},
					stepClaudeSend("hi"), stepClaudeEnter))
				assertServed(t, rec)
				if tenant.name == "no-tiers" {
					// A tenant with no tier has one answer for every word.
					for _, m := range modelsOf(rec) {
						if m != namesFrontier {
							t.Errorf("/model %s sent %q, want the launch model %q", word, m, namesFrontier)
						}
					}
				}
			})
		}

		t.Run(tenant.name+"/subagent", func(t *testing.T) {
			t.Parallel()
			rec := &stubserver.Recorder{}
			runClaudeNames(t, claudeNamesCase("subagent", rec, tenant.entries, []string{"-m", namesFrontier, "claude"},
				stepClaudeReady, stepClaudeSend(stubserver.SpawnTrigger), stepClaudeEnter))
			child, ok := subagentCall(rec)
			if !ok {
				t.Fatalf("Claude Code spawned no subagent; it sent %q", modelsOf(rec))
			}
			assertServed(t, rec)
			t.Logf("the Explore subagent named %q; every name sent: %q", child.Model, modelsOf(rec))
			if tenant.name == "no-tiers" && child.Model != namesFrontier {
				t.Errorf("the subagent ran on %q, want the launch model %q", child.Model, namesFrontier)
			}
		})
	}

	t.Run("subagent-model", func(t *testing.T) {
		t.Parallel()
		rec := &stubserver.Recorder{}
		runClaudeNames(t, claudeNamesCase("subagent-model", rec, namesNoTiers,
			[]string{"-m", namesFrontier, "--subagent-model", namesCore, "claude"},
			stepClaudeReady, stepClaudeSend(stubserver.SpawnGeneralTrigger), stepClaudeEnter))
		child, ok := subagentCall(rec)
		if !ok {
			t.Fatalf("Claude Code spawned no subagent; it sent %q", modelsOf(rec))
		}
		assertServed(t, rec)
		if !strings.HasPrefix(child.Model, namesCore) {
			t.Errorf("the subagent ran on %q, want the subagent model %q", child.Model, namesCore)
		}
	})
}
