package opencode

import (
	"errors"
	"fmt"
	"regexp"
)

// openCodeMajor is the major release line a launch runs against. The
// capability table (capability_table.go) is keyed on this, never on the npm
// package name or the executable name: OpenCode 1.x and 2.x both install an
// `opencode` command, so the name tells a launch nothing about which plugin
// shape, config fields or tool-registration route the install understands.
type openCodeMajor int

const (
	// openCodeMajorUnknown is unparseable --version output, including the
	// "local" string a dev build prints. A launch on it takes the 1.x path
	// and prints one warning line, so a source build or a future major this
	// launcher has never seen is not newly blocked, only treated as 1.x.
	openCodeMajorUnknown openCodeMajor = 0
	openCodeMajor1       openCodeMajor = 1
	openCodeMajor2       openCodeMajor = 2
)

// openCodeVersionPattern matches the first X.Y.Z in --version output, so
// detection works whether the line is bare ("2.2.6") or name-prefixed
// ("opencode 2.2.6"), and whether or not it carries a leading "v".
var openCodeVersionPattern = regexp.MustCompile(`(\d+)\.\d+\.\d+`)

// errVersionLookup is a sentinel for tests exercising the lookup-failure path;
// production callers only check err != nil.
var errVersionLookup = errors.New("opencode version lookup failed")

// detectOpenCodeMajor extracts the major version from the first X.Y.Z in s.
// It never looks at the executable or package name. A major this launcher
// does not recognise (0, or anything past 2) is reported as unknown rather
// than guessed into the 2.x path: a future major may drop the very
// capabilities the 2.x path assumes.
func detectOpenCodeMajor(s string) openCodeMajor {
	match := openCodeVersionPattern.FindStringSubmatch(s)
	if match == nil {
		return openCodeMajorUnknown
	}
	switch match[1] {
	case "1":
		return openCodeMajor1
	case "2":
		return openCodeMajor2
	default:
		return openCodeMajorUnknown
	}
}

// resolveOpenCodeMajor runs the installed OpenCode's own --version and
// resolves the major it reports, failing open to the 1.x path — today's only
// path — whenever the version cannot be read or parsed. That covers a lookup
// error (OpenCode absent, exec failure) and unparseable output including
// "local", and prints one warning line for the latter so the operator knows
// a version check failed open rather than silently picking a path.
func resolveOpenCodeMajor() openCodeMajor {
	out, err := openCodeVersion()
	if err != nil {
		return openCodeMajor1
	}
	major := detectOpenCodeMajor(out)
	if major == openCodeMajorUnknown {
		_, _ = fmt.Fprintf(noticeOut, "prizmal: could not read an OpenCode major version from %q, assuming 1.x\n", out)
		return openCodeMajor1
	}
	return major
}
