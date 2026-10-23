// Package homeguard keeps a package's tests away from the operator's real
// ~/.prizmal. Run points HOME and USERPROFILE at a fresh temp directory for the
// whole package run, so a test that forgets its own sandbox writes into the
// throwaway home. It also fingerprints the real ~/.prizmal before and after the
// run and fails the package when anything under it changed.
//
// It imports nothing from this module, so every package's tests can use it,
// envconfig's included.
package homeguard

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// stateDir is the directory under the home that the guard watches.
const stateDir = ".prizmal"

// Snapshot maps a path relative to the watched directory to a description of
// what is there: a file's size, modification time and content hash, a
// directory, or a symlink's target.
type Snapshot map[string]string

// Take fingerprints every entry under dir. A directory that does not exist
// yields an empty snapshot, so creating it later shows up as a change.
func Take(dir string) Snapshot {
	snap := Snapshot{}
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil || rel == "." {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, _ := os.Readlink(path)
			snap[rel] = "symlink -> " + target
		case d.IsDir():
			snap[rel] = "dir"
		default:
			snap[rel] = fmt.Sprintf("file size=%d mtime=%d sha256=%s",
				info.Size(), info.ModTime().UnixNano(), hashFile(path))
		}
		return nil
	})
	return snap
}

func hashFile(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return "unreadable"
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "unreadable"
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Diff lists, one line per path, what changed between two snapshots: added,
// removed or modified. The lines are sorted.
func Diff(before, after Snapshot) []string {
	var out []string
	for p, b := range before {
		a, ok := after[p]
		switch {
		case !ok:
			out = append(out, "removed:  "+p)
		case a != b:
			out = append(out, "modified: "+p)
		}
	}
	for p := range after {
		if _, ok := before[p]; !ok {
			out = append(out, "added:    "+p)
		}
	}
	sort.Strings(out)
	return out
}

// Run runs the package's tests with HOME and USERPROFILE pointing at a fresh
// temp directory, then compares the real ~/.prizmal with how it was before. It
// returns the exit code for os.Exit: m.Run's, or 1 when the real directory
// changed or the sandbox could not be set up.
//
// Another process that writes ~/.prizmal while the tests run, such as the
// operator using the CLI, trips the same check. The failure names each path so
// the cause is visible.
func Run(m *testing.M) int {
	realHome, err := os.UserHomeDir()
	watched := ""
	if err == nil && realHome != "" {
		watched = filepath.Join(realHome, stateDir)
	}
	var before Snapshot
	if watched != "" {
		before = Take(watched)
	}

	sandbox, err := os.MkdirTemp("", "prizmal-test-home-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "homeguard: create the temp home: %v\n", err)
		return 1
	}
	defer func() { _ = os.RemoveAll(sandbox) }()
	defer setEnv("USERPROFILE", sandbox)()
	defer setEnv("HOME", sandbox)()

	code := m.Run()

	if watched != "" {
		if changes := Diff(before, Take(watched)); len(changes) > 0 {
			fmt.Fprintf(os.Stderr, "homeguard: the tests changed the real %s:\n", watched)
			for _, c := range changes {
				fmt.Fprintf(os.Stderr, "  %s\n", c)
			}
			if code == 0 {
				code = 1
			}
		}
	}
	return code
}

// setEnv sets name and returns the function that puts the previous state back,
// including "was unset".
func setEnv(name, value string) func() {
	prev, had := os.LookupEnv(name)
	_ = os.Setenv(name, value)
	return func() {
		if had {
			_ = os.Setenv(name, prev)
		} else {
			_ = os.Unsetenv(name)
		}
	}
}
