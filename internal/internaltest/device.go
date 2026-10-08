package internaltest

import (
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
)

// WithDeviceMode turns device mode on for one test and restores the previous
// state, so a launch test cannot leak the mode into the next one.
func WithDeviceMode(t *testing.T) {
	t.Helper()
	previous := envconfig.DeviceMode()
	envconfig.SetDeviceMode(true)
	envconfig.SetDeviceToken("pz-d-dt-test-token")
	t.Cleanup(func() {
		envconfig.SetDeviceMode(previous)
		envconfig.SetDeviceToken("")
	})
}
