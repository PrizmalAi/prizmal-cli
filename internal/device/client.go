package device

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The refresh endpoint, appended to the configured Switch host.
const refreshPath = "/v1/cli/token"

// refreshTimeout bounds one refresh round trip. It is short because the
// helper runs under Claude Code's TTL: a hung switch must not stall the
// session longer than the token still has left.
const refreshTimeout = 15 * time.Second

// ErrDeviceUnknown means the switch answered 404: the device_id is not
// enrolled. During enrollment that is "still pending", which the login loop
// keeps polling; at refresh it means the enrollment is gone and the operator
// must run prizmal login.
var ErrDeviceUnknown = errors.New("device not enrolled")

// ErrNotAuthorized means the switch answered 401 with no more specific code:
// a bad signature, a stale ts, a revoked device, an inactive member, an
// inactive WorkOS membership, or a disabled tenant. The switch says nothing
// about which, and neither does this.
var ErrNotAuthorized = errors.New("device not authorized")

// ErrReauthRequired means the switch answered 401 REAUTH_REQUIRED: the
// device's sign-in deadline passed. Its fix differs from every other 401's —
// the developer signs in again in the browser, which re-approves the same
// device key — so it is a distinct error the CLI maps to "run prizmal login".
var ErrReauthRequired = errors.New("device sign-in deadline passed")

// ErrRateLimited means the switch answered 429: a refresh arrived within 30
// seconds of the previous one. The remedy is to wait one second and re-read
// the cache, which a parallel helper has just written.
var ErrRateLimited = errors.New("device refresh too soon")

// ErrUnavailable means the switch (or a dependency behind it) answered 503:
// no verdict, retryable. The CLI prints a still-valid cached token rather than
// failing.
var ErrUnavailable = errors.New("device token service unavailable")

// Client issues device tokens against one Switch host.
type Client struct {
	BaseURL string
	Env     string // the environment tag (d|s|p) the signature binds
	HTTP    *http.Client
}

// NewClient builds a Client for baseURL, deriving the env tag from the host.
func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Env:     EnvTag(baseURL),
		HTTP:    &http.Client{Timeout: refreshTimeout},
	}
}

// EnvTag names the environment a host belongs to, the tag the refresh
// signature binds and the device token carries: p for the production API, s
// for staging, d otherwise (local development included).
func EnvTag(baseURL string) string {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "d"
	}
	switch strings.ToLower(u.Hostname()) {
	case "api.prizmal.ai":
		return "p"
	case "api.staging.prizmal.ai":
		return "s"
	default:
		return "d"
	}
}

// Token is a successful refresh: the device token, and the deadline the
// server reported when the member's sign-in is not managed by Directory Sync.
type Token struct {
	Value     string
	ExpiresIn time.Duration
	Expiry    time.Time
	ReauthBy  *time.Time
}

// refreshResponse is the 200 body: device_token, expires_in, and reauth_by
// when a deadline applies.
type refreshResponse struct {
	DeviceToken string    `json:"device_token"`
	ExpiresIn   int       `json:"expires_in"`
	ReauthBy    time.Time `json:"reauth_by"`
}

// errorEnvelope is the structured error body every route answers with:
// {"error":{"code":"…","message":"…"}}.
type errorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Refresh proves the device key and exchanges it for a device token. now is
// the timestamp the signature covers, formatted RFC3339.
func (c *Client) Refresh(key *Key, now time.Time) (*Token, error) {
	ts := now.UTC().Format(time.RFC3339)
	body := map[string]string{
		"device_id": key.ID(),
		"ts":        ts,
		"sig":       key.SignRefresh(c.Env, ts),
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, c.BaseURL+refreshPath, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: refreshTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("refresh device token: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, fmt.Errorf("refresh device token: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
		var rr refreshResponse
		if err := json.Unmarshal(data, &rr); err != nil {
			return nil, fmt.Errorf("refresh device token: %w", err)
		}
		if rr.DeviceToken == "" {
			return nil, fmt.Errorf("refresh device token: switch returned no token")
		}
		tok := &Token{
			Value:     rr.DeviceToken,
			ExpiresIn: time.Duration(rr.ExpiresIn) * time.Second,
			Expiry:    now.Add(time.Duration(rr.ExpiresIn) * time.Second),
		}
		if !rr.ReauthBy.IsZero() {
			deadline := rr.ReauthBy
			tok.ReauthBy = &deadline
		}
		return tok, nil

	case http.StatusNotFound:
		return nil, ErrDeviceUnknown

	case http.StatusTooManyRequests:
		return nil, ErrRateLimited

	case http.StatusServiceUnavailable:
		return nil, ErrUnavailable

	case http.StatusUnauthorized:
		if codeOf(data) == "REAUTH_REQUIRED" {
			return nil, ErrReauthRequired
		}
		return nil, ErrNotAuthorized

	default:
		return nil, fmt.Errorf("refresh device token: switch answered %s", resp.Status)
	}
}

// codeOf reads the error code out of an error envelope, empty when the body is
// not one.
func codeOf(data []byte) string {
	var env errorEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return ""
	}
	return env.Error.Code
}
