package stubserver

import (
	"os"
	"testing"
	"time"
)

// TestLifetime runs a stub with a growing usage plan for as long as the
// marker file exists, and writes the server URL to a file. It exists only for
// manual exploration; CI never reaches it because the marker file is absent.
func TestLifetime(t *testing.T) {
	if os.Getenv("STUB_LIFETIME_DIR") == "" {
		t.Skip("exploration only")
	}
	dir := os.Getenv("STUB_LIFETIME_DIR")
	plan := NewUsagePlan(900000)
	srv := NewServer(WithModels("stub-model"), WithUsagePlan(plan))
	_ = os.WriteFile(dir+"/url", []byte(srv.URL), 0o644)
	defer srv.Close()
	for {
		if _, err := os.Stat(dir + "/stop"); err == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}
