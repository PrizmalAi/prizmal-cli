// Package selfpath resolves the path a launcher records for its credential
// helper: the command a harness runs to fetch a fresh token from prizmal.
//
// The recorded path must survive a package-manager upgrade. os.Executable
// cannot be it: on Linux it reads /proc/self/exe, which resolves symlinks,
// so a launch through Homebrew's stable bin symlink records the versioned
// cask path under Caskroom. A cask upgrade deletes that directory, and a
// session still running then fails every token refresh with "no such file".
// The path the operator typed (or the PATH match their shell ran) names the
// stable symlink, which an upgrade retargets rather than removes.
package selfpath

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// argv0, lookPath and executable are the seams a test drives: the typed
// invocation, the PATH search and the resolved-self fallback. The package
// level keeps the launch call sites one-line; a test that stubs one restores
// it with t.Cleanup.
var (
	argv0      = func() string { return os.Args[0] }
	lookPath   = exec.LookPath
	executable = os.Executable
)

// Path returns the prizmal path to record in a launcher's credential
// command.
//
//   - An argv0 with a path separator is what the operator typed, made
//     absolute when it is relative: it names the entry point the launch
//     came from, stable or not, and it wins over any PATH match.
//   - A bare argv0 (a PATH launch) resolves through exec.LookPath under the
//     name the operator ran, which answers with the matched path itself and
//     never resolves symlinks: a Homebrew launch records .../bin/prizmal, not
//     the Caskroom target, and a launch through a renamed symlink records
//     that symlink rather than a different install of the standard name.
//   - With no match on PATH at all, the resolved executable is the only path
//     left, so an os.Executable failure there is the caller's error.
func Path() (string, error) {
	typed := argv0()
	if hasSeparator(typed) {
		return filepath.Abs(typed)
	}
	// Look up the name the operator ran, never a fixed "prizmal": an
	// invocation through a renamed symlink on PATH (pz) must record that
	// symlink. Searching for "prizmal" would find a different install or
	// nothing, and the latter falls through to the symlink-resolving
	// os.Executable this package exists to avoid.
	if path, err := lookPath(filepath.Base(typed)); err == nil {
		return path, nil
	}
	return executable()
}

// hasSeparator reports whether name carries a path, so the operator invoked
// prizmal by path rather than by a PATH name. Both separators count: Windows
// accepts slash as well as backslash, and its own separator is what
// filepath.Abs makes the path relative to.
func hasSeparator(name string) bool {
	return strings.ContainsRune(name, '/') || strings.ContainsRune(name, filepath.Separator)
}
