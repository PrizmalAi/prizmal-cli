package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// runPrizmalWithEnvKey runs prizmal against a temp HOME with
// $PRIZMAL_SWITCH_KEY exported, the case the environment variable exists for.
func runPrizmalWithEnvKey(t *testing.T, prizmalBin, home, envKey string, args ...string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(prizmalBin, args...)
	cmd.Env = append(prizmalChildEnv(home, os.Getenv("PATH")), "PRIZMAL_SWITCH_KEY="+envKey)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	exitCode := 0
	if err := cmd.Run(); err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("failed to run prizmal: %v", err)
		}
		exitCode = exitErr.ExitCode()
	}
	return exitCode, stdout.String(), stderr.String()
}

// An exported $PRIZMAL_SWITCH_KEY outranks the config file's api_key, the way
// $PRIZMAL_SWITCH_URL outranks its base_url. The key the Switch receives is
// the exported one.
func TestEnvKeyOutranksConfigFileKey(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess test in short mode")
	}
	prizmalBin := buildPrizmalTestBinary(t)

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(deployedCatalog))
	}))
	t.Cleanup(srv.Close)

	home := t.TempDir()
	writePrizmalConfig(t, home, "sk-from-config-file")

	exitCode, stdout, stderr := runPrizmalWithEnvKey(t, prizmalBin, home, "sk-from-env", "--list", "--url", srv.URL)
	if exitCode != 0 {
		t.Fatalf("prizmal --list exited %d\nstdout: %s\nstderr: %s", exitCode, stdout, stderr)
	}
	if gotAuth != "Bearer sk-from-env" {
		t.Errorf("the Switch did not receive the exported key; the config file's key won")
	}
}

// A refused discovery call names the key source and the status on one stderr
// line, so the operator can tell a wrong key from a missing feature.
func TestDiscoveryFailureNamesKeySourceAndStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess test in short mode")
	}
	prizmalBin := buildPrizmalTestBinary(t)

	srv := tenantCatalogServer(t, `{"error":{"message":"invalid api key"}}`, http.StatusUnauthorized)
	home := t.TempDir()
	writePrizmalConfig(t, home, "sk-from-config-file")

	exitCode, _, stderr := runPrizmalWithEnvKey(t, prizmalBin, home, "sk-from-env", "--list", "--url", srv.URL)
	if exitCode == 0 {
		t.Fatalf("prizmal --list succeeded against a 401")
	}
	for _, want := range []string{"api key from $PRIZMAL_SWITCH_KEY", "401"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr, want)
		}
	}
	for _, secret := range []string{"sk-from-env", "sk-from-config-file"} {
		if strings.Contains(stderr, secret) {
			t.Errorf("stderr leaked a key value")
		}
	}
}
