package update

import (
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"testing"
)

const testPackage = ModulePath + "/cmd/prizmal"

// buildInfo builds a *debug.BuildInfo the way the Go toolchain stamps one.
func buildInfo(modulePath, version, sum, revision string) *debug.BuildInfo {
	bi := &debug.BuildInfo{Path: testPackage}
	bi.Main = debug.Module{Path: modulePath, Version: version, Sum: sum}
	if revision != "" {
		bi.Settings = append(bi.Settings,
			debug.BuildSetting{Key: "vcs", Value: "git"},
			debug.BuildSetting{Key: "vcs.revision", Value: revision})
	}
	return bi
}

func TestDetect(t *testing.T) {
	const rev = "a67f209a699ed0e16236aeef889fa964edb29ba4"
	const sum = "h1:/ZLHbKwLKWBfl1IBiE8Ob0DFTwHce3lY1r0V6bgjCWw="
	// A goreleaser build is made from a git checkout, so it carries
	// vcs.revision and a +dirty module version as well as the tag.
	release := buildInfo(ModulePath, "v0.1.2+dirty", "", rev)

	cases := []struct {
		name     string
		injected string
		bi       *debug.BuildInfo
		exe      string
		want     Install
		posix    bool // POSIX paths, so Windows cannot run the row
	}{
		{
			name:     "release build in the Homebrew Caskroom",
			posix:    true,
			injected: "0.1.2",
			bi:       release,
			exe:      "/opt/homebrew/Caskroom/prizmal/0.1.2/prizmal",
			want: Install{Method: Homebrew, Version: "v0.1.2",
				Exe: "/opt/homebrew/Caskroom/prizmal/0.1.2/prizmal", BrewPrefix: "/opt/homebrew"},
		},
		{
			name:     "release build in the Linuxbrew Caskroom",
			posix:    true,
			injected: "0.1.2",
			bi:       release,
			exe:      "/home/linuxbrew/.linuxbrew/Caskroom/prizmal/0.1.2/prizmal",
			want: Install{Method: Homebrew, Version: "v0.1.2",
				Exe: "/home/linuxbrew/.linuxbrew/Caskroom/prizmal/0.1.2/prizmal", BrewPrefix: "/home/linuxbrew/.linuxbrew"},
		},
		{
			name:     "release build in another cask's directory is an archive",
			injected: "0.1.2",
			bi:       release,
			exe:      "/opt/homebrew/Caskroom/prizmal-nightly/0.1.2/prizmal",
			want:     Install{Method: Archive, Version: "v0.1.2", Exe: "/opt/homebrew/Caskroom/prizmal-nightly/0.1.2/prizmal"},
		},
		{
			name:     "release build anywhere else is an unpacked archive",
			injected: "0.1.2",
			bi:       release,
			exe:      "/usr/local/bin/prizmal",
			want:     Install{Method: Archive, Version: "v0.1.2", Exe: "/usr/local/bin/prizmal"},
		},
		{
			name:     "an injected version that already has the v prefix",
			injected: "v0.1.2",
			bi:       release,
			exe:      "/usr/local/bin/prizmal",
			want:     Install{Method: Archive, Version: "v0.1.2", Exe: "/usr/local/bin/prizmal"},
		},
		{
			name:     "a goreleaser snapshot keeps its prerelease suffix",
			injected: "0.1.3-next",
			bi:       release,
			exe:      "/usr/local/bin/prizmal",
			want:     Install{Method: Archive, Version: "v0.1.3-next", Exe: "/usr/local/bin/prizmal"},
		},
		{
			name:     "an injected version that is not semver is unknown",
			injected: "nightly",
			bi:       release,
			exe:      "/usr/local/bin/prizmal",
			want:     Install{Method: Unknown},
		},
		{
			name:     "go install of a tag",
			injected: "dev",
			bi:       buildInfo(ModulePath, "v0.1.2", sum, ""),
			exe:      "/home/dev/go/bin/prizmal",
			want: Install{Method: GoInstall, Version: "v0.1.2", Exe: "/home/dev/go/bin/prizmal",
				Package: testPackage},
		},
		{
			name:     "go install of a commit records a pseudo-version",
			injected: "dev",
			bi:       buildInfo(ModulePath, "v0.1.3-0.20261005182724-84fe65cc8617", sum, ""),
			exe:      "/home/dev/go/bin/prizmal",
			want: Install{Method: GoInstall, Version: "v0.1.3-0.20261005182724-84fe65cc8617",
				Exe: "/home/dev/go/bin/prizmal", Package: testPackage},
		},
		{
			name:     "go run of a version is a throwaway binary",
			injected: "dev",
			bi:       buildInfo(ModulePath, "v0.1.2", sum, ""),
			exe:      "/tmp/go-build3011/b001/exe/prizmal",
			want:     Install{Method: Unknown},
		},
		{
			// Go 1.24 and later stamp a clone build with a version from its tags.
			name:     "build from a clone after a tag",
			injected: "dev",
			bi:       buildInfo(ModulePath, "v0.1.3-0.20261005222209-a67f209a699e", "", rev),
			exe:      "/home/dev/src/prizmal-cli/prizmal",
			want: Install{Method: Source, Version: "v0.1.3-0.20261005222209-a67f209a699e",
				Exe: "/home/dev/src/prizmal-cli/prizmal"},
		},
		{
			name:     "build from a clone with no tag history",
			injected: "dev",
			bi:       buildInfo(ModulePath, "v0.0.0-20261007180007-9e5bac6ad4b4", "", rev),
			exe:      "/home/runner/work/prizmal-cli/prizmal",
			want:     Install{Method: Unknown},
		},
		{
			name:     "build from a clone with local changes",
			injected: "dev",
			bi:       buildInfo(ModulePath, "v0.1.2+dirty", "", rev),
			exe:      "/home/dev/src/prizmal-cli/prizmal",
			want:     Install{Method: Source, Version: "v0.1.2+dirty", Exe: "/home/dev/src/prizmal-cli/prizmal"},
		},
		{
			name:     "build without version control has no version",
			injected: "dev",
			bi:       buildInfo(ModulePath, "(devel)", "", ""),
			exe:      "/home/dev/prizmal",
			want:     Install{Method: Unknown},
		},
		{
			name:     "build from a clone without version control stamping",
			injected: "dev",
			bi:       buildInfo(ModulePath, "(devel)", "", rev),
			exe:      "/home/dev/prizmal",
			want:     Install{Method: Unknown},
		},
		{
			name:     "a fork under another module path",
			injected: "dev",
			bi:       buildInfo("github.com/someone/prizmal-cli", "v0.1.2", sum, ""),
			exe:      "/home/dev/go/bin/prizmal",
			want:     Install{Method: Unknown},
		},
		{
			name:     "no build info",
			injected: "dev",
			bi:       nil,
			exe:      "/home/dev/go/bin/prizmal",
			want:     Install{Method: Unknown},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.posix && runtime.GOOS == "windows" {
				t.Skip("Homebrew paths are POSIX")
			}
			got := Detect(tc.injected, tc.bi, tc.exe)
			if got != tc.want {
				t.Fatalf("Detect = %+v\nwant     %+v", got, tc.want)
			}
		})
	}
}

func TestDetectReadsWindowsPaths(t *testing.T) {
	got := Detect("dev", buildInfo(ModulePath, "v0.1.2", "h1:x=", ""), `C:\Users\dev\go\bin\prizmal.exe`)
	if got.Method != GoInstall {
		t.Fatalf("Method = %v, want GoInstall", got.Method)
	}
	got = Detect("dev", buildInfo(ModulePath, "v0.1.2", "h1:x=", ""), `C:\Users\dev\AppData\Local\Temp\go-build123\b001\exe\prizmal.exe`)
	if got.Method != Unknown {
		t.Fatalf("go run on Windows: Method = %v, want Unknown", got.Method)
	}
}

// Homebrew links the cask binary from bin into the Caskroom. The running
// binary resolves to the Caskroom path, which is what Detect reads.
func TestExecutableResolvesTheHomebrewLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Homebrew does not run on Windows")
	}
	prefix := t.TempDir()
	caskroom := filepath.Join(prefix, "Caskroom", "prizmal", "0.1.2")
	if err := os.MkdirAll(caskroom, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(prefix, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(caskroom, "prizmal")
	if err := os.WriteFile(target, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(prefix, "bin", "prizmal")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	resolved := ResolveExecutable(link)
	got := Detect("0.1.2", nil, resolved)
	wantPrefix, err := filepath.EvalSymlinks(prefix)
	if err != nil {
		t.Fatal(err)
	}
	if got.Method != Homebrew || got.BrewPrefix != wantPrefix {
		t.Fatalf("Detect(%s) = %+v, want Homebrew under %s", resolved, got, wantPrefix)
	}
	if got.BrewLink() != filepath.Join(wantPrefix, "bin", "prizmal") {
		t.Fatalf("BrewLink = %s, want the link in %s/bin", got.BrewLink(), wantPrefix)
	}
}

func TestResolveExecutableKeepsAnUnresolvablePath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone", "prizmal")
	if got := ResolveExecutable(missing); got != missing {
		t.Fatalf("ResolveExecutable(%s) = %s, want the path unchanged", missing, got)
	}
}
