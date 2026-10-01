package device

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"time"
)

// Enrollment defaults, from the design: poll every 2 seconds for at most 10
// minutes, and treat a 404 as "still pending".
const (
	DefaultPollInterval = 2 * time.Second
	DefaultPollTimeout  = 10 * time.Minute
)

// LoginOptions tunes the enrollment loop. The zero value is production:
// a real terminal, a real clock, and the default cadence. Tests supply a
// fake sleep, clock and browser so the loop runs without waiting.
type LoginOptions struct {
	// AuthorizeBaseURL is the admin app the operator approves in, the
	// origin of the /cli/authorize consent page.
	AuthorizeBaseURL string
	// DeviceName is offered to the page as the default label. The page
	// decides the name it enrolls; this is only a suggestion.
	DeviceName string
	// Interval between polls, DefaultPollInterval when zero.
	Interval time.Duration
	// Timeout bounds the wait, DefaultPollTimeout when zero.
	Timeout time.Duration
	// Sleep waits, time.Sleep when nil.
	Sleep func(time.Duration)
	// Now reads the clock, time.Now when nil.
	Now func() time.Time
	// OpenBrowser launches the URL, BestEffortOpenBrowser when nil.
	OpenBrowser func(string) error
	// Out receives the URL, fingerprint and progress, os.Stderr when nil.
	Out io.Writer
}

func (o *LoginOptions) withDefaults() LoginOptions {
	if o.Interval <= 0 {
		o.Interval = DefaultPollInterval
	}
	if o.Timeout <= 0 {
		o.Timeout = DefaultPollTimeout
	}
	if o.Sleep == nil {
		o.Sleep = time.Sleep
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.OpenBrowser == nil {
		o.OpenBrowser = BestEffortOpenBrowser
	}
	if o.Out == nil {
		o.Out = io.Discard
	}
	return *o
}

// AuthorizeURL builds the consent page URL: base + /cli/authorize with the
// public key and the suggested device name. The page derives the device id and
// fingerprint itself and trusts no other parameter.
func AuthorizeURL(base string, key *Key, name string) string {
	base = trimTrailingSlash(base)
	q := url.Values{}
	q.Set("pk", key.PublicB64())
	q.Set("name", name)
	return base + "/cli/authorize?" + q.Encode()
}

func trimTrailingSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

// Login enrolls (or re-approves) the device: it prints the consent URL and the
// fingerprint, opens the browser, and polls the refresh endpoint until the
// operator approves. A 404 means the approval has not landed yet, so it keeps
// polling; the first token that comes back is the result.
//
// Enrollment needs no localhost listener — the CLI only makes outbound
// requests — so it works over SSH: the operator opens the printed URL on
// whatever machine has a browser.
func Login(client *Client, key *Key, opts LoginOptions) (*Token, error) {
	opts = opts.withDefaults()

	authURL := AuthorizeURL(opts.AuthorizeBaseURL, key, opts.DeviceName)
	// Explicit discards: these write progress to the operator's terminal, and a
	// failure (a closed pipe) is not a reason to abandon an enrollment already
	// in flight.
	_, _ = fmt.Fprintf(opts.Out, "\nTo approve this device, open:\n\n  %s\n\n", authURL)
	_, _ = fmt.Fprintf(opts.Out, "Device fingerprint: %s\n", key.Fingerprint())
	_, _ = fmt.Fprintf(opts.Out, "Confirm this matches the fingerprint shown in your browser.\n\n")

	if err := opts.OpenBrowser(authURL); err != nil {
		_, _ = fmt.Fprintf(opts.Out, "Could not open a browser automatically (%v).\nOpen the URL above on any machine with a browser.\n", err)
	}
	fmt.Fprintf(opts.Out, "Waiting for approval...\n")

	deadline := opts.Now().Add(opts.Timeout)
	for {
		tok, err := client.Refresh(key, opts.Now())
		switch {
		case err == nil:
			return tok, nil
		case errors.Is(err, ErrDeviceUnknown):
			// Not approved yet. Keep waiting.
		case errors.Is(err, ErrRateLimited):
			// A parallel helper refreshed first; wait out the throttle and
			// try again.
			opts.Sleep(time.Second)
			continue
		default:
			return nil, loginError(err)
		}

		if !opts.Now().Before(deadline) {
			return nil, fmt.Errorf("timed out waiting for approval; run prizmal login again to retry")
		}
		opts.Sleep(opts.Interval)
	}
}

// loginError turns a refresh failure during enrollment into a message with the
// right next step. Every 401 body the switch sends is the same, so the message
// never claims to know which check failed.
func loginError(err error) error {
	switch {
	case errors.Is(err, ErrReauthRequired):
		return fmt.Errorf("this device's sign-in expired; run prizmal login to approve it again")
	case errors.Is(err, ErrNotAuthorized):
		return fmt.Errorf("device not authorized; ask a tenant manager to approve this device, or run prizmal login again")
	case errors.Is(err, ErrUnavailable):
		return fmt.Errorf("the token service is unavailable; try prizmal login again shortly")
	default:
		return err
	}
}
