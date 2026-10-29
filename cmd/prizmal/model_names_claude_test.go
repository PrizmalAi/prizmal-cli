//go:build !windows

package main

import (
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PrizmalAi/prizmal-cli/internal/stubserver"
)

// These tests drive the real Claude Code through prizmal against a stub that
// refuses a model name that is neither a model nor an alias, the way
// the Switch does. They
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

	// A bare tier word is a name Claude Code resolves itself. On a tenant that
	// holds the tier, the launch maps its ids onto the tier's alias, so the
	// session and its background calls carry names the tenant routes. A held
	// haiku tier is the exception: the launch leaves haiku out of
	// modelOverrides, because a haiku profile would refuse auto mode, so the
	// session's id reaches the Switch as the vendor id it maps onto the alias.
	for _, tc := range []struct{ word, wire string }{
		{"sonnet", "claude-tier-sonnet"},
		{"opus", "claude-tier-opus"},
		{"haiku", "claude-haiku-4-5-20251001"},
		{"best", "claude-tier-fable"},
	} {
		t.Run("held-tiers/launch-"+tc.word, func(t *testing.T) {
			t.Parallel()
			rec := &stubserver.Recorder{}
			entries := namesHeldTiers
			if tc.word == "best" {
				// best runs the fable tier, which this tenant holds too.
				entries = append(slices.Clone(namesHeldTiers), stubserver.Entry{ID: "prizmal-fable", Tier: "fable"})
			}
			runClaudeNames(t, claudeNamesCase("launch-"+tc.word, rec, entries, []string{"-m", tc.word, "claude"},
				stepClaudeReady, stepClaudeSend("hi"), stepClaudeEnter))
			assertServed(t, rec)
			if !slices.Contains(modelsOf(rec), tc.wire) {
				t.Errorf("-m %s sent %q, want %q among them", tc.word, modelsOf(rec), tc.wire)
			}
		})
	}

	// A Switch that reports generic tiers holds the frontier alias, and the
	// launch maps Claude Code's opus ids onto it. So --model opus routes to the
	// config that holds frontier, as it did to the one holding
	// claude-tier-opus.
	t.Run("generic-tiers/launch-opus", func(t *testing.T) {
		t.Parallel()
		rec := &stubserver.Recorder{}
		entries := []stubserver.Entry{
			{ID: namesFrontier, Tier: "frontier", Tiers: []string{"frontier"}},
			{ID: namesFlash, Tier: "flash", Tiers: []string{"flash"}},
		}
		runClaudeNames(t, claudeNamesCase("generic-launch-opus", rec, entries, []string{"-m", "opus", "claude"},
			stepClaudeReady, stepClaudeSend("hi"), stepClaudeEnter))
		assertServed(t, rec)
		if !slices.Contains(modelsOf(rec), "frontier") {
			t.Errorf("-m opus sent %q, want frontier among them", modelsOf(rec))
		}
	})

	// A name copied off a picker row reads "tier-opus" and routes as the
	// catalog's own name, which carries the prefix the label drops.
	t.Run("held-tiers/launch-picker-label", func(t *testing.T) {
		t.Parallel()
		rec := &stubserver.Recorder{}
		runClaudeNames(t, claudeNamesCase("launch-picker-label", rec, namesHeldTiers, []string{"-m", "tier-opus", "claude"},
			stepClaudeReady, stepClaudeSend("hi"), stepClaudeEnter))
		assertServed(t, rec)
		if !slices.Contains(modelsOf(rec), "claude-tier-opus") {
			t.Errorf("-m tier-opus sent %q, want claude-tier-opus among them", modelsOf(rec))
		}
	})

	// A subagent tier word resolves through the same chain: a held tier onto
	// its alias, an unheld one onto the launch model.
	t.Run("held-tiers/subagent-model-sonnet", func(t *testing.T) {
		t.Parallel()
		rec := &stubserver.Recorder{}
		runClaudeNames(t, claudeNamesCase("subagent-model-sonnet", rec, namesHeldTiers,
			[]string{"-m", namesFrontier, "--subagent-model", "sonnet", "claude"},
			stepClaudeReady, stepClaudeSend(stubserver.SpawnGeneralTrigger), stepClaudeEnter))
		child, ok := subagentCall(rec)
		if !ok {
			t.Fatalf("Claude Code spawned no subagent; it sent %q", modelsOf(rec))
		}
		assertServed(t, rec)
		if child.Model != "claude-tier-sonnet" {
			t.Errorf("the subagent ran on %q, want claude-tier-sonnet", child.Model)
		}
	})

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
