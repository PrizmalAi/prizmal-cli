package launch

import (
	"testing"
)

// TestDeviceModeSupport pins the device-mode surface: Claude Code refreshes a
// device token through its apiKeyHelper, and Pi through the extension its
// launch loads, whose provider resolves the key by running `prizmal auth
// token` per request. A runner that claimed support without a refresh
// contract would launch with an expired token after ten minutes.
func TestDeviceModeSupport(t *testing.T) {
	for _, spec := range ListAllIntegrationSpecs() {
		want := spec.Name == "claude" || spec.Name == "pi"
		if got := SupportsDeviceMode(spec.Runner); got != want {
			t.Errorf("%s SupportsDeviceMode = %v, want %v", spec.Name, got, want)
		}
	}
}
