package registry

import "testing"

func TestCodexIntegrationIsAutoInstallable(t *testing.T) {
	spec := integrationSpecsByName["codex"]
	if spec == nil || spec.Install.EnsureInstalled == nil {
		t.Fatal("the codex integration has no EnsureInstalled")
	}
}
