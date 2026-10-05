package launch

import (
	"testing"
)

// TestOnlyClaudeSupportsDeviceMode pins the device-mode surface: Claude Code
// refreshes a device token through its apiKeyHelper, and no other harness has a
// documented refresh contract. A runner that claimed support without one would
// launch with an expired token after ten minutes.
func TestOnlyClaudeSupportsDeviceMode(t *testing.T) {
	for _, spec := range ListAllIntegrationSpecs() {
		want := spec.Name == "claude"
		if got := SupportsDeviceMode(spec.Runner); got != want {
			t.Errorf("%s SupportsDeviceMode = %v, want %v", spec.Name, got, want)
		}
	}
}
