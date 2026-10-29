package registry

import "testing"

// TestAdapterInstallersAreWiredToTheRegistry covers the registry declaring an
// install hook for every harness that has one, so a launch that goes through
// the CLI's ensure gate and a launch that goes through the runner take the same
// path rather than two that can drift.
//
// Only the install is checked here. Whether a harness is installed is not the
// registry's question any more: the runner answers it through Installed, and
// TestEveryRunnerAnswersInstalled pins that every adapter can.
func TestAdapterInstallersAreWiredToTheRegistry(t *testing.T) {
	for _, name := range []string{"claude", "cline", "codex", "kilo", "opencode", "pi"} {
		spec, err := LookupIntegrationSpec(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if spec.Install.EnsureInstalled == nil {
			t.Errorf("%s does not declare an install", name)
		}
	}
}

// TestCodexIntegrationIsAutoInstallable keeps Codex on the install path the
// other harnesses take: the registry entry carries the hook, so the picker
// offers the install and the launch runs it rather than printing a URL.
func TestCodexIntegrationIsAutoInstallable(t *testing.T) {
	spec, err := LookupIntegrationSpec("codex")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Install.EnsureInstalled == nil {
		t.Fatal("the codex integration has no EnsureInstalled")
	}
}
