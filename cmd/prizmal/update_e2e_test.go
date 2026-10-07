//go:build !windows

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/stubserver"
)

// updateRig is a real prizmal at a pinned version, a stub Switch, a stub
// release host that counts its requests, and a stand-in claude that leaves a
// marker when the launch reaches it.
type updateRig struct {
	t         *testing.T
	bin       string
	home      string
	switchURL string
	releases  *httptest.Server
	hits      atomic.Int32
	marker    string
}

// newUpdateRig builds prizmal as release 0.1.2. latestStatus and latestTag set
// what the release host answers.
func newUpdateRig(t *testing.T, latestStatus int, latestTag string) *updateRig {
	t.Helper()
	if testing.Short() {
		t.Skip("subprocess test in short mode")
	}
	r := &updateRig{t: t, home: t.TempDir()}
	r.bin = filepath.Join(t.TempDir(), "prizmal")
	build := exec.Command("go", "build", "-ldflags", "-X main.version=0.1.2", "-o", r.bin, ".")
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	sw := stubserver.NewServer(stubserver.WithModels("smart"))
	t.Cleanup(sw.Close)
	r.switchURL = sw.URL

	r.releases = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.hits.Add(1)
		w.WriteHeader(latestStatus)
		_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": latestTag})
	}))
	t.Cleanup(r.releases.Close)

	binDir := filepath.Join(r.home, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	r.marker = filepath.Join(r.home, "claude-ran")
	stand := "#!/bin/sh\ntouch '" + r.marker + "'\nexit 0\n"
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte(stand), 0o755); err != nil {
		t.Fatal(err)
	}
	r.writeConfig(map[string]any{})
	return r
}

func (r *updateRig) writeConfig(extra map[string]any) {
	r.t.Helper()
	cfg := map[string]any{"version": 1, "base_url": r.switchURL, "api_key": stubserver.StubKey}
	for k, v := range extra {
		cfg[k] = v
	}
	data, _ := json.Marshal(cfg)
	if err := os.MkdirAll(filepath.Join(r.home, ".prizmal"), 0o700); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.home, ".prizmal", "config.json"), data, 0o600); err != nil {
		r.t.Fatal(err)
	}
}

// run starts prizmal with stdin closed, which makes the run non-interactive.
func (r *updateRig) run(extraEnv []string, args ...string) (int, string, string) {
	r.t.Helper()
	cmd := exec.Command(r.bin, args...)
	cmd.Env = append(prizmalChildEnv(r.home, filepath.Join(r.home, "bin")+":/usr/bin:/bin"),
		"PRIZMAL_TEST_INSTALL=archive", "PRIZMAL_TEST_UPDATE_URL="+r.releases.URL)
	cmd.Env = append(cmd.Env, extraEnv...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			r.t.Fatalf("run: %v", err)
		}
		code = ee.ExitCode()
	}
	return code, stdout.String(), stderr.String()
}

func (r *updateRig) launched() bool {
	_, err := os.Stat(r.marker)
	return err == nil
}

const warning = "prizmal v0.2.0 is available. You have v0.1.2."

func TestLaunchWarnsOnANewerReleaseAndStillLaunches(t *testing.T) {
	r := newUpdateRig(t, 200, "v0.2.0")
	code, _, stderr := r.run(nil, "-m", "smart", "claude")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	if !strings.Contains(stderr, warning) || !strings.Contains(stderr, "releases/latest") {
		t.Errorf("stderr lacks the warning and the release link:\n%s", stderr)
	}
	if !r.launched() {
		t.Error("the warning stopped the launch")
	}
}

func TestLaunchChecksOncePerDay(t *testing.T) {
	r := newUpdateRig(t, 200, "v0.2.0")
	r.run(nil, "-m", "smart", "claude")
	_, _, second := r.run(nil, "-m", "smart", "claude")
	if got := r.hits.Load(); got != 1 {
		t.Errorf("release host got %d requests over two launches, want 1", got)
	}
	if !strings.Contains(second, warning) {
		t.Errorf("the second run lost the warning (non-interactive runs warn from the cached answer):\n%s", second)
	}
}

func TestLaunchSaysNothingWhenCurrent(t *testing.T) {
	r := newUpdateRig(t, 200, "v0.1.2")
	code, _, stderr := r.run(nil, "-m", "smart", "claude")
	if code != 0 || strings.Contains(stderr, "available") {
		t.Fatalf("exit %d, stderr:\n%s", code, stderr)
	}
}

func TestLaunchSurvivesAReleaseHostThatFails(t *testing.T) {
	for name, status := range map[string]int{"500": 500, "403 rate limit": 403, "404": 404} {
		r := newUpdateRig(t, status, "")
		code, _, stderr := r.run(nil, "-m", "smart", "claude")
		if code != 0 || !r.launched() {
			t.Errorf("%s: exit %d, launched %v\n%s", name, code, r.launched(), stderr)
		}
		if strings.Contains(stderr, "available") {
			t.Errorf("%s: warned without an answer:\n%s", name, stderr)
		}
	}
}

func TestLaunchSurvivesAnUnreachableReleaseHost(t *testing.T) {
	r := newUpdateRig(t, 200, "v0.2.0")
	r.releases.Close()
	code, _, stderr := r.run(nil, "-m", "smart", "claude")
	if code != 0 || !r.launched() {
		t.Fatalf("exit %d, launched %v\n%s", code, r.launched(), stderr)
	}
}

func TestEnvironmentTurnsTheCheckOff(t *testing.T) {
	r := newUpdateRig(t, 200, "v0.2.0")
	code, _, stderr := r.run([]string{"PRIZMAL_NO_UPDATE_CHECK=1"}, "-m", "smart", "claude")
	if code != 0 || strings.Contains(stderr, "available") {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	if r.hits.Load() != 0 {
		t.Errorf("the release host got %d requests with the check off", r.hits.Load())
	}
}

func TestConfigTurnsTheCheckOff(t *testing.T) {
	r := newUpdateRig(t, 200, "v0.2.0")
	r.writeConfig(map[string]any{"check_updates": false})
	code, _, stderr := r.run(nil, "-m", "smart", "claude")
	if code != 0 || strings.Contains(stderr, "available") {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	if r.hits.Load() != 0 {
		t.Errorf("the release host got %d requests with check_updates false", r.hits.Load())
	}
	if _, err := os.Stat(filepath.Join(r.home, ".prizmal", "update-check.json")); err == nil {
		t.Error("the check wrote its state file while turned off")
	}
}

// Everything that is not a launch stays silent and offline: --list and a bare
// prizmal print for a person or a pipe, and the device subcommands feed another
// program whose stdout must hold only what it asked for.
func TestOnlyALaunchChecksForUpdates(t *testing.T) {
	cases := map[string][]string{
		"bare":          nil,
		"--list":        {"--list"},
		"--list claude": {"--list", "claude"},
		"auth token":    {"auth", "token"},
		"--restore":     {"--restore", "claude"},
		"--persist":     {"--model", "smart", "--persist", "claude"},
		"--help":        {"--help"},
		"--version":     {"--version"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			r := newUpdateRig(t, 200, "v0.2.0")
			_, stdout, stderr := r.run(nil, args...)
			if r.hits.Load() != 0 {
				t.Errorf("the release host got %d requests", r.hits.Load())
			}
			if strings.Contains(stdout+stderr, "available. You have") {
				t.Errorf("printed an update notice:\nstdout: %s\nstderr: %s", stdout, stderr)
			}
		})
	}
}

func TestYesNeverUpgradesUnattended(t *testing.T) {
	r := newUpdateRig(t, 200, "v0.2.0")
	code, _, stderr := r.run([]string{"PRIZMAL_TEST_INSTALL=go-install"}, "--yes", "-m", "smart", "claude")
	if code != 0 || !r.launched() {
		t.Fatalf("exit %d, launched %v\n%s", code, r.launched(), stderr)
	}
	if !strings.Contains(stderr, "Upgrade with: go install") || strings.Contains(stderr, "Upgraded prizmal") {
		t.Errorf("--yes should only warn:\n%s", stderr)
	}
}

func TestTestingPoseIsInertOutsideTestingMode(t *testing.T) {
	r := newUpdateRig(t, 200, "v0.2.0")
	// Without PRIZMAL_ENV=testing the pose and the release override are both
	// ignored, so the binary checks GitHub's real hosts as an unclassified
	// build and never reaches the stub.
	cmd := exec.Command(r.bin, "-m", "smart", "claude")
	cmd.Env = []string{"HOME=" + r.home, "PATH=" + filepath.Join(r.home, "bin") + ":/usr/bin:/bin",
		"PRIZMAL_TEST_INSTALL=homebrew", "PRIZMAL_TEST_UPDATE_URL=" + r.releases.URL, "PRIZMAL_NO_UPDATE_CHECK="}
	_ = cmd.Run()
	if r.hits.Load() != 0 {
		t.Errorf("the testing override redirected a non-testing process: %d requests", r.hits.Load())
	}
}
