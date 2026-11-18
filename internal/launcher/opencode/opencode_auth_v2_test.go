package opencode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/launcher/launchtest"
)

// On OpenCode 2.x, a device launch writes the Plugin.define shape, not the
// 1.x chat.headers shape: 2.x has no chat.headers hook, so the 1.x plugin
// would not load at all.
func TestOpenCodeDeviceLaunchOnV2WritesTheEffectPlugin(t *testing.T) {
	home := sandboxedHome(t)
	launchtest.WithDeviceMode(t)

	if err := prepareAuthPlugin(openCodeMajor2); err != nil {
		t.Fatalf("prepareAuthPlugin: %v", err)
	}
	dir, err := openCodeSearchDir()
	if err != nil {
		t.Fatalf("openCodeSearchDir: %v", err)
	}
	if !strings.HasPrefix(dir, home) {
		t.Fatalf("config dir %q is outside the sandboxed home", dir)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "plugin", openCodeAuthPluginFile))
	if err != nil {
		t.Fatalf("v2 auth plugin missing: %v", err)
	}
	source := string(raw)
	for _, want := range []string{
		"Plugin.define(",
		`"model.request"`,
		"auth token",
		`e.headers["Authorization"]`,
		"Bearer ",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("v2 auth plugin lacks %q", want)
		}
	}
	for _, mustNotHave := range []string{"chat.headers", "server:"} {
		if strings.Contains(source, mustNotHave) {
			t.Errorf("v2 auth plugin still carries the 1.x shape %q", mustNotHave)
		}
	}
}

// Unknown or unparseable versions fail open to the 1.x plugin shape, matching
// resolveOpenCodeMajor's own rule.
func TestOpenCodeDeviceLaunchOnUnknownMajorWritesTheV1Plugin(t *testing.T) {
	sandboxedHome(t)
	launchtest.WithDeviceMode(t)

	if err := prepareAuthPlugin(openCodeMajorUnknown); err != nil {
		t.Fatalf("prepareAuthPlugin: %v", err)
	}
	dir, err := openCodeSearchDir()
	if err != nil {
		t.Fatalf("openCodeSearchDir: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "plugin", openCodeAuthPluginFile))
	if err != nil {
		t.Fatalf("auth plugin missing: %v", err)
	}
	if !strings.Contains(string(raw), `"chat.headers"`) {
		t.Error("unknown major did not fall back to the 1.x plugin shape")
	}
}

// Moving from a 2.x launch to a 1.x launch (or back) must not leave the other
// major's plugin shape under the same filename: prepareAuthPlugin rewrites it
// every launch.
func TestOpenCodeAuthPluginSwapsShapeAcrossMajors(t *testing.T) {
	sandboxedHome(t)
	launchtest.WithDeviceMode(t)

	if err := prepareAuthPlugin(openCodeMajor2); err != nil {
		t.Fatalf("prepareAuthPlugin(v2): %v", err)
	}
	if err := prepareAuthPlugin(openCodeMajor1); err != nil {
		t.Fatalf("prepareAuthPlugin(v1): %v", err)
	}
	dir, _ := openCodeSearchDir()
	raw, err := os.ReadFile(filepath.Join(dir, "plugin", openCodeAuthPluginFile))
	if err != nil {
		t.Fatalf("auth plugin missing: %v", err)
	}
	source := string(raw)
	if strings.Contains(source, "Plugin.define(") {
		t.Error("a later 1.x launch left the 2.x plugin shape in place")
	}
	if !strings.Contains(source, `"chat.headers"`) {
		t.Error("a later 1.x launch did not write the 1.x plugin shape")
	}
}
