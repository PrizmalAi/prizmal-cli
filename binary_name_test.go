package prizmalcli

import (
	"os/exec"
	"strings"
	"testing"
)

// TestInstallPathNamesTheBinaryPrizmal pins the layout that makes
// `go install github.com/PrizmalAi/prizmal-cli/cmd/prizmal@latest` install a
// binary called prizmal: go names a binary after the last element of its
// package path, so the main package must live in cmd/prizmal and the module
// root must not be a main package.
func TestInstallPathNamesTheBinaryPrizmal(t *testing.T) {
	out, err := exec.Command("go", "list", "-f", "{{.ImportPath}} {{.Name}}", "github.com/PrizmalAi/prizmal-cli/cmd/prizmal").CombinedOutput()
	if err != nil {
		t.Fatalf("go list cmd/prizmal: %v: %s", err, out)
	}
	if got, want := strings.TrimSpace(string(out)), "github.com/PrizmalAi/prizmal-cli/cmd/prizmal main"; got != want {
		t.Errorf("go list = %q, want %q", got, want)
	}

	// A main package at the root would install as prizmal-cli again.
	out, err = exec.Command("go", "list", "-f", "{{.Name}}", "github.com/PrizmalAi/prizmal-cli").CombinedOutput()
	if err != nil {
		t.Fatalf("go list module root: %v: %s", err, out)
	}
	if strings.TrimSpace(string(out)) == "main" {
		t.Error("the module root is a main package; go install of the module path would name the binary prizmal-cli")
	}
}
