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
// prizmal binary against the stub switch. It asserts on the requests Claude
// Code sends, not on its output. A compaction request is the one whose
// instructions begin "CRITICAL: Respond with TEXT ONLY" (tQt in the 2.1.x
// binaries).
//
// Claude Code checks its threshold at the start of each query, a user turn,
// and counts the conversation from the previous assistant message's usage
// (tm, dCt in the 2.1.x binaries). So each arm runs three print-mode queries
// against one session, with --continue opening the same session as a new
// query: the second query's reply carries the usage the arm's plan reports,
// and the third query adds the ~84k-token estimate of its filler prompt.
//
// The launch states a 1M window in CLAUDE_CODE_AUTO_COMPACT_WINDOW. Claude
// Code's threshold for it is 967000 counted tokens: the window less the 20000
// output budget, less a 13000 margin. Four arms:
//
//   - proactive: the launch as it is. The count Claude Code reads at turn 3
//     is about 984000, past 967000, and it compacts before any error.
//   - proactive-control: the same launch through a `claude` shim that removes
//     the variable before it starts Claude Code. The window's source stays
//     "auto", the threshold check returns early, and no compaction request
//     appears at the same count. This is the bug the variable fixes.
//   - recognized-error: the shim again, and a count of about 924000, under
//     the threshold, so only the endpoint can start a compaction. The stub
//     refuses the turn-3 request the way a provider refuses a prompt past its
//     window, "prompt is too long: N tokens > M maximum". Claude Code must
//     compact then, and the retried turn must end in the stub's reply.
//   - unrecognized-error: the same refusal in a text Claude Code does not
//     recognize. Nothing compacts and the session fails, which is why the
//     Switch's refusal has to carry the recognized shape.
//
// Claude Code is the version the terminal baselines pin
// (testdata/terminal/claude/claude-code-version), so a release that changes
// the resolver or the error matcher fails here when the pin moves.

// compactMarker is the first line of every compaction request's instructions,
// whatever reply shape the request asks for: the analysis+summary shape, the
// summary-only shape, and the recent-portions shape all begin with it.
const compactMarker = "CRITICAL: Respond with TEXT ONLY"

// compactProactiveTokens is the count at which Claude Code compacts a session
// whose window the launch states as 1M: 1000000 less the 20000-token output
// budget, less the 13000 margin.
const compactProactiveTokens = 967000

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
	// dropWindowVar runs the launch through the shim that removes
	// CLAUDE_CODE_AUTO_COMPACT_WINDOW, the launch without the safeguard.
	dropWindowVar bool
	// plan is the stub's per-request input_tokens. Turn 1 answers plan[0],
	// turn 2 plan[1], and turn 3's request plan[2]; the last value repeats.
	plan []int
	// refusal, when set, is the message of the 400 the stub answers a request
	// with whose planned count passes the 1M window.
	refusal string
	// check asserts on what the arm saw.
	check func(t *testing.T, r compactResult)
}

// compactResult is what one arm's conversation produced.
type compactResult struct {
	requests int
	// markerIndex is the index of the first compaction request, or -1.
	markerIndex int
	// preTokens is the count Claude Code recorded when it compacted, or 0.
	preTokens int
	// stdout3 and stderr3 are the third query's output.
	stdout3, stderr3 string
}

// fail stops the arm with the third query's output attached.
func (r compactResult) fail(t *testing.T, format string, args ...any) {
	t.Helper()
	t.Fatalf(format+"\n--- turn3 stdout ---\n%s\n--- turn3 stderr ---\n%s", append(args, tail(r.stdout3, 40), tail(r.stderr3, 40))...)
}

func TestClaudeCompactsOnTheStatedWindowAndOnARecognizedRefusal(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Claude Code launches in short mode")
	}
	// Both workflows' require switches count: the claude-code workflow's
	// terminal-baselines job sets the baselines one, and a launch job the
	// harness one.
	require := os.Getenv(harnessLaunchRequireEnv) == "require" || os.Getenv(baselineRequireEnv) == "require"
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
	// The control arms run this same binary through a shim that removes the
	// window variable, so what they show is the missing variable and nothing
	// else: the stub, prompts, prizmal and Claude Code are the others'. It
	// needs no git ref, which a shallow CI checkout does not carry.
	shimDir := buildWindowVarShim(t)

	cases := []compactCase{
		{
			name: "proactive",
			plan: []int{1, 900000},
			check: func(t *testing.T, r compactResult) {
				switch {
				case r.markerIndex == -1:
					r.fail(t, "Claude Code never sent a compaction request with the window stated (%d requests)", r.requests)
				case r.markerIndex < 2:
					r.fail(t, "Claude Code compacted before the count crossed %d (marker request %d of %d)", compactProactiveTokens, r.markerIndex, r.requests)
				case r.preTokens < compactProactiveTokens:
					r.fail(t, "the compaction ran at %d counted tokens, below the threshold %d", r.preTokens, compactProactiveTokens)
				}
			},
		},
		{
			name:          "proactive-control",
			dropWindowVar: true,
			plan:          []int{1, 900000},
			check: func(t *testing.T, r compactResult) {
				if r.markerIndex != -1 {
					r.fail(t, "Claude Code sent a compaction request with no window stated (the control arm must stay silent): marker at request %d of %d", r.markerIndex, r.requests)
				}
			},
		},
		{
			name:          "recognized-error",
			dropWindowVar: true,
			plan:          []int{1, 840000, 1012345, 1},
			refusal:       "prompt is too long: {tokens} tokens > {limit} maximum",
			check: func(t *testing.T, r compactResult) {
				// Request 2 is the turn-3 request the stub refused, so the
				// compaction request must come after it, and at a count under
				// the proactive threshold: the refusal started it.
				switch {
				case r.markerIndex < 3:
					r.fail(t, "Claude Code did not compact after the recognized refusal (marker request %d of %d)", r.markerIndex, r.requests)
				case r.preTokens >= compactProactiveTokens:
					r.fail(t, "the compaction ran at %d counted tokens, past the proactive threshold, so the refusal did not start it", r.preTokens)
				case !strings.Contains(r.stdout3, stubserver.Reply):
					r.fail(t, "the retried turn did not end in the stub's reply")
				}
			},
		},
		{
			name:          "unrecognized-error",
			dropWindowVar: true,
			plan:          []int{1, 840000, 1012345},
			refusal:       "Provider returned error",
			check: func(t *testing.T, r compactResult) {
				switch {
				case r.markerIndex != -1:
					r.fail(t, "Claude Code compacted after a refusal it should not recognize (marker request %d of %d)", r.markerIndex, r.requests)
				case strings.Contains(r.stdout3, stubserver.Reply):
					r.fail(t, "the turn ended in the stub's reply, but the stub refused every request past the window")
				}
			},
		},
	}

	for _, tc := range cases {
		pathDirs := claudeDir
		if tc.dropWindowVar {
			pathDirs = shimDir + string(filepath.ListSeparator) + claudeDir
		}
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			plan := stubserver.NewUsagePlan(tc.plan...)
			if tc.refusal != "" {
				plan.OverflowAbove(1_000_000, tc.refusal)
			}
			rec := &stubserver.Recorder{}
			srv := stubserver.NewServer(
				stubserver.WithModels("stub-model"),
				stubserver.WithUsagePlan(plan),
				stubserver.WithRecorder(rec),
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

			// runPrint runs one print-mode query. A prompt argument rides the
			// command line; stdin carries a prompt too large for it, because
			// Linux caps one argument at 128 KB.
			runPrint := func(t *testing.T, stdin string, promptArgs ...string) (string, string) {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), harnessLaunchTimeout)
				defer cancel()
				args := append([]string{"--yes", "-m", "stub-model", "claude", "--", "--print"}, promptArgs...)
				cmd := exec.CommandContext(ctx, prizmalBin, args...)
				cmd.Dir = project
				cmd.Env = []string{
					"HOME=" + home,
					"PATH=" + pathDirs + string(filepath.ListSeparator) + os.Getenv("PATH"),
					shimRealEnv + "=" + claudePath,
					"TMPDIR=" + os.TempDir(),
					"LANG=C.UTF-8",
					"TERM=dumb",
					"NO_COLOR=1",
					"DISABLE_AUTOUPDATER=1",
				}
				var stdout, stderr strings.Builder
				cmd.Stdout = &stdout
				cmd.Stderr = &stderr
				if stdin != "" {
					cmd.Stdin = strings.NewReader(stdin)
				}
				// A harness can leave child processes holding stdout after the
				// timeout kills it. WaitDelay stops Wait from blocking on them.
				cmd.WaitDelay = 10 * time.Second
				_ = cmd.Run()
				return stdout.String(), stderr.String()
			}

			// Turn 1: the plain prompt, whose reply carries the first anchor.
			_, stderr1 := runPrint(t, "", "Reply with a short greeting.")
			// Turn 2: its reply carries the second report. Its own threshold
			// check reads turn 1's anchor, far under any threshold, so nothing
			// compacts yet.
			_, stderr2 := runPrint(t, "", "--continue", "Reply again.")
			// Turn 3: the threshold check reads turn 2's anchor plus this
			// turn's filler estimate. The filler also keeps the sending path
			// reachable: without it the whole report sits in the fixed
			// prefix, and the reactive compaction bails on an empty summarize
			// set before it sends anything.
			stdout3, stderr3 := runPrint(t, "Summarize this filler in one word: "+strings.Repeat("filler ", 48000), "--continue")

			requests := plan.Requests()
			if requests < 3 {
				t.Fatalf("the conversation did not reach the third turn: %d requests\n--- turn1 stderr ---\n%s\n--- turn2 stderr ---\n%s\n--- turn3 stdout ---\n%s\n--- turn3 stderr ---\n%s", requests, tail(stderr1, 30), tail(stderr2, 30), tail(stdout3, 30), tail(stderr3, 30))
			}

			// The compaction request is a request like the others: it names the
			// session model, a name the tenant routes.
			assertServed(t, rec)

			markerIndex := -1
			for i, body := range plan.Bodies() {
				if compactSent([]map[string]any{body}) {
					markerIndex = i
					break
				}
			}
			tc.check(t, compactResult{
				requests:    requests,
				markerIndex: markerIndex,
				// preTokens is the count Claude Code read when it compacted,
				// recorded on the session transcript's compact_boundary row.
				preTokens: readCompactPreTokens(t, home),
				stdout3:   stdout3,
				stderr3:   stderr3,
			})
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

// shimRealEnv names the variable that tells the shim where the real Claude
// Code is.
const shimRealEnv = "PRIZMAL_COMPACT_SHIM_REAL"

// windowVarShimSource is a `claude` stand-in for the control arms. It removes
// CLAUDE_CODE_AUTO_COMPACT_WINDOW from the environment, then replaces itself
// with the real Claude Code, which sees the launch exactly as prizmal made it
// less the one variable.
const windowVarShimSource = `package main

import (
	"os"
	"strings"
	"syscall"
)

func main() {
	real := os.Getenv("` + shimRealEnv + `")
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "` + windowEnvName + `=") {
			env = append(env, kv)
		}
	}
	if err := syscall.Exec(real, append([]string{real}, os.Args[1:]...), env); err != nil {
		os.Stderr.WriteString("shim: " + err.Error() + "\n")
		os.Exit(1)
	}
}
`

// windowEnvName is the variable the launch states the window in.
const windowEnvName = "CLAUDE_CODE_AUTO_COMPACT_WINDOW"

// buildWindowVarShim builds the control arms' shim into a directory of its
// own and returns the directory, ready to lead PATH. The binary is named
// claude, because prizmal finds Claude Code by that name.
func buildWindowVarShim(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "shim.go")
	if err := os.WriteFile(src, []byte(windowVarShimSource), 0o644); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", filepath.Join(binDir, "claude"), src)
	build.Dir = dir
	build.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build the control arms' claude shim: %v\n%s", err, out)
	}
	return binDir
}

// readCompactPreTokens reads the compacted conversation's preTokens from the
// session transcript under HOME: the token count Claude Code acted on when
// it compacted, recorded on the compact_boundary system row. A session that
// never compacted has no row, and the zero return reads as "no record".
func readCompactPreTokens(t *testing.T, home string) int {
	t.Helper()
	projects := filepath.Join(home, ".claude", "projects")
	rows, err := os.ReadDir(projects)
	if err != nil {
		return 0
	}
	count := 0
	for _, row := range rows {
		if !row.IsDir() {
			continue
		}
		matches, err := filepath.Glob(filepath.Join(projects, row.Name(), "*.jsonl"))
		if err != nil {
			continue
		}
		for _, match := range matches {
			data, err := os.ReadFile(match)
			if err != nil {
				continue
			}
			for _, line := range strings.Split(string(data), "\n") {
				var rec struct {
					Subtype string `json:"subtype"`
					Meta    struct {
						PreTokens int `json:"preTokens"`
					} `json:"compactMetadata"`
				}
				if err := json.Unmarshal([]byte(line), &rec); err != nil {
					continue
				}
				if rec.Subtype == "compact_boundary" && rec.Meta.PreTokens > count {
					count = rec.Meta.PreTokens
				}
			}
		}
	}
	return count
}
