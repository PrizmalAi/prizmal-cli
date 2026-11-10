package opencode

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/launchtest"
)

// captureNotices redirects the launch notices into a buffer for one test.
func captureNotices(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := noticeOut
	noticeOut = &buf
	t.Cleanup(func() { noticeOut = previous })
	return &buf
}

// blockSearchDir occupies ~/.cache with a regular file, so the search directory
// cannot be created whatever the permissions of the account running the test.
func blockSearchDir(t *testing.T) string {
	t.Helper()
	home := sandboxedHome(t)
	if err := os.WriteFile(filepath.Join(home, ".cache"), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

func noticeLines(buf *bytes.Buffer) []string {
	text := strings.TrimSpace(buf.String())
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func TestOpenCodeUnwritableSearchDirKeepsInvariantEnv(t *testing.T) {
	blockSearchDir(t)
	captureNotices(t)

	env := (&OpenCode{}).childEnv("wire-test-model", wireKeyTestModel())

	if content, ok := envLookup(env, "OPENCODE_CONFIG_CONTENT"); !ok || content == "" {
		t.Fatal("a launch that cannot write the search directory dropped OPENCODE_CONFIG_CONTENT")
	}
	if _, ok := envLookup(env, "OPENCODE_CONFIG_DIR"); ok {
		t.Error("OPENCODE_CONFIG_DIR is set although the search tool was not written")
	}
}

func TestOpenCodeUnwritableSearchDirNamesWebSearch(t *testing.T) {
	home := blockSearchDir(t)
	out := captureNotices(t)

	(&OpenCode{}).envVars("wire-test-model", wireKeyTestModel())

	lines := noticeLines(out)
	if len(lines) != 1 {
		t.Fatalf("got %d notices, want 1: %q", len(lines), out.String())
	}
	if !strings.HasPrefix(lines[0], "prizmal: web search is off on this launch: cannot write ") {
		t.Errorf("notice = %q", lines[0])
	}
	if !strings.Contains(lines[0], home) {
		t.Errorf("notice %q does not name the path under %s", lines[0], home)
	}
}

// A device-mode launch that cannot write the plugin fails in Run's
// prepareAuthPlugin with an error, so envVars has no device notice of its own.
func TestPrepareAuthPluginFailsLoudlyWhenDeviceWriteFails(t *testing.T) {
	home := sandboxedHome(t)
	launchtest.WithDeviceMode(t)
	dir := filepath.Join(home, ".cache", "prizmal", "opencode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A regular file where the plugin directory belongs fails only the plugin.
	if err := os.WriteFile(filepath.Join(dir, "plugin"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := prepareAuthPlugin()
	if err == nil || !strings.Contains(err.Error(), "write OpenCode's auth plugin") {
		t.Fatalf("prepareAuthPlugin = %v, want the write error", err)
	}
}

// prepareAuthPlugin owns the plugin's removal: a launch that is not device mode
// deletes the plugin an earlier device launch left behind.
func TestPrepareAuthPluginRemovesStalePluginOutsideDeviceMode(t *testing.T) {
	home := sandboxedHome(t)
	pluginPath := filepath.Join(home, ".cache", "prizmal", "opencode", "plugin", "prizmal_auth.ts")

	launchtest.WithDeviceMode(t)
	if err := prepareAuthPlugin(); err != nil {
		t.Fatalf("device prepareAuthPlugin: %v", err)
	}
	if _, err := os.Stat(pluginPath); err != nil {
		t.Fatalf("device launch left no plugin: %v", err)
	}

	envconfig.SetDeviceMode(false)
	envconfig.SetDeviceToken("")
	if err := prepareAuthPlugin(); err != nil {
		t.Fatalf("static-key prepareAuthPlugin: %v", err)
	}
	if _, err := os.Stat(pluginPath); !os.IsNotExist(err) {
		t.Fatalf("stale plugin survived a static-key launch (stat err: %v)", err)
	}
}

// envVars no longer touches the plugin.
func TestOpenCodeEnvVarsLeavesTheAuthPluginAlone(t *testing.T) {
	home := sandboxedHome(t)
	launchtest.WithDeviceMode(t)
	(&OpenCode{}).envVars("wire-test-model", wireKeyTestModel())
	if _, err := os.Stat(filepath.Join(home, ".cache", "prizmal", "opencode", "plugin")); !os.IsNotExist(err) {
		t.Fatalf("envVars wrote under the plugin directory (stat err: %v)", err)
	}
}

func TestOpenCodeNormalLaunchPrintsNoNotice(t *testing.T) {
	for name, device := range map[string]bool{"static key": false, "device": true} {
		t.Run(name, func(t *testing.T) {
			sandboxedHome(t)
			if device {
				launchtest.WithDeviceMode(t)
			}
			out := captureNotices(t)

			(&OpenCode{}).envVars("wire-test-model", wireKeyTestModel())

			if out.Len() != 0 {
				t.Fatalf("normal launch printed %q", out.String())
			}
		})
	}
}
