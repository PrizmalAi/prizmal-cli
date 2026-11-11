package envconfig

import "strings"

const (
	// ClientVersionHeader is the request header that carries the CLI's version
	// to the Switch on every model request.
	ClientVersionHeader = "x-prizmal-client-version"

	// ClientVersionEnvVar is the variable a launch exports for pi, whose
	// provider headers resolve "$NAME" values from the environment.
	ClientVersionEnvVar = "PRIZMAL_CLIENT_VERSION"

	// UnknownClientVersion is what a build with neither a release tag nor a
	// commit reports.
	UnknownClientVersion = "dev-unknown"

	// maxClientVersionBytes is the longest value the Switch accepts.
	maxClientVersionBytes = 64
)

var clientVersion string

// SetClientVersion records the version this CLI build reports.
func SetClientVersion(v string) {
	mu.Lock()
	defer mu.Unlock()
	clientVersion = strings.TrimSpace(v)
}

// ClientVersion is the value every harness sends in ClientVersionHeader. It is
// never empty: pi refuses to send a request when the variable behind its header
// is empty, so a missing or malformed value becomes UnknownClientVersion. A
// value outside the Switch's bound (at most 64 bytes of letters, digits and
// ".+_-") is replaced too, because the Switch would drop it whole.
func ClientVersion() string {
	mu.RLock()
	v := clientVersion
	mu.RUnlock()
	if !validClientVersion(v) {
		return UnknownClientVersion
	}
	return v
}

func validClientVersion(v string) bool {
	if v == "" || len(v) > maxClientVersionBytes {
		return false
	}
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '+', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}
