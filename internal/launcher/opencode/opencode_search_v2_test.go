package opencode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// On a 2.x install, the launch writes no search tool file (2.x has no
// tools/ directory loader) and prints the one-line degraded notice the
// design calls for, naming OpenCode 2.x specifically. OPENCODE_CONFIG_DIR is
// still set, because the auth plugin lives in the same directory.
func TestOpenCodeV2LaunchDegradesSearchWithTheRightNotice(t *testing.T) {
	sandboxedHome(t)
	buf := captureNotices(t)
	o := &OpenCode{resolvedMajor: openCodeMajor2}

	env := o.envVars("wire-test-model", wireKeyTestModel())

	dir, ok := envLookup(env, "OPENCODE_CONFIG_DIR")
	if !ok || dir == "" {
		t.Fatal("2.x launch set no OPENCODE_CONFIG_DIR; the auth plugin would not load either")
	}
	if _, present := envLookup(env, openCodeSearchURLEnv); present {
		t.Errorf("2.x launch still set %s; the search tool it names does not exist", openCodeSearchURLEnv)
	}
	if _, err := os.Stat(filepath.Join(dir, "tool", openCodeSearchToolID+".ts")); !os.IsNotExist(err) {
		t.Fatalf("2.x launch left a search tool file behind (stat err: %v); 2.x never loads it", err)
	}
	notice := buf.String()
	if !containsAll(notice, "web search", "off on OpenCode 2.x") {
		t.Errorf("notice = %q, want it to name web search and OpenCode 2.x", notice)
	}
}

// A 1.x launch is unaffected by the 2.x degradation path: it still writes
// and uses the search tool as before.
func TestOpenCodeV1LaunchStillGetsSearch(t *testing.T) {
	sandboxedHome(t)
	o := &OpenCode{resolvedMajor: openCodeMajor1}

	env := o.envVars("wire-test-model", wireKeyTestModel())
	if _, present := envLookup(env, openCodeSearchURLEnv); !present {
		t.Error("1.x launch set no search URL env; the 2.x degradation path must not touch 1.x")
	}
}

// A previous 1.x launch's stale search tool file does not survive a later
// 2.x launch on the same cache directory.
func TestOpenCodeV2LaunchRemovesAStale1xSearchTool(t *testing.T) {
	sandboxedHome(t)

	v1 := &OpenCode{resolvedMajor: openCodeMajor1}
	v1.envVars("wire-test-model", wireKeyTestModel())
	dir, err := openCodeSearchDir()
	if err != nil {
		t.Fatalf("openCodeSearchDir: %v", err)
	}
	stale := filepath.Join(dir, "tool", openCodeSearchToolID+".ts")
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("1.x launch did not leave the search tool it was supposed to: %v", err)
	}

	v2 := &OpenCode{resolvedMajor: openCodeMajor2}
	v2.envVars("wire-test-model", wireKeyTestModel())
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale 1.x search tool survived a 2.x launch (stat err: %v)", err)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
