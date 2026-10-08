package registry

import (
	"testing"

	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

// TestDeviceModeSupport pins the device-mode surface: Claude Code refreshes a
// device token through its apiKeyHelper, Pi through the extension its launch
// loads whose provider runs `prizmal auth token` per request, and Codex through
// its command-backed provider auth. No other harness has a documented refresh
// contract, and a runner that claimed support without one would launch with an
// expired token after ten minutes.
func TestDeviceModeSupport(t *testing.T) {
	for _, spec := range ListAllIntegrationSpecs() {
		want := spec.Name == "claude" || spec.Name == "pi" || spec.Name == "codex"
		if got := launch.SupportsDeviceMode(spec.Runner); got != want {
			t.Errorf("%s SupportsDeviceMode = %v, want %v", spec.Name, got, want)
		}
	}
}
