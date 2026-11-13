package selfpath

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func stubArgv0(t *testing.T, value string) {
	t.Helper()
	previous := argv0
	argv0 = func() string { return value }
	t.Cleanup(func() { argv0 = previous })
}

func stubLookPath(t *testing.T, fn func(string) (string, error)) {
	t.Helper()
	previous := lookPath
	lookPath = fn
	t.Cleanup(func() { lookPath = previous })
}

func stubExecutable(t *testing.T, fn func() (string, error)) {
	t.Helper()
	previous := executable
	executable = fn
	t.Cleanup(func() { executable = previous })
}

// A typed absolute path is what the operator launched, so it is recorded
// verbatim even when PATH would resolve a different prizmal.
func TestPathPrefersTypedAbsoluteOverPATH(t *testing.T) {
	typed := filepath.Join(t.TempDir(), "bin", "prizmal")
	stubArgv0(t, typed)
	stubLookPath(t, func(string) (string, error) { return "/usr/bin/prizmal", nil })

	got, err := Path()
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if got != typed {
		t.Errorf("Path = %q, want the typed path %q", got, typed)
	}
}

// A typed relative path is made absolute against the working directory, so
// the recorded command still runs after the harness changes its own.
func TestPathAbsolutizesATypedRelativePath(t *testing.T) {
	stubArgv0(t, filepath.Join("bin", "prizmal"))
	stubLookPath(t, func(string) (string, error) { return "", errors.New("not on PATH") })
	stubExecutable(t, func() (string, error) { return "", errors.New("no executable") })

	got, err := Path()
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	want, err := filepath.Abs(filepath.Join("bin", "prizmal"))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}

// A bare name is a PATH launch: LookPath answers with the matched symlink
// itself, and that stable path is what gets recorded rather than the
// symlink's target.
func TestPathReturnsThePATHSymlinkNotItsTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Windows symlinks need a privilege a test machine may not hold, and
		// exec.LookPath appends PATHEXT, so a bare "prizmal" link is not the
		// shape it matches. The POSIX legs cover this resolution.
		t.Skip("symlink resolution is POSIX-shaped; covered on darwin and linux")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "Caskroom", "prizmal", "0.3.2", "prizmal")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "bin", "prizmal")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	stubArgv0(t, "prizmal")
	t.Setenv("PATH", filepath.Dir(link))
	stubExecutable(t, func() (string, error) { return target, nil })

	got, err := Path()
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if got != link {
		t.Errorf("Path = %q, want the stable symlink %q (not its target %q)", got, link, target)
	}
}

// With no prizmal on PATH and no typed path, the resolved executable is all
// that is left.
func TestPathFallsBackToTheExecutable(t *testing.T) {
	stubArgv0(t, "prizmal")
	t.Setenv("PATH", t.TempDir())
	stubExecutable(t, func() (string, error) { return "/resolved/prizmal", nil })

	got, err := Path()
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if got != "/resolved/prizmal" {
		t.Errorf("Path = %q, want /resolved/prizmal", got)
	}
}

// The fallback's own failure is the caller's error: recording nothing would
// write a credential command that cannot run.
func TestPathReportsAnExecutableFailure(t *testing.T) {
	stubArgv0(t, "prizmal")
	t.Setenv("PATH", t.TempDir())
	stubExecutable(t, func() (string, error) { return "", errors.New("no executable") })

	if _, err := Path(); err == nil {
		t.Fatal("Path succeeded with no typed path, no PATH match and no executable")
	}
}
