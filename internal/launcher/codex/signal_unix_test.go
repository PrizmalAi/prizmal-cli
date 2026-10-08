//go:build !windows

package codex

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

// A launch writes the model catalog to a temporary directory and removes it
// when Codex exits. A SIGTERM or SIGHUP to prizmal used to end the process
// before the removal ran, which left a directory behind on every killed
// session. The launch now passes the signal to Codex, waits for it to exit,
// and then cleans up.
func TestCodexLaunchRemovesItsCatalogDirOnTerminationSignals(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			skipWithoutShell(t)
			tmp := t.TempDir()
			t.Setenv("TMPDIR", tmp)
			started := filepath.Join(t.TempDir(), "started")
			bundle := filepath.Join(t.TempDir(), "bundle.json")
			if err := os.WriteFile(bundle, []byte(fakeCodexCatalog), 0o644); err != nil {
				t.Fatal(err)
			}
			// The fake answers the version and bundle probes at once and
			// then waits, as a running session does, until it is told to stop.
			fakeBin(t, map[string]string{"codex": `case "$1" in
--version) echo "codex-cli ` + codexMinVersion + `"; exit 0;;
debug) /bin/cat '` + bundle + `'; exit 0;;
esac
trap 'exit 0' TERM HUP
: > '` + started + `'
while :; do /bin/sleep 0.05; done`})

			done := make(chan error, 1)
			go func() { done <- (&Codex{}).Run("smart", []launch.LaunchModel{{Name: "smart"}}, nil) }()

			waitFor(t, "the fake codex to start", func() bool {
				select {
				case err := <-done:
					t.Fatalf("the launch returned before codex started: %v", err)
				default:
				}
				_, err := os.Stat(started)
				return err == nil
			})
			if dirs, _ := filepath.Glob(filepath.Join(tmp, "prizmal-codex-*")); len(dirs) != 1 {
				t.Fatalf("catalog directories while codex runs = %v, want 1", dirs)
			}

			if err := syscall.Kill(os.Getpid(), sig); err != nil {
				t.Fatal(err)
			}
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("the launch did not return after the signal")
			}
			if dirs, _ := filepath.Glob(filepath.Join(tmp, "prizmal-codex-*")); len(dirs) != 0 {
				t.Fatalf("the launch left %v behind after %s", dirs, sig)
			}
		})
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
