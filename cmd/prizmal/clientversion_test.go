package main

import (
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
)

// clientVersionFor is what every harness sends in x-prizmal-client-version.
// A release reports its tag, a development build reports dev-<short sha>, and a
// build with no commit reports dev-unknown. The value is never empty, because
// pi sends nothing when the variable behind its header is empty.
func TestClientVersion(t *testing.T) {
	const pseudo = "v0.0.0-20260910222028-01fff91317fc"
	const full = "01fff91317fcaabbccddeeff0011223344556677"
	for _, tc := range []struct {
		name     string
		injected string
		bi       bool
		main     string
		revision string
		want     string
	}{
		{name: "release tag from goreleaser", injected: "1.4.2", want: "1.4.2"},
		{name: "release wins over the commit", injected: "1.4.2", bi: true, revision: full, want: "1.4.2"},
		{name: "checkout build reports the short commit", injected: "dev", bi: true, revision: full, want: "dev-01fff91"},
		{name: "go install of a tag is a release", injected: "dev", bi: true, main: "v1.4.2", want: "1.4.2"},
		{name: "go install of a pseudo-version is a development build", injected: "dev", bi: true, main: pseudo, want: "dev-01fff91"},
		{name: "no commit at all", injected: "dev", bi: true, want: "dev-unknown"},
		{name: "no build info", injected: "dev", want: "dev-unknown"},
		{name: "empty injection", injected: "", want: "dev-unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bi := versionBuildInfo(tc.main, tc.revision, "")
			if !tc.bi {
				bi = nil
			}
			if got := clientVersion(tc.injected, bi); got != tc.want {
				t.Errorf("clientVersion = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClientVersionIsPublishedToTheLaunchers(t *testing.T) {
	t.Cleanup(func() { envconfig.SetClientVersion("") })
	publishClientVersion("3.1.0")
	if got := envconfig.ClientVersion(); got != "3.1.0" {
		t.Errorf("envconfig.ClientVersion() = %q, want 3.1.0", got)
	}
}

// envconfig, not the resolver, rejects a value the Switch would drop whole.
func TestMalformedClientVersionIsNotSent(t *testing.T) {
	t.Cleanup(func() { envconfig.SetClientVersion("") })
	publishClientVersion("1.4 2")
	if got := envconfig.ClientVersion(); got != envconfig.UnknownClientVersion {
		t.Errorf("envconfig.ClientVersion() = %q, want %q", got, envconfig.UnknownClientVersion)
	}
}
