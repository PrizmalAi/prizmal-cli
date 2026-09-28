package main

import (
	"os"
	"strings"
	"testing"
)

// TestGoreleaserInjectsVersion pins the release-build half of the version
// story: .goreleaser.yaml's build must stamp the git tag into main.version,
// or release binaries would print the compile-time fallback instead of
// v0.1.0. goreleaser itself is not available here, so the yaml is asserted
// directly — the same way the README Usage block is pinned to registerFlags.
func TestGoreleaserInjectsVersion(t *testing.T) {
	raw, err := os.ReadFile(".goreleaser.yaml")
	if err != nil {
		t.Fatalf("read .goreleaser.yaml: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "ldflags:") {
		t.Fatal(".goreleaser.yaml has no ldflags block; release binaries would print the dev fallback")
	}
	if !strings.Contains(text, "-X main.version={{.Version}}") {
		t.Errorf(".goreleaser.yaml ldflags do not inject the tag into main.version:\n%s", text)
	}
}
