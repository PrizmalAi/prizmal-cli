//go:build !unix

package device

// stdinReady has no non-blocking probe off Unix, so it reports ready and the
// caller's read blocks until the operator answers. On these platforms an
// approval from another browser ends the login, but the Enter read stays
// pending until a key arrives.
func stdinReady() (bool, error) { return true, nil }
