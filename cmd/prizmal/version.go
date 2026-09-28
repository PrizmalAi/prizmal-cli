package main

import (
	"runtime/debug"
)

// versionDisplay resolves what `prizmal --version` should print, in three
// tiers, from an injection and the binary's embedded build info:
//
//  1. A release build: goreleaser's ldflags inject the git tag into the
//     version var, and that wins over anything the toolchain can know.
//  2. A `go install module@version` build: the toolchain records the resolved
//     module version (a pseudo-version like v0.0.0-20260910222028-01fff91317fc)
//     as bi.Main.Version. It encodes both the date and the commit, so prefer
//     it over a bare revision when both are present.
//  3. A plain build inside a git checkout: Go embeds vcs.revision (the commit
//     id) in the build settings; show its first seven characters. When the
//     commit id is unknowable, keep the literal "dev".
//
// The fallback tiers only apply when nobody injected a version — i.e. the
// version var still carries its compile-time default "dev".
func versionDisplay(injected string, bi *debug.BuildInfo) string {
	if injected != "" && injected != "dev" {
		return injected
	}
	if bi == nil {
		return injected
	}
	if bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	if rev := vcsSetting(bi, "vcs.revision"); rev != "" {
		if len(rev) > 7 {
			rev = rev[:7]
		}
		return rev
	}
	return injected
}

// vcsSetting returns the value of the named vcs.* build setting. Go embeds
// build settings as "key=value" strings in the buildinfo metadata; only the
// settings Go itself stamps carry these keys, and the lookup is exact.
func vcsSetting(bi *debug.BuildInfo, key string) string {
	for _, s := range bi.Settings {
		if s.Key == key {
			return s.Value
		}
	}
	return ""
}

// resolveVersion is the seam between the binary's compile-time version var and
// the runtime resolution: it hands versionDisplay the live build info.
func resolveVersion(injected string) string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return injected
	}
	return versionDisplay(injected, bi)
}

