package device

import (
	"errors"
	"time"
)

// CacheValidMargin is how much of a cached token's life must remain for the
// helper to reuse it instead of refreshing. A token with less than this left
// is refreshed, so Claude Code never gets a credential about to expire.
const CacheValidMargin = 5 * time.Minute

// MaxHelperOutput is the most Claude Code accepts from an apiKeyHelper
// command: 16384 printable ASCII characters. A longer or non-printable value
// is refused, so the CLI validates before printing.
const MaxHelperOutput = 16384

// HelperToken returns the credential `prizmal auth token` prints, and the
// cache to persist.
//
// A cached token with at least CacheValidMargin left is reused. Otherwise the
// helper proves the device key and refreshes. Two failures have a fallback:
// a 429 means a parallel helper refreshed a moment ago, so it waits and
// re-reads the cache that helper wrote; a 503 means the service has no verdict,
// so a cached token that has not expired is printed rather than broken. Every
// other failure is returned and the caller exits 1 without printing.
//
// load reads the token cache (nil when there is none), and sleep waits out the
// throttle. Both are seams so the helper can be tested without a file or a
// real delay.
func HelperToken(client *Client, key *Key, now time.Time, load func() *CachedToken, sleep func(time.Duration)) (string, *CachedToken, error) {
	cached := load()
	if cached != nil && !cached.Expiry.Before(now.Add(CacheValidMargin)) {
		return cached.Token, cached, nil
	}

	tok, err := client.Refresh(key, now)
	switch {
	case err == nil:
		ct := &CachedToken{Token: tok.Value, Expiry: tok.Expiry, ReauthBy: tok.ReauthBy}
		return ct.Token, ct, nil

	case errors.Is(err, ErrRateLimited):
		// A parallel helper won the 30-second throttle. Wait it out and
		// re-read the cache; that helper's token is the one to print.
		if sleep != nil {
			sleep(time.Second)
		}
		if again := load(); again != nil && now.Before(again.Expiry) {
			return again.Token, again, nil
		}
		return "", cached, err

	case errors.Is(err, ErrUnavailable):
		if cached != nil && now.Before(cached.Expiry) {
			return cached.Token, cached, nil
		}
		return "", cached, err

	default:
		return "", cached, err
	}
}

// ValidHelperOutput reports whether a credential may be printed for Claude
// Code: non-empty, no longer than MaxHelperOutput bytes, and printable ASCII.
func ValidHelperOutput(s string) bool {
	if s == "" || len(s) > MaxHelperOutput {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}
