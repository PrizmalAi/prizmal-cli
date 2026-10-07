//go:build !windows

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PrizmalAi/prizmal-cli/internal/stubserver"
)

// The compaction end-to-end tests run the real Claude Code through a real
// prizmal binary against the stub switch, whose answers report a usage the
// test aims at the compaction threshold. They assert on the requests Claude
// Code sends, not on its output: when the count crosses the compaction
// threshold the launch stated, the client must summarize the conversation —
// its summary request's instructions begin "CRITICAL: Respond with TEXT ONLY"
// (tQt in the 2.1.x binaries) — before that turn's model request goes out.
// The control arm, a launch without the window stated, must send no summary
// request at all, because the window's source stays "auto" and Claude Code's
// threshold check returns early while it is.
//
// Claude Code checks the threshold at the start of each query — a user turn —
// and anchors the count on the previous assistant message's usage (tm, dCt in
// the 2.1.x binaries), so the test runs two print-mode queries against one
// session: the first sets the anchor by reporting a usage past the threshold,
// and the second must compact before its request. --continue opens the same
// session as a new query.
//
// Claude Code is the version the terminal baselines pin
// (testdata/terminal/claude/claude-code-version), so a release that changes
// the resolver fails here when the pin moves.

// compactMarker is the first line of every compaction request's instructions,
// whatever reply shape the request asks for: the analysis+summary shape, the
// summary-only shape, and the recent-portions shape all begin with it.
const compactMarker = "CRITICAL: Respond with TEXT ONLY"

// compactSent reports whether any request Claude Code sent carried the
// compaction instructions.
func compactSent(bodies []map[string]any) bool {
	for _, body := range bodies {
		raw, err := json.Marshal(body["messages"])
		if err != nil {
			continue
		}
		if strings.Contains(string(raw), compactMarker) {
			return true
		}
	}
	return false
}

// compactCase is one arm of the compaction test.
type compactCase struct {
	name string
	// statedWindow runs the launch that states the window (the safeguard).
	// false runs the control arm, where prizmal states none.
	statedWindow bool
}

func TestClaudeCompactRequestFollowsTheStatedWindow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Claude Code launches in short mode")
	}
	require := os.Getenv(harnessLaunchRequireEnv) == "require"
	skipOrFail := func(t *testing.T, format string, args ...any) {
		t.Helper()
		if require {
			t.Fatalf(format, args...)
		}
		t.Skipf(format, args...)
	}

	claudePath, err := exec.LookPath("claude")
	if err != nil {
		skipOrFail(t, "claude is not on PATH")
	}
	claudeDir := filepath.Dir(claudePath)
	if _, reason := pinnedClaudeDir(t); reason != "" {
		skipOrFail(t, "%s", reason)
	}

	prizmalBin := buildPrizmal(t, "HEAD")
	// The control binary is prizmal without this change: the parent commit's
	// tree, archived and built in a temp dir. The same stub, prompts and
	// Claude Code run against a launch that states no compaction window, so
	// the arm's silence is the safeguard's absence, not a difference in the
	// conversation.
	prizmalBinBefore := buildPrizmalFromCommit(t, "HEAD^")

	for _, tc := range []compactCase{
		{name: "with-the-stated-window", statedWindow: true},
		{name: "control-no-stated-window"},
	} {
		prizmalUnderTest := prizmalBin
		if !tc.statedWindow {
			prizmalUnderTest = prizmalBinBefore
		}
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// The turn-2 reply reports 900000 used. The turn-3 query's
			// threshold check reads that anchor against the launch's 882000
			// threshold, and the turn-2 assistant is still in the summarize
			// set — with only two turns the fixed prefix is everything and
			// the reactive compaction bails with an empty set — so the
			// summary request goes out before turn 3's model request. The
			// control arm's own threshold, derived from its 1M window for the
			// unknown model (967000), is beyond anything three turns of this
			// shape carry, so its silence is the plan's doing, not the end of
			// the conversation: every query ends in the stub's reply.
			plan := stubserver.NewUsagePlan(1, 900000, 900000, 900000, 900000, 900000)
			srv := stubserver.NewServer(
				stubserver.WithModels("stub-model"),
				stubserver.WithUsagePlan(plan),
			)
			t.Cleanup(srv.Close)

			// One HOME and one project across both queries, so --continue
			// opens the first query's session.
			root, err := os.MkdirTemp("", "prizmal-compact-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(root) })
			home := filepath.Join(root, "home")
			project := filepath.Join(root, "project")
			if err := os.MkdirAll(filepath.Join(home, ".prizmal"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(project, 0o755); err != nil {
				t.Fatal(err)
			}
			writeJSONFile(t, filepath.Join(home, ".prizmal", "config.json"), map[string]any{
				"version":  1,
				"base_url": srv.URL,
				"api_key":  stubserver.StubKey,
			})
			// The Claude Code state the baselines seed: onboarding done and
			// the project trusted, so the run is a plain session, not a setup
			// flow.
			seedClaudeState(t, home, project)

			runPrint := func(t *testing.T, extraArgs ...string) (string, string) {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), harnessLaunchTimeout)
				defer cancel()
				args := append([]string{"--yes", "-m", "stub-model", "claude", "--", "--print"}, extraArgs...)
				cmd := exec.CommandContext(ctx, prizmalUnderTest, args...)
				cmd.Dir = project
				cmd.Env = []string{
					"HOME=" + home,
					"PATH=" + claudeDir + string(filepath.ListSeparator) + os.Getenv("PATH"),
					"TMPDIR=" + os.TempDir(),
					"LANG=C.UTF-8",
					"TERM=dumb",
					"NO_COLOR=1",
					"DISABLE_AUTOUPDATER=1",
				}
				var stdout, stderr strings.Builder
				cmd.Stdout = &stdout
				cmd.Stderr = &stderr
				cmd.Stdin = nil
				// A harness can leave child processes holding stdout after the
				// timeout kills it. WaitDelay stops Wait from blocking on them.
				cmd.WaitDelay = 10 * time.Second
				_ = cmd.Run()
				return stdout.String(), stderr.String()
			}

			// Turn 1: the plain prompt, whose reply carries a 1-token anchor.
			_, stderr1 := runPrint(t, "Reply with a short greeting.")
			// Turn 2: its reply carries the 900000 anchor. --continue opens
			// the previous session as a new query. The prompt carries a large
			// filler so the conversation's own estimate covers most of the
			// reported usage: the compaction's fixed-prefix check (the usage
			// beyond what the messages themselves explain) must stay under
			// the threshold, or the reactive compaction bails with an empty
			// summarize set, which is the state of a lie a small conversation
			// cannot survive.
			_, stderr2 := runPrint(t, "--continue", "Summarize this filler in one word: "+strings.Repeat("filler ", 48000))
			// Turn 3: the threshold check reads the 900000 anchor from turn 2
			// against the stated threshold. This is the turn that must
			// compact.
			stdout3, stderr3 := runPrint(t, "--continue", "go on")

			requests := plan.Requests()
			if requests < 3 {
				t.Fatalf("the conversation did not reach the third turn: %d requests\n--- turn1 stderr ---\n%s\n--- turn2 stderr ---\n%s\n--- turn3 stdout ---\n%s\n--- turn3 stderr ---\n%s", requests, tail(stderr1, 30), tail(stderr2, 30), tail(stdout3, 30), tail(stderr3, 30))
			}

			sent := compactSent(plan.Bodies())
			switch {
			case tc.statedWindow && !sent:
				t.Fatalf("Claude Code never sent a compaction request with the window stated (%d requests):\n--- turn3 stdout ---\n%s\n--- turn3 stderr ---\n%s", requests, tail(stdout3, 40), tail(stderr3, 40))
			case !tc.statedWindow && sent:
				t.Fatalf("Claude Code sent a compaction request with no window stated (the control arm must stay silent): %d requests\n--- turn3 stdout ---\n%s\n--- turn3 stderr ---\n%s", requests, tail(stdout3, 40), tail(stderr3, 40))
			}
		})
	}
}

// buildPrizmal builds the working tree's prizmal binary.
func buildPrizmal(t *testing.T, _ string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "prizmal")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build prizmal: %v\n%s", err, out)
	}
	return bin
}

// buildPrizmalFromCommit builds prizmal from a commit's tree, extracted into
// a temp dir, so the control arm runs the launch WITHOUT the safeguard and
// everything else the working tree carries is exactly the tested change's
// parent. git archive is read-only; the temp dir belongs to the test.
func buildPrizmalFromCommit(t *testing.T, commit string) string {
	t.Helper()
	root := t.TempDir()
	tree := filepath.Join(root, "tree")
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatal(err)
	}
	archive := exec.Command("git", "archive", "--format=tar", commit)
	archive.Dir = repoRoot(t)
	out, err := archive.Output()
	if err != nil {
		t.Fatalf("git archive %s: %v\n%s", commit, err, out)
	}
	cmd := exec.Command("tar", "-x", "-C", tree)
	cmd.Stdin = strings.NewReader(string(out))
	if outb, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("extract %s: %v\n%s", commit, err, outb)
	}
	if _, err := os.Stat(filepath.Join(tree, "go.mod")); err != nil {
		t.Fatalf("the extracted tree %s has no go.mod (archive %d bytes): %v", tree, len(out), err)
	}
	bin := filepath.Join(root, "prizmal")
	build := exec.Command("go", "build", "-o", bin, "./cmd/prizmal")
	build.Dir = tree
	build.Env = append(os.Environ(), "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build prizmal from %s (dir %s): %v\n%s", commit, tree, err, out)
	}
	return bin
}

// repoRoot is the repository root above the test's package directory, found
// by the go.mod marker, so a git command run from a test binary whose cwd is
// cmd/prizmal covers the whole tree.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test's directory")
		}
		dir = parent
	}
}
