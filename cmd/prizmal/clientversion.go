package main

import (
	"regexp"
	"runtime/debug"
	"strings"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
)

// pseudoVersionHash picks the commit out of a module pseudo-version, which
// `go install module@commit` records. Three shapes exist: v0.0.0-<ts>-<hash>
// (no tag), vX.Y.Z-pre.0.<ts>-<hash> (after a pre-release tag) and
// vX.Y.(Z+1)-0.<ts>-<hash> (after a release tag). The timestamp follows a
// hyphen in the first and a dot in the others.
var pseudoVersionHash = regexp.MustCompile(`[-.]\d{14}-([0-9a-f]{12})$`)

// shortCommitLen is how much of a commit id a development build reports.
const shortCommitLen = 7

// clientVersion is the value the CLI sends to the Switch in
// x-prizmal-client-version, from the release tag and the build info:
//
//  1. A release build: goreleaser injects the tag into version, and it wins.
//  2. `go install module@vX.Y.Z`: the toolchain records the tag as the module
//     version, reported without its leading "v" as a release build is.
//  3. Any other build is a development build and reports dev-<short commit>:
//     a pseudo-version or a git checkout supplies the commit. With no commit it
//     reports dev-unknown.
//
// The result is never empty, because pi sends nothing when the variable behind
// its header is empty.
func clientVersion(injected string, bi *debug.BuildInfo) string {
	if injected != "" && injected != "dev" {
		return injected
	}
	if bi == nil {
		return envconfig.UnknownClientVersion
	}
	if main := bi.Main.Version; main != "" && main != "(devel)" {
		if m := pseudoVersionHash.FindStringSubmatch(main); m != nil {
			return "dev-" + m[1][:shortCommitLen]
		}
		return strings.TrimPrefix(main, "v")
	}
	if rev := vcsSetting(bi, "vcs.revision"); rev != "" {
		if len(rev) > shortCommitLen {
			rev = rev[:shortCommitLen]
		}
		return "dev-" + rev
	}
	return envconfig.UnknownClientVersion
}

// publishClientVersion records the version for the launchers. The build info
// is read here, so a test passes the injected value alone.
func publishClientVersion(injected string) {
	bi, _ := debug.ReadBuildInfo()
	envconfig.SetClientVersion(clientVersion(injected, bi))
}
