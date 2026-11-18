package opencode

import "testing"

// detectOpenCodeMajor takes the first X.Y.Z in --version output and branches
// on the major digit, never on the npm package name or the executable name:
// both OpenCode 1.x and 2.x install an `opencode` command. Unparseable output
// or the "local" dev build is openCodeMajorUnknown, and the caller takes the
// 1.x path and prints one warning line for that case.
func TestDetectOpenCodeMajor(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want openCodeMajor
	}{
		{"bare 1.x", "1.19.3", openCodeMajor1},
		{"bare 2.x", "2.2.6", openCodeMajor2},
		{"trailing newline", "1.19.3\n", openCodeMajor1},
		{"name-prefixed", "opencode 2.2.6", openCodeMajor2},
		{"v-prefixed", "v2.2.6", openCodeMajor2},
		{"local dev build", "local", openCodeMajorUnknown},
		{"empty", "", openCodeMajorUnknown},
		{"garbage", "not a version", openCodeMajorUnknown},
		{"future major 3.x is unknown, not 2.x", "3.0.0", openCodeMajorUnknown},
		{"major 0 is unknown", "0.9.9", openCodeMajorUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectOpenCodeMajor(tc.out); got != tc.want {
				t.Errorf("detectOpenCodeMajor(%q) = %v, want %v", tc.out, got, tc.want)
			}
		})
	}
}

// resolveOpenCodeMajor fails open to the 1.x path, exactly as today's
// behavior, whenever the version cannot be read or parsed, and prints one
// warning line to noticeOut in that case.
func TestResolveOpenCodeMajorFailsOpenToV1(t *testing.T) {
	cases := []struct {
		name  string
		out   string
		warns bool
	}{
		{"1.x output", "1.19.3", false},
		{"2.x output", "2.2.6", false},
		{"local build warns and falls back to 1.x", "local", true},
		{"garbage warns and falls back to 1.x", "garbage", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			old := openCodeVersion
			t.Cleanup(func() { openCodeVersion = old })
			openCodeVersion = func() (string, error) { return tc.out, nil }

			buf := captureNotices(t)

			major := resolveOpenCodeMajor()
			wantMajor := detectOpenCodeMajor(tc.out)
			if wantMajor == openCodeMajorUnknown {
				wantMajor = openCodeMajor1
			}
			if major != wantMajor {
				t.Errorf("resolveOpenCodeMajor() = %v, want %v", major, wantMajor)
			}
			if tc.warns != (buf.Len() > 0) {
				t.Errorf("warning written = %v, want %v (buf=%q)", buf.Len() > 0, tc.warns, buf.String())
			}
		})
	}
}

// A version lookup failure (opencode not installed, or the exec itself
// failing) also fails open to 1.x, matching today's behavior for every
// existing caller of openCodeVersion.
func TestResolveOpenCodeMajorFailsOpenWhenVersionLookupErrors(t *testing.T) {
	old := openCodeVersion
	t.Cleanup(func() { openCodeVersion = old })
	openCodeVersion = func() (string, error) { return "", errVersionLookup }

	if got := resolveOpenCodeMajor(); got != openCodeMajor1 {
		t.Errorf("resolveOpenCodeMajor() = %v, want openCodeMajor1", got)
	}
}
