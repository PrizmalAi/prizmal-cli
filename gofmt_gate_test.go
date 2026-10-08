package prizmalcli

import (
	"os"
	"strings"
	"testing"
)

// TestCIChecksGofmt fails when the CI workflow stops running `gofmt -l .` or
// adds `-s`. Local gofmt and CI must agree, so the gate uses plain gofmt.
func TestCIChecksGofmt(t *testing.T) {
	raw, err := os.ReadFile(".github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("read ci.yml: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "gofmt -l .") {
		t.Error("ci.yml does not run `gofmt -l .`; unformatted Go files would pass CI")
	}
	if strings.Contains(text, "gofmt -s") || strings.Contains(text, "gofmt -l -s") {
		t.Error("ci.yml runs gofmt with -s; use plain `gofmt -l .` so CI matches local gofmt")
	}
}
