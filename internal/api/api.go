// Package api is a minimal stub client. The launcher only used the client
// for best-effort niceties (context-window lookup, model-existence pings);
// every call site degrades gracefully on error. Since prizmal targets
// arbitrary providers, the client always reports failure so callers take
// their fallback paths.
package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
)

// ErrUnavailable is returned by every client operation.
var ErrUnavailable = errors.New("prizmal: provider does not expose the requested API")

// Client is a stub API client.
type Client struct {
	base *url.URL
	http *http.Client
}

// NewClient returns a Client for the given base URL.
func NewClient(base *url.URL, h *http.Client) *Client {
	if h == nil {
		h = http.DefaultClient
	}
	return &Client{base: base, http: h}
}

// ClientFromEnvironment returns a Client built from the configured base URL.
func ClientFromEnvironment() (*Client, error) {
	return NewClient(baseFromEnv(), http.DefaultClient), nil
}

func baseFromEnv() *url.URL {
	if v := strings.TrimSpace(os.Getenv("PRIZMAL_SWITCH_URL")); v != "" {
		if u, err := url.Parse(v); err == nil && u.Scheme != "" && u.Host != "" {
			return u
		}
	}
	u, _ := url.Parse(envconfig.DefaultURL)
	return u
}

// ShowRequest and ShowResponse mirror the subset of the API types
// referenced by the adapters.
type ShowRequest struct{ Model string }

type ShowResponse struct {
	ModelInfo     map[string]any
	Capabilities  []string
	ContextLength int
}

// Show always fails with ErrUnavailable.
func (c *Client) Show(ctx context.Context, req *ShowRequest) (*ShowResponse, error) {
	return nil, ErrUnavailable
}

// ListHelm is unused; List mirrors the tag-list call and always fails.
func (c *Client) List(ctx context.Context) (*ListResponse, error) {
	return nil, ErrUnavailable
}

// ListResponse mirrors the subset of the API types referenced by the adapters.
type ListResponse struct {
	Models []ListModel
}

// ListModel mirrors a single entry of the tag list.
type ListModel struct {
	Name string
}

// GenerateRequest and GenerateResponse mirror the subset of the API
// types referenced by the adapters.
type GenerateRequest struct{ Model string }

type GenerateResponse struct{}

// Generate always fails with ErrUnavailable.
func (c *Client) Generate(ctx context.Context, req *GenerateRequest, fn func(GenerateResponse) error) error {
	return ErrUnavailable
}
