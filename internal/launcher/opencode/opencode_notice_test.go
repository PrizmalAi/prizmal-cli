package opencode

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

func TestOpenCodeDeviceAuthPluginFailureNamesDeviceSignIn(t *testing.T) {
	home := sandboxedHome(t)
	launchtest.WithDeviceMode(t)
	out := captureNotices(t)
	// A regular file where the plugin directory belongs fails only the plugin.
	dir := filepath.Join(home, ".cache", "prizmal", "opencode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	env := (&OpenCode{}).envVars("wire-test-model", wireKeyTestModel())

	if _, ok := envLookup(env, "OPENCODE_CONFIG_CONTENT"); !ok {
		t.Error("OPENCODE_CONFIG_CONTENT dropped")
	}
	var device []string
	for _, line := range noticeLines(out) {
		if strings.HasPrefix(line, "prizmal: device sign-in") {
			device = append(device, line)
		}
	}
	if len(device) != 1 || !strings.HasPrefix(device[0], "prizmal: device sign-in is off on this launch: cannot write ") {
		t.Fatalf("device notices = %q, all output %q", device, out.String())
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
