package opencode

import "testing"

// The capability table is the one place a launch's behavior differs by
// major; every other caller consults it instead of branching on the major
// itself. An unknown major takes the 1.x row, matching resolveOpenCodeMajor's
// own fail-open rule.
func TestCapabilitiesForTable(t *testing.T) {
	cases := []struct {
		name  string
		major openCodeMajor
		want  majorCapabilities
	}{
		{
			name:  "1.x",
			major: openCodeMajor1,
			want:  majorCapabilities{},
		},
		{
			name:  "2.x",
			major: openCodeMajor2,
			want: majorCapabilities{
				effectAuthPlugin:                 true,
				dropsReasoningFields:             true,
				searchNeedsUnprovenToolTransform: true,
			},
		},
		{
			name:  "unknown falls back to the 1.x row",
			major: openCodeMajorUnknown,
			want:  majorCapabilities{},
		},
		{
			name:  "a future major not yet in the table falls back to the 1.x row",
			major: openCodeMajor(99),
			want:  majorCapabilities{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := capabilitiesFor(tc.major); got != tc.want {
				t.Errorf("capabilitiesFor(%v) = %+v, want %+v", tc.major, got, tc.want)
			}
		})
	}
}

// 1.x's search tool is file-based and unconditional: this flag must stay
// false for 1.x, or searchDirEnv would wrongly degrade a working 1.x launch.
func TestV1NeverSetsSearchNeedsUnprovenToolTransform(t *testing.T) {
	if capabilitiesFor(openCodeMajor1).searchNeedsUnprovenToolTransform {
		t.Error("1.x capability row sets searchNeedsUnprovenToolTransform; its tools/ directory tool does not depend on it")
	}
}
