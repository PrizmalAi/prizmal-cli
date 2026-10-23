package prizmalcli

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// TestInstallPathNamesTheBinaryPrizmal pins the layout that makes
// `go install github.com/PrizmalAi/prizmal-cli/cmd/prizmal@latest` install a
// binary called prizmal: go names a binary after the last element of its
// package path, so the main package must live in cmd/prizmal and the module
// root must not be a main package.
// goList returns the stdout of `go list`. Module download lines go to stderr
// on a cold cache, so stderr joins the output only in a failure message.
func goList(t *testing.T, format, pkg string) string {
	t.Helper()
	out, err := exec.Command("go", "list", "-f", format, pkg).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			t.Fatalf("go list %s: %v: %s", pkg, err, exitErr.Stderr)
		}
		t.Fatalf("go list %s: %v", pkg, err)
	}
	return strings.TrimSpace(string(out))
}

func TestInstallPathNamesTheBinaryPrizmal(t *testing.T) {
	got := goList(t, "{{.ImportPath}} {{.Name}}", "github.com/PrizmalAi/prizmal-cli/cmd/prizmal")
	if want := "github.com/PrizmalAi/prizmal-cli/cmd/prizmal main"; got != want {
		t.Errorf("go list = %q, want %q", got, want)
	}

	// A main package at the root would install as prizmal-cli again.
	if goList(t, "{{.Name}}", "github.com/PrizmalAi/prizmal-cli") == "main" {
		t.Error("the module root is a main package; go install of the module path would name the binary prizmal-cli")
	}
}
