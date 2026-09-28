package main

import (
	"os"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
)

// worktreeVCSMinToolchain is the first Go release whose -buildvcs detects a
// linked git worktree, and so the first whose builds can carry vcs.revision
// from one.
//
// Before it, Go looked for `.git` as a directory only. A linked worktree has
// `.git` as a file holding "gitdir: <path>", so Go did not recognize the
// repository, omitted vcs.revision, and the version command printed the literal
// "dev". The tier-3 "short commit id" branch of versionDisplay could not fire in
// the layout this project uses, a bare repo with sibling worktrees.
//
// golang.org/issue/58218 and its duplicate /59068 track the breakage. Go fixed
// it in the 1.27 cycle (cmd/go/internal/vcs: support git worktrees), which is
// why the floor is 1.27 rather than the version in go.mod's go directive.
const worktreeVCSMinToolchain = "v1.27.0"

// TestGoModRequiresWorktreeCapableToolchain pins the worktree half of the
// version story. The module must declare a toolchain at or above the release
// that supports `.git` files, so a contributor building in a worktree reads a
// commit id rather than "dev".
//
// It compares the semantic version instead of looking for a substring. A plain
// "contains toolchain" check would pass if the directive specified a release
// that cannot see a worktree, which is the regression this guards.
func TestGoModRequiresWorktreeCapableToolchain(t *testing.T) {
	raw, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	f, err := modfile.Parse("go.mod", raw, nil)
	if err != nil {
		t.Fatalf("parse go.mod: %v", err)
	}
	if f.Toolchain == nil || f.Toolchain.Name == "" {
		t.Fatalf("go.mod declares no toolchain directive, so a build with an older Go in a git worktree reports no version. "+
			"Worktree VCS stamping needs %s or later", worktreeVCSMinToolchain)
	}

	// modfile reports the toolchain as "go1.27.1", and semver wants a leading "v".
	got := "v" + strings.TrimPrefix(f.Toolchain.Name, "go")
	if !semver.IsValid(got) {
		t.Fatalf("go.mod toolchain %q is not a parseable version", f.Toolchain.Name)
	}
	if semver.Compare(got, worktreeVCSMinToolchain) < 0 {
		t.Errorf("go.mod toolchain %s is below %s, the first release whose -buildvcs detects a linked worktree. "+
			"A build in a worktree will print %q instead of the commit id",
			f.Toolchain.Name, worktreeVCSMinToolchain, "dev")
	}
}
