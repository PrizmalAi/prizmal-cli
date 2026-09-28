package launch

import (
	"os"
	"strings"
	"testing"
)

// readmeWithoutCLIHeading is the README section that shows how to start Claude
// Code against the Switch without prizmal.
const readmeWithoutCLIHeading = "## Claude Code without prizmal"

// TestREADMEDocumentsClaudeLaunchWithoutCLI checks that the README section for
// a launch without prizmal sets every variable a prizmal launch sets, so a
// change to envVars fails here until the README matches it.
func TestREADMEDocumentsClaudeLaunchWithoutCLI(t *testing.T) {
	data, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	// A Windows checkout can convert the README to CRLF line endings.
	readme := strings.ReplaceAll(string(data), "\r\n", "\n")
	_, section, ok := strings.Cut(readme, readmeWithoutCLIHeading+"\n")
	if !ok {
		t.Fatalf("README has no %q section", readmeWithoutCLIHeading)
	}
	if next := strings.Index(section, "\n## "); next >= 0 {
		section = section[:next]
	}

	for _, kv := range (&Claude{}).envVars() {
		name, _, _ := strings.Cut(kv, "=")
		if !strings.Contains(section, name+"=") {
			t.Errorf("README section %q does not set %s", readmeWithoutCLIHeading, name)
		}
	}
}
