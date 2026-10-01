package device

import (
	"strings"
	"testing"
)

// A testing process must never open a browser. This is the guard that keeps an
// e2e test's accidental path into sign-in from popping a window on the
// operator's desktop and pointing it at the production consent page.
func TestBestEffortOpenBrowserRefusesInTestingProcess(t *testing.T) {
	t.Setenv(EnvTesting, "testing")
	err := BestEffortOpenBrowser("https://app.prizmal.ai/cli/authorize?pk=x&name=y")
	if err == nil {
		t.Fatal("a testing process opened a browser")
	}
	if !strings.Contains(err.Error(), "testing") {
		t.Fatalf("error = %q, want it to name the testing refusal", err)
	}
}

// TestingEnv must carry the testing marker and a non-routable consent origin.
func TestTestingEnvPinsNonRoutableAppURL(t *testing.T) {
	joined := strings.Join(TestingEnv(), "\n")
	if !strings.Contains(joined, EnvTesting+"=testing") {
		t.Errorf("TestingEnv does not set %s=testing: %v", EnvTesting, TestingEnv())
	}
	if !strings.Contains(joined, "PRIZMAL_APP_URL=http://127.0.0.1:0") {
		t.Errorf("TestingEnv does not pin the app URL: %v", TestingEnv())
	}
}
