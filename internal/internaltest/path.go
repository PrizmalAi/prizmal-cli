package internaltest

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// WithEmptyPATH puts an empty directory on PATH for the test, so every lookup
// misses: the state of a machine that has none of the tools an installer
// needs. It returns that directory, for a test that wants a binary in it.
func WithEmptyPATH(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	return dir
}

// WriteFakeBinary puts an executable named name in dir, with the extension the
// platform resolves it by: a bare name on Unix, .exe on Windows, where
// exec.LookPath only finds a name a PATHEXT entry spells.
func WriteFakeBinary(t *testing.T, dir, name string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
