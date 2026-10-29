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
	"strings"
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
// temp directory, then checks that nothing leaked into the real ~/.prizmal. It
// returns the exit code for os.Exit: m.Run's, or 1 when a leak was found or the
// sandbox could not be set up.
//
// A leak is either of these:
//   - HOME or USERPROFILE no longer names the sandbox when the tests end.
//   - A file in the real ~/.prizmal was created or changed during the run and
//     now holds the same bytes as the file at the same path in the sandbox's
//     ~/.prizmal.
//
// Other processes write the real directory while the tests run: the operator
// using the CLI refreshes token.json and writes update-check.json. Their bytes
// differ from what the tests wrote in the sandbox, so they do not fail the
// package.
//
// Known limit: a leak whose bytes differ from the sandbox copy, or that
// touches a path the tests never wrote in the sandbox, is not attributed. The
// HOME check is the primary defence. The file check catches a test that
// computed the real home some other way, such as an absolute path.
func Run(m *testing.M) int {
	realHome, _ := os.UserHomeDir()
	return runIn(realHome, m.Run)
}

// runIn is Run with the real home and the test body as parameters.
func runIn(realHome string, body func() int) int {
	watched := ""
	if realHome != "" {
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

	code := body()

	leaks := envLeaks(sandbox)
	if watched != "" {
		leaks = append(leaks, fileLeaks(watched, filepath.Join(sandbox, stateDir), before)...)
	}
	if len(leaks) > 0 {
		fmt.Fprintf(os.Stderr, "homeguard: the tests leaked out of the temp home %s:\n", sandbox)
		for _, l := range leaks {
			fmt.Fprintf(os.Stderr, "  %s\n", l)
		}
		if code == 0 {
			code = 1
		}
	}
	return code
}

// envLeaks reports a home variable that no longer names the sandbox.
func envLeaks(sandbox string) []string {
	var out []string
	for _, name := range []string{"HOME", "USERPROFILE"} {
		if got := os.Getenv(name); got != sandbox {
			out = append(out, fmt.Sprintf("%s is %q, want the temp home", name, got))
		}
	}
	return out
}

// fileLeaks lists the files in the watched real directory that changed since
// before and now match, byte for byte, the file at the same path in the
// sandbox's directory.
func fileLeaks(watched, sandboxDir string, before Snapshot) []string {
	var out []string
	for _, c := range Diff(before, Take(watched)) {
		kind, rel, ok := splitChange(c)
		if !ok || kind == "removed:" {
			continue
		}
		mine := hashFile(filepath.Join(sandboxDir, rel))
		if mine == "unreadable" {
			continue
		}
		if hashFile(filepath.Join(watched, rel)) == mine {
			out = append(out, fmt.Sprintf("%s %s in the real %s", kind, rel, watched))
		}
	}
	return out
}

func splitChange(line string) (kind, rel string, ok bool) {
	kind, rel, ok = strings.Cut(line, " ")
	return kind, strings.TrimLeft(rel, " "), ok
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
