package main

import (
	"runtime/debug"
	"testing"
)

// versionBuildInfo builds a *debug.BuildInfo the way the Go toolchain would
// hand one over, so the table can name each build flavor explicitly.
func versionBuildInfo(mainVersion string, vcsRevision string, vcsModified string) *debug.BuildInfo {
	bi := &debug.BuildInfo{GoVersion: "go1.22"}
	if mainVersion != "" {
		bi.Main.Version = mainVersion
	}
	if vcsRevision != "" {
		bi.Settings = append(bi.Settings, debug.BuildSetting{Key: "vcs.revision", Value: vcsRevision})
	}
	if vcsModified != "" {
		bi.Settings = append(bi.Settings, debug.BuildSetting{Key: "vcs.modified", Value: vcsModified})
	}
	return bi
}

// TestVersionDisplay pins the three-way resolution:
// injected (goreleaser ldflags) > module pseudo-version (go install @version)
// > short commit id (vcs.revision, from a plain git-checkout build) > "dev".
func TestVersionDisplay(t *testing.T) {
	const pseudo = "v0.0.0-20260910222028-01fff91317fc"
	const full = "01fff91317fcaabbccddeeff0011223344556677"
	cases := []struct {
		name    string
		version string // the injectable var
		bi      *debug.BuildInfo
		want    string
	}{
		{
			name:    "injected version wins over everything",
			version: "v0.1.0",
			bi:      versionBuildInfo(pseudo, full, "false"),
			want:    "v0.1.0",
		},
		{
			name:    "injected version wins even when the fallback knows nothing",
			version: "v0.1.0",
			bi:      versionBuildInfo("(devel)", "", ""),
			want:    "v0.1.0",
		},
		{
			name:    "uninjected + module pseudo-version shows the pseudo-version",
			version: "dev",
			bi:      versionBuildInfo(pseudo, full, "false"),
			want:    pseudo,
		},
		{
			name:    "uninjected + devel main + vcs.revision shows the short commit id",
			version: "dev",
			bi:      versionBuildInfo("(devel)", full, "false"),
			want:    "01fff91",
		},
		{
			name:    "uninjected + (devel) main and no vcs.revision stays dev",
			version: "dev",
			bi:      versionBuildInfo("(devel)", "", ""),
			want:    "dev",
		},
		{
			name:    "nil build info stays dev",
			version: "dev",
			bi:      nil,
			want:    "dev",
		},
		{
			name:    "empty main version counts as unknowable",
			version: "dev",
			bi:      versionBuildInfo("", "", ""),
			want:    "dev",
		},
		{
			name:    "short vcs.revision is used as-is",
			version: "dev",
			bi:      versionBuildInfo("(devel)", "abc1234", "false"),
			want:    "abc1234",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := versionDisplay(tc.version, tc.bi); got != tc.want {
				t.Errorf("versionDisplay(%q, %v) = %q, want %q", tc.version, tc.bi != nil, got, tc.want)
			}
		})
	}
}

// vcsSetting returns the value of a vcs.* build setting by its exact key.
// ReadBuildInfo reports settings as key=value strings; the parser on the
// receiving side strips the "vcs.revision=" prefix with exactly that prefix.
func TestVCSSettingParsesPrefixedValue(t *testing.T) {
	bi := &debug.BuildInfo{
		Settings: []debug.BuildSetting{
			{Key: "vcs.time", Value: "2026-09-10T22:20:28Z"},
			{Key: "vcs.revision", Value: "01fff91317fcaabbccddeeff0011223344556677"},
		},
	}
	if got := vcsSetting(bi, "vcs.revision"); got != "01fff91317fcaabbccddeeff0011223344556677" {
		t.Errorf("vcsSetting(vcs.revision) = %q", got)
	}
	if got := vcsSetting(bi, "vcs.modified"); got != "" {
		t.Errorf("vcsSetting for absent key = %q, want empty", got)
	}
}
