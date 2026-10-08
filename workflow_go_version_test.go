package prizmalcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWorkflowsPinGoToGoMod fails when a workflow step installs a floating Go
// version. `go-version: stable` moves with each Go release, so CI would build
// with a different compiler than the toolchain line in go.mod names.
func TestWorkflowsPinGoToGoMod(t *testing.T) {
	files, err := filepath.Glob(".github/workflows/*.yml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no workflow files found: %v", err)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		text := string(raw)
		if strings.Contains(text, "actions/setup-go") && strings.Contains(text, "go-version: stable") {
			t.Errorf("%s installs a floating Go with `go-version: stable`; use `go-version-file: go.mod`", f)
		}
	}
}
