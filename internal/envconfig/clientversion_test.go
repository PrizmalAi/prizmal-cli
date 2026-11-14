package envconfig

import (
	"strings"
	"testing"
)

// A harness refuses to send anything when the header's value is empty (pi
// resolves it from the environment and stops the request), so the value is
// never empty, whatever the process was told.
func TestClientVersionIsNeverEmpty(t *testing.T) {
	for _, tc := range []struct{ name, set, want string }{
		{name: "never set", set: "", want: UnknownClientVersion},
		{name: "blank", set: "  \t", want: UnknownClientVersion},
		{name: "a release", set: "1.4.2", want: "1.4.2"},
		{name: "a development build", set: "dev-abc1234", want: "dev-abc1234"},
		{name: "a character the Switch rejects", set: "1.4 2", want: UnknownClientVersion},
		{name: "a newline would split the header", set: "1.4\nx-evil: 1", want: UnknownClientVersion},
		{name: "over the Switch's 64-byte bound", set: strings.Repeat("1", 65), want: UnknownClientVersion},
		{name: "exactly 64 bytes", set: strings.Repeat("1", 64), want: strings.Repeat("1", 64)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			SetClientVersion(tc.set)
			t.Cleanup(func() { SetClientVersion("") })
			if got := ClientVersion(); got != tc.want {
				t.Errorf("ClientVersion() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClientVersionHeaderSpelling(t *testing.T) {
	if ClientVersionHeader != "x-prizmal-client-version" {
		t.Errorf("header = %q", ClientVersionHeader)
	}
	if ClientVersionEnvVar != "PRIZMAL_CLIENT_VERSION" {
		t.Errorf("env var = %q", ClientVersionEnvVar)
	}
	if UnknownClientVersion != "dev-unknown" {
		t.Errorf("unknown = %q", UnknownClientVersion)
	}
}
