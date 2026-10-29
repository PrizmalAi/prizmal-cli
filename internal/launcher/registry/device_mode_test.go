package registry

import (
	"github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"testing"
)

// TestDeviceModeSupport pins the device-mode surface: Claude Code refreshes a
// device token through its apiKeyHelper, Pi through the extension its launch
// loads whose provider runs `prizmal auth token` per request, Codex through
// its command-backed provider auth, OpenCode through the plugin its launch
// loads whose chat.headers hook runs the same helper before each request, and
// Cline and Kilo through the loopback relay their launches start.
func TestDeviceModeSupport(t *testing.T) {
	for _, spec := range ListAllIntegrationSpecs() {
		want := spec.Name == "claude" || spec.Name == "pi" || spec.Name == "codex" ||
			spec.Name == "opencode" || spec.Name == "cline" || spec.Name == "kilo"
		if got := launch.SupportsDeviceMode(spec.Runner); got != want {
			t.Errorf("%s SupportsDeviceMode = %v, want %v", spec.Name, got, want)
		}
	}
}
