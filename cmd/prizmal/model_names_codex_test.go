//go:build !windows

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/stubserver"
)

// The Switch is about to stop serving a model name that is neither a router
// config nor an alias with the key's bound config. These tests drive the real
// Codex through prizmal against a stub that refuses such a name the same way,
// and show that every request Codex made carried a name the tenant routes.
//
// Each scenario reaches a model slot Codex fills on its own:
//
//   - a turn: the session model.
//   - /compact: the compaction request, which Codex sends on the session model.
//   - codex exec review: the review thread, which uses review_model or the
//     session model.
//   - a spawned subagent: agents.default_subagent_model or the session model.
//
// The catalog lists three names. The stub serves those and refuses anything
// else with HTTP 400 and a model_not_found error.

// reviewPrompt is the start of the instruction Codex gives a review thread
// that reads the uncommitted changes.
const reviewPrompt = "Review the current code changes"

const (
	namesFlash    = "prizmal-flash"
	namesFrontier = "prizmal-frontier"
	namesCore     = "prizmal-core"
)

// namesCatalog is the tenant: three router configs and no Claude tier.
var namesCatalog = []string{namesFlash, namesFrontier, namesCore}

// codexNamesCase builds a Codex scenario against the strict stub. The scenario
// runs from a git repository with one uncommitted change, so a review has a
// diff to read.
func codexNamesCase(name string, rec *stubserver.Recorder, args, after []string, steps ...baselineStep) baselineCase {
	return baselineCase{
		name: name, cols: 100, rows: 30,
		args: args, argsAfter: after, codex: true,
		catalog: namesCatalog, recorder: rec, steps: steps,
		setup: seedGitProject,
	}
}

// seedGitProject makes the project a git repository with a commit and an
// uncommitted edit.
func seedGitProject(t *testing.T, home, project string) {
	t.Helper()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = project
		cmd.Env = append(os.Environ(), "HOME="+home, "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(project, "main.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "first")
	if err := os.WriteFile(filepath.Join(project, "main.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// codexOnPath returns the directory of the Codex on PATH. The scenarios run
// the installed Codex, not the pinned one, because they read requests rather
// than screens.
func codexOnPath(t *testing.T) string {
	t.Helper()
	return filepath.Dir(harnessLaunchOnPath(t, "codex"))
}

// modelsOf lists the model of every request the recorder kept, in order.
func modelsOf(rec *stubserver.Recorder) []string {
	var models []string
	for _, call := range rec.ModelCalls() {
		models = append(models, call.Model)
	}
	return models
}

// assertServed fails when the stub refused any name, or saw none.
func assertServed(t *testing.T, rec *stubserver.Recorder) {
	t.Helper()
	if len(rec.ModelCalls()) == 0 {
		t.Fatal("the harness sent the stub no inference request")
	}
	if refused := rec.RefusedModels(); len(refused) > 0 {
		t.Errorf("the harness sent model names the Switch would refuse: %q (all names sent: %q)", refused, modelsOf(rec))
	}
}

// subagentCall returns the first request the spawned subagent made: the one
// that holds the task the stub handed it and not the prompt that made the
// parent spawn it.
func subagentCall(rec *stubserver.Recorder) (stubserver.ModelCall, bool) {
	for _, call := range rec.ModelCalls() {
		raw, _ := json.Marshal(call.Body)
		if strings.Contains(string(raw), stubserver.SpawnedTask) && !strings.Contains(string(raw), "stub-spawn-") {
			return call, true
		}
	}
	return stubserver.ModelCall{}, false
}

// callWith returns the first recorded request whose body contains text.
func callWith(rec *stubserver.Recorder, text string) (stubserver.ModelCall, bool) {
	for _, call := range rec.ModelCalls() {
		raw, _ := json.Marshal(call.Body)
		if strings.Contains(string(raw), text) {
			return call, true
		}
	}
	return stubserver.ModelCall{}, false
}

func runCodexNames(t *testing.T, tc baselineCase) *stubserver.Recorder {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Codex launches in short mode")
	}
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux is not on PATH")
	}
	tc.codexDir = codexOnPath(t)
	prizmalBin := buildPrizmal(t)
	_, _ = renderBaseline(t, tmuxPath, prizmalBin, "", tc)
	return tc.recorder
}

func TestCodexSendsOnlyRoutableNames(t *testing.T) {
	t.Run("turn", func(t *testing.T) {
		rec := &stubserver.Recorder{}
		runCodexNames(t, codexNamesCase("turn", rec, []string{"-m", namesFlash, "codex"}, nil,
			stepCodexPrompt, stepCodexSendHi, stepCodexSubmit))
		assertServed(t, rec)
		for _, m := range modelsOf(rec) {
			if m != namesFlash {
				t.Errorf("a request named %q, want the session model %q", m, namesFlash)
			}
		}
	})

	t.Run("compact", func(t *testing.T) {
		rec := &stubserver.Recorder{}
		runCodexNames(t, codexNamesCase("compact", rec, []string{"-m", namesFlash, "codex"}, nil,
			stepCodexPrompt, stepCodexSendHi, stepCodexSubmit,
			baselineStep{literal: "/compact", waitFor: "/compact"},
			baselineStep{key: "Enter", waitFor: "Context compacted"}))
		assertServed(t, rec)
		if len(rec.ModelCalls()) < 2 {
			t.Fatalf("compaction sent no request: %q", modelsOf(rec))
		}
		for _, m := range modelsOf(rec) {
			if m != namesFlash {
				t.Errorf("a request named %q, want the session model %q", m, namesFlash)
			}
		}
	})

	t.Run("review", func(t *testing.T) {
		rec := &stubserver.Recorder{}
		runCodexNames(t, codexNamesCase("review", rec, []string{"-m", namesFlash, "codex"},
			[]string{"exec", "review", "--uncommitted"}, stepPrizmalExit))
		assertServed(t, rec)
		if _, ok := callWith(rec, reviewPrompt); !ok {
			t.Fatalf("no request carried the review prompt: %q", modelsOf(rec))
		}
	})

	// --subagent-model names the model /review and spawned subagents run.
	t.Run("review-on-the-subagent-model", func(t *testing.T) {
		rec := &stubserver.Recorder{}
		runCodexNames(t, codexNamesCase("review-sub", rec, []string{"-m", namesFlash, "--subagent-model", namesCore, "codex"},
			[]string{"exec", "review", "--uncommitted"}, stepPrizmalExit))
		assertServed(t, rec)
		call, ok := callWith(rec, reviewPrompt)
		if !ok {
			t.Fatalf("no request carried the review prompt: %q", modelsOf(rec))
		}
		if call.Model != namesCore {
			t.Errorf("the review ran on %q, want the subagent model %q", call.Model, namesCore)
		}
	})

	t.Run("subagent", func(t *testing.T) {
		rec := &stubserver.Recorder{}
		runCodexNames(t, codexNamesCase("subagent", rec, []string{"-m", namesFlash, "codex"},
			[]string{"exec", "--skip-git-repo-check", "-s", "read-only", stubserver.SpawnTrigger}, stepPrizmalExit))
		assertServed(t, rec)
		child, ok := subagentCall(rec)
		if !ok {
			t.Fatalf("Codex spawned no subagent: %q", modelsOf(rec))
		}
		if child.Model != namesFlash {
			t.Errorf("the subagent ran on %q, want the session model %q", child.Model, namesFlash)
		}
	})

	t.Run("subagent-on-the-subagent-model", func(t *testing.T) {
		rec := &stubserver.Recorder{}
		runCodexNames(t, codexNamesCase("subagent-sub", rec, []string{"-m", namesFlash, "--subagent-model", namesCore, "codex"},
			[]string{"exec", "--skip-git-repo-check", "-s", "read-only", stubserver.SpawnTrigger}, stepPrizmalExit))
		assertServed(t, rec)
		child, ok := subagentCall(rec)
		if !ok {
			t.Fatalf("Codex spawned no subagent: %q", modelsOf(rec))
		}
		if child.Model != namesCore {
			t.Errorf("the subagent ran on %q, want the subagent model %q", child.Model, namesCore)
		}
	})
}
