package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PrizmalAi/prizmal-cli/internal/device"
	"github.com/PrizmalAi/prizmal-cli/internal/stubserver"
)

// The compaction-window tests prove, for each harness the launcher starts, that
// the harness compacts at the context window the launch declares. Each one runs
// the real harness binary through a real prizmal binary against the stub
// switch, and the stub reports a token count for each reply the way a provider
// does. A harness reads that count to decide when its conversation is too long,
// so the test moves the count to either side of the harness's own threshold and
// asserts on the requests the harness sends: a compaction request appears one
// token past the threshold and does not appear at it. A harness that ignored the
// declared window would compact at its own default, and the below-threshold arm
// would see a compaction it should not.
//
// The counts are the tokens the harness adds up from a reply: the prompt tokens
// the stub reports plus the one completion token it adds.
//
// Claude Code's proof lives in claude_compact_e2e_test.go, which also drives the
// control launches that show what happens without the window.
//
// A harness at a version other than the one the terminal baselines pin skips,
// because a threshold is a property of a release. Under PRIZMAL_HARNESS_LAUNCH=
// require the skip is a failure, so CI cannot pass by skipping.

// compactionWindow is the window every harness launch declares.
const compactionWindow = 1_000_000

// compactionFiller is a prompt big enough to leave older turns for a harness to
// summarise: every harness keeps its most recent 20,000 tokens or so verbatim,
// so two turns of this size put the first one past that. It travels on stdin:
// Linux refuses an argument over 128 KiB and Windows one over 32 KiB, or 8 KiB
// through an npm .cmd shim.
var compactionFiller = "Summarize this filler in one word: " + strings.Repeat("filler ", 16000)

// compactionCase is one harness's proof.
type compactionCase struct {
	harness string
	label   string
	// versionFile and versionField name the pinned release and where in
	// `<binary> --version` it appears.
	versionFile  string
	versionField int
	// threshold is the first counted token total at which the harness
	// compacts; one less must not compact it.
	threshold int
	// offset says how the threshold follows from the declared window.
	offset string
	// markers identify the harness's compaction request in a request body.
	markers []string
	// replies is the stub's prompt_tokens for each request, in order, given the
	// prompt tokens the reply the harness's threshold check reads must carry.
	// The stub adds one completion token to each.
	replies func(prompt int) []int
	// drive runs the harness's turns.
	drive func(t *testing.T, s *compactionSession)
	// run, when set, replaces the two-arm check with the harness's own proof.
	run func(t *testing.T, c compactionCase, harnessDir string)
}

// compactionSession is one arm's sandbox: a stub switch with a usage plan, and
// the prizmal and harness binaries to run against it.
type compactionSession struct {
	harness     string
	prizmalBin  string
	harnessPath string
	home        string
	project     string
	plan        *stubserver.UsagePlan
}

// run launches the harness through prizmal with args after the `--`, and
// returns what it printed. stdin carries a prompt too large for an argument.
func (s *compactionSession) run(t *testing.T, stdin string, args ...string) (string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), harnessLaunchTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.prizmalBin, append([]string{"-y", "-m", harnessLaunchModel, s.harness, "--"}, args...)...)
	cmd.Dir = s.project
	cmd.Env = append(homeEnv(s.home),
		"PATH="+filepath.Dir(s.harnessPath)+string(filepath.ListSeparator)+os.Getenv("PATH"),
		"LANG=C.UTF-8",
		"TERM=dumb",
		"NO_COLOR=1",
		"DISABLE_AUTOUPDATER=1",
		"CLINE_NO_AUTO_UPDATE=1",
	)
	cmd.Env = append(cmd.Env, device.TestingEnv()...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	cmd.WaitDelay = 10 * time.Second
	_ = cmd.Run()
	return stdout.String(), stderr.String()
}

// compactions counts the compaction requests the stub has served.
func (s *compactionSession) compactions() int {
	count := 0
	for _, body := range s.plan.Bodies() {
		if s.plan.IsCompaction(body) {
			count++
		}
	}
	return count
}

// pinnedBinary returns the harness's directory when the installed release is
// the pinned one, and skips (or fails, under require) otherwise.
func (c compactionCase) pinnedBinary(t *testing.T) string {
	t.Helper()
	dir, reason := pinnedHarnessDir(t, c.harness, c.label, c.versionFile, c.versionField)
	if reason != "" {
		if os.Getenv(harnessLaunchRequireEnv) == "require" || os.Getenv(baselineRequireEnv) == "require" {
			t.Fatalf("%s", reason)
		}
		t.Skipf("%s", reason)
	}
	return dir
}

// newSession builds an arm's sandbox around the plan, with tool steps the stub
// answers the first chat requests with when a harness needs a tool loop.
func (c compactionCase) newSession(t *testing.T, harnessDir string, plan *stubserver.UsagePlan, steps ...stubserver.ToolStep) *compactionSession {
	t.Helper()
	opts := []stubserver.ServerOption{stubserver.WithModels(harnessLaunchModel), stubserver.WithUsagePlan(plan)}
	if len(steps) > 0 {
		opts = append(opts, stubserver.WithToolSteps(steps...))
	}
	srv := stubserver.NewServer(opts...)
	t.Cleanup(srv.Close)
	prizmalBin, home, project := harnessLaunchSandbox(t, srv.URL, false)
	return &compactionSession{
		harness:     c.harness,
		prizmalBin:  prizmalBin,
		harnessPath: harnessBinary(harnessDir, c.harness),
		home:        home,
		project:     project,
		plan:        plan,
	}
}

func TestHarnessCompactsAtTheDeclaredWindow(t *testing.T) {
	skipHarnessLaunchInShortMode(t)
	for _, c := range compactionCases {
		t.Run(c.harness, func(t *testing.T) {
			harnessDir := c.pinnedBinary(t)
			if c.run != nil {
				c.run(t, c, harnessDir)
				return
			}
			for _, arm := range []struct {
				name    string
				count   int
				compact bool
			}{
				{"below", c.threshold - 1, false},
				{"at", c.threshold, true},
			} {
				t.Run(arm.name, func(t *testing.T) {
					t.Parallel()
					// The count is the prompt tokens plus the one completion
					// token the stub adds to each reply.
					plan := stubserver.NewUsagePlan(c.replies(arm.count - 1)...).CompactionMarkers(c.markers...)
					s := c.newSession(t, harnessDir, plan)
					c.drive(t, s)
					// A harness that never started would also see no
					// compaction, so the below arm needs the turns to have run.
					if got := plan.Requests(); got < 3 {
						t.Fatalf("%s sent %d requests, want at least 3; the turns did not run", c.harness, got)
					}
					got := s.compactions()
					switch {
					case arm.compact && got == 0:
						t.Fatalf("%s did not compact at %d counted tokens (%s); %d requests", c.harness, arm.count, c.offset, plan.Requests())
					case !arm.compact && got > 0:
						t.Fatalf("%s compacted at %d counted tokens, one under the threshold %d (%s)", c.harness, arm.count, c.threshold, c.offset)
					}
				})
			}
		})
	}
}

// withPrompt appends prompt to args, or leaves it out when the prompt arrives on
// stdin instead.
func withPrompt(args []string, prompt string) []string {
	if prompt == "" {
		return args
	}
	return append(args, prompt)
}

// threeTurns drives a harness that continues its last session: two turns of
// filler, so the first one falls outside the recent turns the harness keeps
// verbatim, then a short turn, whose threshold check reads the count the
// second turn's reply carried.
func threeTurns(first, resume func(prompt string) []string) func(t *testing.T, s *compactionSession) {
	return func(t *testing.T, s *compactionSession) {
		t.Helper()
		s.run(t, compactionFiller, first("")...)
		s.run(t, compactionFiller, resume("")...)
		s.run(t, "", resume("Reply again.")...)
	}
}

// compactionCases has one entry per harness this file drives. Claude Code is
// driven by claude_compact_e2e_test.go, which needs its control launches.
var compactionCases = []compactionCase{
	{
		harness: "pi", label: "Pi",
		versionFile: filepath.Join("testdata", "harness", "pi-version"), versionField: 0,
		// Pi compacts after a reply once the counted tokens pass the window less
		// its 16,384-token reserve. The check runs before the next prompt.
		threshold: compactionWindow - 16384 + 1,
		offset:    "the window less Pi's 16,384-token reserve, strictly passed",
		markers:   []string{"You are a context summarization assistant"},
		replies:   func(prompt int) []int { return []int{1, prompt, 1} },
		drive: threeTurns(
			func(p string) []string { return withPrompt([]string{"--print"}, p) },
			func(p string) []string { return withPrompt([]string{"--print", "--continue"}, p) },
		),
	},
	{
		harness: "codex", label: "Codex",
		versionFile: filepath.Join(codexBaselineDir, "codex-version"), versionField: -1,
		// Codex derives its auto-compaction limit from the catalog's
		// context_window: 90 percent of it.
		threshold: compactionWindow * 9 / 10,
		offset:    "90 percent of the window the catalog declares",
		markers:   []string{"CONTEXT CHECKPOINT COMPACTION"},
		replies:   func(prompt int) []int { return []int{1, prompt, 1} },
		drive: threeTurns(
			func(p string) []string {
				return withPrompt([]string{"exec", "--skip-git-repo-check", "-s", "read-only"}, p)
			},
			func(p string) []string {
				return withPrompt([]string{"exec", "--skip-git-repo-check", "-s", "read-only", "resume", "--last"}, p)
			},
		),
	},
	{
		harness: "opencode", label: "OpenCode",
		versionFile: filepath.Join(opencodeBaselineDir, "opencode-version"), versionField: -1,
		// OpenCode compacts when the counted tokens reach the model's limit.context
		// less the 32,000-token output budget limit.output declares.
		threshold: compactionWindow - 32000,
		offset:    "the window less the 32,000-token output budget",
		markers:   []string{"Create a new anchored summary"},
		// OpenCode asks for a session title beside the first reply, and either
		// request can be served first, so both slots carry the count.
		replies: func(prompt int) []int { return []int{prompt, prompt, 1} },
		drive: threeTurns(
			func(p string) []string { return withPrompt([]string{"run"}, p) },
			func(p string) []string { return withPrompt([]string{"run", "--continue"}, p) },
		),
	},
	{
		harness: "cline", label: "Cline",
		versionFile: filepath.Join("testdata", "terminal", "cline", "cline-version"), versionField: 0,
		threshold: clineTriggerTokens,
		offset:    "90 percent of a fourth of the window, when the provider reports at least four times what Cline estimates",
		markers:   []string{"Summarize this session for continuation"},
		run:       proveClineCompaction,
	},
}

// Cline cannot continue a session headlessly, so its proof is one run: a tool
// loop of clineSteps calls, each returning a file of the same size, grows the
// conversation a step at a time while every reply reports a count far past the
// window. Cline scales its input budget down by how far the provider's count
// exceeds its own character estimate, by at most a factor of four, then
// compacts at 90 percent of that budget. So with a declared window of
// 1,000,000 tokens it compacts at 0.9 x 1,000,000 / 4 = 225,000 estimated
// tokens; its 128,000-token default would compact at 28,800. Cline writes the
// numbers it acted on to its own log, as a task.compaction_executed event, and
// the test reads the compaction point from there.
const (
	clineScale         = 4
	clineTriggerTokens = compactionWindow * 9 / 10 / clineScale
	clineSteps         = 130
	// clineStepTokens bounds how far one tool step can move the estimate, so
	// the compaction cannot land more than one step past the trigger.
	clineStepTokens = 4000
)

func proveClineCompaction(t *testing.T, c compactionCase, harnessDir string) {
	var steps []stubserver.ToolStep
	for i := 0; i < clineSteps; i++ {
		// A different command each step: Cline stops after five identical calls.
		steps = append(steps, stubserver.ToolStep{Name: "run_commands", Args: fmt.Sprintf(`{"commands":["cat big.txt; echo %d"]}`, i)})
	}
	plan := stubserver.NewUsagePlan(compactionWindow).CompactionMarkers(c.markers...)
	s := c.newSession(t, harnessDir, plan, steps...)
	if err := os.WriteFile(filepath.Join(s.project, "big.txt"), []byte(strings.Repeat("filler line of text\n", 300)), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stderr := s.run(t, "", "--json", "Reply with a short greeting.")

	events := clineCompactions(t, s.home)
	if len(events) == 0 {
		t.Fatalf("Cline never compacted in %d requests (%s)\n--- stdout ---\n%s\n--- stderr ---\n%s", plan.Requests(), c.offset, tail(stdout, 8), tail(stderr, 20))
	}
	first := events[0]
	switch {
	case first.MaxInputTokens != compactionWindow/clineScale:
		t.Fatalf("Cline budgeted %d input tokens, want %d: a fourth of the declared %d window", first.MaxInputTokens, compactionWindow/clineScale, compactionWindow)
	case first.ThresholdRatio != 0.9 || first.TriggerTokens != clineTriggerTokens:
		t.Fatalf("Cline's trigger is %d at ratio %v, want %d at 0.9", first.TriggerTokens, first.ThresholdRatio, clineTriggerTokens)
	case first.TokensBefore < first.TriggerTokens:
		t.Fatalf("Cline compacted at %d estimated tokens, under its trigger %d", first.TokensBefore, first.TriggerTokens)
	case first.TokensBefore > first.TriggerTokens+clineStepTokens:
		t.Fatalf("Cline compacted at %d estimated tokens, more than a step past its trigger %d", first.TokensBefore, first.TriggerTokens)
	}
	if s.compactions() == 0 {
		t.Fatalf("Cline logged a compaction but sent no summary request")
	}
}

// clineCompaction is the part of Cline's task.compaction_executed log event the
// proof reads.
type clineCompaction struct {
	TokensBefore   int     `json:"tokensBefore"`
	TriggerTokens  int     `json:"triggerTokens"`
	MaxInputTokens int     `json:"maxInputTokens"`
	ThresholdRatio float64 `json:"thresholdRatio"`
}

// clineCompactions reads the compactions Cline recorded in its own log under
// home, in order.
func clineCompactions(t *testing.T, home string) []clineCompaction {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, ".cline", "data", "logs", "cline.log"))
	if os.IsNotExist(err) {
		// Cline never logged, which the caller reports with its output.
		return nil
	}
	if err != nil {
		t.Fatalf("read Cline's log: %v", err)
	}
	var out []clineCompaction
	for _, line := range strings.Split(string(raw), "\n") {
		var rec struct {
			Event      string          `json:"event"`
			Properties clineCompaction `json:"properties"`
		}
		if json.Unmarshal([]byte(line), &rec) == nil && rec.Event == "task.compaction_executed" {
			out = append(out, rec.Properties)
		}
	}
	return out
}
