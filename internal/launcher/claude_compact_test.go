package launch

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
)

// Claude Code 2.1.292's window resolver (wE) reads the inline settings JSON
// before its own defaults: an autoCompactWindow present there — at the top
// level, or per model under modelSettings.<canonical name> — resolves the
// window with the source "settings", and compaction turns on. Without one of
// these, a model id the binary does not recognize resolves with the source
// "auto", and the threshold check returns early before it ever compacts: the
// launched session waits for the endpoint's overflow error, which the Switch
// does not serve in the shape Claude Code recognizes. The setting values and
// their key spellings here are the contract with that resolver.

// The launch states 90% of the 1M window: 882000 tokens, the value the
// prizmal Compact contract fixes for a 1M-context model.
func TestClaudeCompactModelSettingsValue(t *testing.T) {
	if got := claudeCompactModelSettings(); got != 900000 {
		t.Fatalf("claudeCompactModelSettings() = %d, want 900000", got)
	}
	if claudeCompactFraction != 90 {
		t.Fatalf("claudeCompactFraction = %d, want the documented 90", claudeCompactFraction)
	}
	if claudeCompactWindow != 1_000_000 {
		t.Fatalf("claudeCompactWindow = %d, want the Switch window 1000000", claudeCompactWindow)
	}
}

// The settings key is the canonical model spelling: the [1m] suffix stripped,
// lowercased. Claude Code canonicalizes both sides of the lookup the same
// way, so the suffixed and bare spellings of one name read the same entry.
func TestClaudeCompactKeyStripsTheSuffixAndLowercases(t *testing.T) {
	for model, want := range map[string]string{
		"smart[1m]":        "smart",
		"SMART[1M]":        "smart",
		"prizmal/stub":     "prizmal/stub",
		"prizmal/stub[1m]": "prizmal/stub",
		"claude-tier-opus": "claude-tier-opus",
	} {
		if got := claudeCompactKey(model); got != want {
			t.Errorf("claudeCompactKey(%q) = %q, want %q", model, got, want)
		}
	}
}

// The inline settings JSON carries the window per model, keyed by the bare
// name, beside the model field that spells the same name with its [1m].
// Claude Code's aggregation reads modelSettings.<canonical>.autoCompactWindow
// into a byModel map and consults it before the top-level default, so the
// window follows a /model switch to any spelling of the same launchable set,
// which a process-wide environment variable cannot.
func TestClaudeSettingsJSONStatesTheCompactWindow(t *testing.T) {
	settings, err := claudeSettingsJSON("smart", nil)
	if err != nil {
		t.Fatalf("claudeSettingsJSON: %v", err)
	}
	var parsed struct {
		Model         string `json:"model"`
		ModelSettings map[string]struct {
			AutoCompactWindow int `json:"autoCompactWindow"`
		} `json:"modelSettings"`
	}
	if err := json.Unmarshal([]byte(settings), &parsed); err != nil {
		t.Fatalf("parse settings %s: %v", settings, err)
	}
	if parsed.Model != "smart[1m]" {
		t.Errorf("model = %q, want smart[1m]", parsed.Model)
	}
	entry, ok := parsed.ModelSettings["smart"]
	if !ok {
		t.Fatalf("settings = %s, want a modelSettings entry keyed by the bare name", settings)
	}
	if entry.AutoCompactWindow != claudeCompactModelSettings() {
		t.Errorf("autoCompactWindow = %d, want %d", entry.AutoCompactWindow, claudeCompactModelSettings())
	}
}

// A model with picker rows gets its window under every key a session can
// resolve to: the launched model's bare name, and each row's. The settings
// key is per model, so a /model switch to another row switches the window
// with the model.
func TestClaudeSettingsJSONKeysEveryLaunchableRow(t *testing.T) {
	rows := ModelRows([]LaunchModel{
		{Name: "smart", Tier: "opus"},
		{Name: "flash", Tier: "haiku"},
	})
	settings, err := claudeSettingsJSON("smart", rows)
	if err != nil {
		t.Fatalf("claudeSettingsJSON: %v", err)
	}
	var parsed struct {
		ModelSettings map[string]struct {
			AutoCompactWindow int `json:"autoCompactWindow"`
		} `json:"modelSettings"`
	}
	if err := json.Unmarshal([]byte(settings), &parsed); err != nil {
		t.Fatalf("parse settings %s: %v", settings, err)
	}
	for _, name := range []string{"smart", "flash"} {
		entry, ok := parsed.ModelSettings[name]
		if !ok {
			t.Errorf("settings = %s, want a modelSettings entry for %s", settings, name)
			continue
		}
		if entry.AutoCompactWindow != claudeCompactModelSettings() {
			t.Errorf("%s autoCompactWindow = %d, want %d", name, entry.AutoCompactWindow, claudeCompactModelSettings())
		}
	}
}

// The window is declared through the settings JSON the launch passes on its
// command line, and never through an environment variable that would outlive
// the launch's scope. Claude Code reads CLAUDE_CODE_AUTO_COMPACT_WINDOW ahead
// of the settings, so an inherited export would silently move the threshold
// the launch states; the child environment must not carry one, from any
// source.
func TestClaudeChildEnvStatesNoCompactWindowVar(t *testing.T) {
	resetAPIKey(t)
	for _, inherited := range []string{"500000", "600000"} {
		t.Setenv("CLAUDE_CODE_AUTO_COMPACT_WINDOW", inherited)

		env := strings.Join(claudeChildEnv("smart", nil), "\n")

		if strings.Contains(env, "CLAUDE_CODE_AUTO_COMPACT_WINDOW=") {
			t.Fatalf("the child env carries CLAUDE_CODE_AUTO_COMPACT_WINDOW; the launch states the window in its settings JSON:\n%s", env)
		}
	}
}

// An autoCompactWindow the operator exports in their own shell environment
// must not override the launch's settings. The env var outranks the settings
// in Claude Code's resolver, so the launcher would otherwise hand a session
// a threshold an operator's Anthropic-shell export chose.
func TestClaudeSettingsJSONWinsOverAnInheritedCompactWindowVar(t *testing.T) {
	resetAPIKey(t)
	t.Setenv("CLAUDE_CODE_AUTO_COMPACT_WINDOW", "200000")

	// The channel check above pins that the child env drops the variable;
	// this test pins that the settings still state the launch's value when
	// the inherited one is present.
	settings, err := claudeSettingsJSON("smart", nil)
	if err != nil {
		t.Fatalf("claudeSettingsJSON: %v", err)
	}
	if !strings.Contains(settings, `"autoCompactWindow":900000`) {
		t.Errorf("settings = %s, want the launch's 900000 with an inherited env override in place", settings)
	}
	if strings.Contains(settings, "200000") {
		t.Errorf("settings = %s, want no trace of the inherited %q", settings, "200000")
	}
}