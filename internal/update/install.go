// Package update finds out how the running prizmal was installed, whether a
// newer release exists, and how to replace the binary: Homebrew, `go install`,
// a build from a clone, or an unpacked archive. Version queries go through
// functions a test replaces, so no test needs the network.
package update

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
)

// ModulePath is the module the released binary reports, which names the
// package go install builds. A binary whose main module differs came from a
// fork, and no check below can update it.
const ModulePath = "github.com/PrizmalAi/prizmal-cli"

// PackagePath is the command path go install builds the binary from.
const PackagePath = ModulePath + "/cmd/prizmal"

// Method is where the running binary came from, as Detect classified it.
type Method int

const (
	// Unknown covers every build this package cannot name: a fork, a binary
	// moved out of its expected directory, a build with no version data.
	Unknown Method = iota
	// Homebrew is a binary inside the Caskroom, so brew installed it.
	Homebrew
	// GoInstall is a `go install ...@version` build the toolchain stamped in
	// place, identified by the module version and checksum the build info
	// carries.
	GoInstall
	// Source is a build inside a clone: git stamped it, so `go install` would
	// not update it.
	Source
	// Archive is a release binary unpacked somewhere by hand, which no
	// installer owns.
	Archive
)

func (m Method) String() string {
	switch m {
	case Homebrew:
		return "Homebrew"
	case GoInstall:
		return "go install"
	case Source:
		return "source"
	case Archive:
		return "archive"
	default:
		return "unknown"
	}
}

// Install names the running binary's origin: how it was installed, the
// version string to compare, and — for Homebrew and go install — what a
// replacement command needs.
type Install struct {
	Method Method
	// Version is the version this binary reports, as a v-prefixed tag when
	// the version is a tag. It is empty for an Unknown build.
	Version string
	// Exe is the resolved path of the running binary.
	Exe string
	// BrewPrefix is the Homebrew prefix whose Caskroom held the binary
	// (Method Homebrew).
	BrewPrefix string
	// Package is the module/package path go install updates (Method
	// GoInstall). It is the live module path only when the binary really
	// carries it.
	Package string
}

// BrewLink is the Homebrew link through which the running binary was
// invoked, <prefix>/bin/prizmal. Re-installing the cask rewrites the link,
// so an upgrade only needs the prefix.
func (i Install) BrewLink() string {
	return filepath.Join(i.BrewPrefix, "bin", binaryName())
}

// binaryName is the installed binary's file name, prizmal on every platform
// the release builds.
func binaryName() string {
	return "prizmal"
}

// CaskroomMarker is the directory under a Homebrew prefix that holds
// installed casks, and prizmal's cask directory under it.
const (
	CaskroomMarker = "Caskroom"
	CaskName       = "prizmal"
)

// Detect classifies a binary from what Go records in it. injected is the
// -X main.version value, bi its build info, and exe the resolved executable
// path. An empty or dev injected version means the toolchain stamped the
// version, which is what tells go install from a clone build apart.
//
// A goreleaser build is also made from a checkout, so it carries both the
// injected tag and vcs.revision (as v0.1.2+dirty measured on the v0.1.2
// release). The injected version is checked first for that reason.
func Detect(injected string, bi *debug.BuildInfo, exe string) Install {
	if injected != "" && injected != "dev" {
		if v := normalizeTag(injected); v != "" {
			inst := Install{Version: v}
			if e := executablePath(exe); e != "" {
				inst.Exe = e
			}
			if pp, ok := caskroomPrefix(inst.Exe); ok {
				inst.Method = Homebrew
				inst.BrewPrefix = pp
			} else {
				inst.Method = Archive
			}
			return inst
		}
		// An injected non-semver word ("nightly") names a build this
		// package cannot update.
		return Install{}
	}
	if bi == nil || bi.Main.Path != ModulePath {
		return Install{}
	}
	v, sum := bi.Main.Version, bi.Main.Sum
	switch {
	case sum != "":
		// A module checksum is written when a module version is resolved
		// by the toolchain: `go install module/version` and `go run
		// module/version`. go run's binary lives in the build cache, which
		// disappears with it.
		e := executablePath(exe)
		if e == "" || goBuildPath(e) {
			return Install{}
		}
		return Install{Method: GoInstall, Version: v, Exe: e, Package: PackagePath}
	case v != "" && v != "(devel)" && !strings.HasPrefix(v, "v0.0.0-") && vcsRevision(bi) != "":
		// A clone build: a pseudo-version and no checksum, because the
		// module was not resolved from the proxy. A v0.0.0 pseudo-version
		// means the clone has no tag history (a shallow CI checkout), so it
		// sorts below every release and says nothing about the code.
		return Install{Method: Source, Version: v, Exe: exe}
	}
	return Install{}
}

// normalizeTag prefixes a bare semver number with v, so 0.1.2 becomes
// v0.1.2. Anything without a leading number is returned empty.
func normalizeTag(v string) string {
	if v == "" {
		return ""
	}
	if strings.HasPrefix(v, "v") {
		return v
	}
	if c := v[0]; c >= '0' && c <= '9' {
		return "v" + v
	}
	return ""
}

// executablePath is exe when it is non-empty, else the running binary as the
// OS reports it. Detect's callers resolve symlinks before calling, so this
// stays simple.
func executablePath(exe string) string {
	if exe != "" {
		return exe
	}
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	return p
}

// ResolveExecutable returns the running or named binary with symlinks
// resolved, because Homebrew invokes the cask binary through a link in
// <prefix>/bin. It returns the input unchanged when it cannot be resolved,
// so a binary whose path has vanished still classifies.
func ResolveExecutable(exe string) string {
	if exe == "" {
		p, err := os.Executable()
		if err != nil {
			return ""
		}
		exe = p
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved
	}
	return exe
}

// caskroomPrefix reports whether path is inside a Homebrew cask directory,
// <prefix>/Caskroom/prizmal/..., and returns the Homebrew prefix when it is.
// Homebrew installs prizmal as
// <prefix>/Caskroom/prizmal/<version>/prizmal.
func caskroomPrefix(path string) (string, bool) {
	d := filepath.Dir(path)
	for depth := 0; depth < 8 && d != filepath.Dir(d); depth++ {
		if filepath.Base(d) != CaskName {
			d = filepath.Dir(d)
			continue
		}
		caskroom := filepath.Dir(d)
		if filepath.Base(caskroom) != CaskroomMarker {
			return "", false
		}
		return filepath.Dir(caskroom), true
	}
	return "", false
}

// goBuildPath reports whether path is inside a go run binary directory. go
// run builds into a "go-build<digits>" directory under the OS temp dir and
// removes it at exit, so such a binary is a throwaway and not an install.
// It matches the directory name rather than the build cache location,
// because the cache root differs between machines and is unset under tests.
var goBuildPattern = regexp.MustCompile(`(^|[\\/])go-build[0-9]+([\\/])b[0-9]+[\\/]exe[\\/]`)

func goBuildPath(path string) bool {
	return goBuildPattern.MatchString(path)
}

// vcsRevision reads the vcs.revision build setting, the commit a git
// checkout build was made from.
func vcsRevision(bi *debug.BuildInfo) string {
	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" {
			return s.Value
		}
	}
	return ""
}
