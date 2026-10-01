package launch

import (
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
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

// TestDeviceModeRefusalNamesAKey pins the refusal's fix: a switch key, which
// has no expiry, is the one thing that unblocks a harness with no refresh
// contract.
func TestDeviceModeRefusalNamesAKey(t *testing.T) {
	err := DeviceModeRefusal("Codex")
	msg := err.Error()
	if !strings.Contains(msg, "Codex") {
		t.Errorf("refusal does not name the harness: %q", msg)
	}
	if !strings.Contains(msg, "--api-key") || !strings.Contains(msg, envconfig.KeyEnvVar) {
		t.Errorf("refusal does not name the switch-key sources: %q", msg)
	}
}
