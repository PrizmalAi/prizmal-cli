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

// The compaction end-to-end test runs the real Claude Code through a real
// prizmal binary against the stub switch, whose answers report a usage the
// test aims at the compaction threshold. It asserts on the requests Claude
// Code sends, not on its output: when the count crosses the compaction
// threshold the launch stated, the client must summarize the conversation,
// and its summary request's instructions begin "CRITICAL: Respond with TEXT
// ONLY" (tQt in the 2.1.x binaries). The control arm, the parent commit's
// prizmal, states no window, so the window's source stays "auto", Claude
// Code's threshold check returns early while it is, and no summary request
// may appear.
//
// Claude Code checks the threshold at the start of each query — a user turn —
// and counts the conversation from the previous assistant message's usage
// (tm, dCt in the 2.1.x binaries), so the test runs three print-mode queries
// against one session: the second query's reply carries the usage the plan
// reports, and the third query must compact before its own model request.
// --continue opens the same session as a new query.
//
// The two arms share everything but the stub's reported usage and the launch
// binary. The with arm's report sits above its own blocked level, the point
// where this conversation's reactive flow sends the summary request: the raw
// window (1M) less its output budget (20000) less the blocking margin (3000),
// plus the ~84k-token estimate the turn-3 prompt's filler contributes, so
// 900000 reports a count of about 984060. The control arm's report keeps the
// same shape's count under its own auto threshold, 967000: 840000 reports
// about 924000. Both arms end every query in the stub's reply, so neither
// silence nor sending is the end of the conversation.
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
	// false runs the control arm, the parent commit's prizmal.
	statedWindow bool
}

// compactPlans returns the stub's per-request input_tokens for one arm. Turn
// 1 answers 1; turn 2 onward answers the arm's anchored report, and the last
// value repeats when the list runs out.
func compactPlans(statedWindow bool) []int {
	if statedWindow {
		return []int{1, 900000, 900000, 900000, 900000, 900000}
	}
	return []int{1, 840000, 840000, 840000, 840000, 840000}
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

	prizmalBin := buildPrizmal(t)
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
			plan := stubserver.NewUsagePlan(compactPlans(tc.statedWindow)...)
			srv := stubserver.NewServer(
				stubserver.WithModels("stub-model"),
				stubserver.WithUsagePlan(plan),
			)
			t.Cleanup(srv.Close)

			// One HOME and one project across all three queries, so
			// --continue opens the previous query's session.
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

			runPrint := func(t *testing.T, promptArgs ...string) (string, string) {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), harnessLaunchTimeout)
				defer cancel()
				args := append([]string{"--yes", "-m", "stub-model", "claude", "--", "--print"}, promptArgs...)
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
			// Turn 2: its reply carries the arm's anchored report.
			_, stderr2 := runPrint(t, "--continue", "Reply again.")
			// Turn 3: the threshold check reads turn 2's anchor. The filler
			// makes the messages after the anchor cover most of the report,
			// which keeps this conversation's sending path reachable: without
			// it the whole report sits in the fixed prefix, and the reactive
			// compaction bails on an empty summarize set before it sends
			// anything.
			stdout3, stderr3 := runPrint(t, "--continue", "Summarize this filler in one word: "+strings.Repeat("filler ", 48000))

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
func buildPrizmal(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "prizmal")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build prizmal: %v\n%s", err, out)
	}
	return bin
}

// buildPrizmalFromCommit builds prizmal from a commit's tree, extracted into
// a temp dir, so the control arm runs the launch WITHOUT the safeguard and
// nothing else about it differs from the tested change's parent. git archive
// is read-only; the temp dir belongs to the test.
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