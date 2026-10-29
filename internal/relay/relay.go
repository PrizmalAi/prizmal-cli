// Package relay runs a loopback HTTP relay between a harness and the Switch.
// The harness receives a per-launch placeholder credential; the relay replaces
// it with the launch's real credential for every upstream request.
package relay

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// TokenSource returns the credential the relay sends upstream. It is called
// for every request. After an upstream 401, the relay calls it once more with
// IsRefresh(ctx) true and replays the request only when the source returns a
// different token.
type TokenSource func(context.Context) (string, error)

type refreshContextKey struct{}

// IsRefresh reports whether the relay is asking the token source for a
// replacement after an upstream 401.
func IsRefresh(ctx context.Context) bool {
	refresh, _ := ctx.Value(refreshContextKey{}).(bool)
	return refresh
}

// Relay accepts only its placeholder, as a bearer or an x-api-key, apart from an opted-in model list, and forwards requests to the
// upstream Switch with the launch's real credential.
type Relay struct {
	server      *http.Server
	listener    net.Listener
	placeholder string
	upstream    *url.URL
	client      *http.Client
	token       TokenSource
	openModels  bool
}

// Option changes how a relay treats its callers.
type Option func(*Relay)

// WithUnauthenticatedModelList lets exactly GET /v1/models through without the
// placeholder bearer; the relay still forwards it with the launch's real
// credential. Cline's model picker sends that request with no Authorization
// header, because Cline attaches one only from a key stored in its provider
// settings, and a launch keeps the key off disk. Every other path, and every
// other method on that path, still needs the placeholder.
func WithUnauthenticatedModelList() Option {
	return func(r *Relay) { r.openModels = true }
}

// New starts a relay on an ephemeral 127.0.0.1 port. upstream is the Switch
// base without a trailing /v1; URL adds that version for OpenAI-shaped
// harnesses.
func New(upstream string, token TokenSource, opts ...Option) (*Relay, error) {
	if token == nil {
		return nil, fmt.Errorf("relay token source is required")
	}
	target, err := url.Parse(upstream)
	if err != nil || target.Scheme == "" || target.Host == "" {
		return nil, fmt.Errorf("invalid Switch URL %q", upstream)
	}
	placeholder, err := newPlaceholder()
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	r := &Relay{
		listener:    listener,
		placeholder: placeholder,
		upstream:    target,
		client:      &http.Client{Transport: http.DefaultTransport},
		token:       token,
	}
	for _, opt := range opts {
		opt(r)
	}
	r.server = &http.Server{
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() { _ = r.server.Serve(listener) }()
	return r, nil
}

func newPlaceholder() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate relay placeholder: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// URL is the OpenAI-shaped relay endpoint.
func (r *Relay) URL() string {
	return "http://" + r.listener.Addr().String() + "/v1"
}

// Placeholder is the per-launch bearer the harness must send.
func (r *Relay) Placeholder() string {
	return r.placeholder
}

// Close stops the loopback server.
func (r *Relay) Close() {
	_ = r.server.Close()
}

func (r *Relay) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	openModelList := r.openModels && req.Method == http.MethodGet && req.URL.Path == "/v1/models"
	if !openModelList && !r.hasPlaceholder(req.Header) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, "read request body", http.StatusBadRequest)
		return
	}
	_ = req.Body.Close()

	token, err := r.token(req.Context())
	if err != nil {
		http.Error(w, "credential unavailable", http.StatusBadGateway)
		return
	}
	resp, err := r.forward(req, body, token)
	if err != nil {
		http.Error(w, "Switch request failed", http.StatusBadGateway)
		return
	}
	if resp.StatusCode != http.StatusUnauthorized {
		defer func() { _ = resp.Body.Close() }()
		copyRelayResponse(w, resp)
		return
	}

	refreshCtx := context.WithValue(req.Context(), refreshContextKey{}, true)
	refreshed, err := r.token(refreshCtx)
	if err != nil {
		_ = resp.Body.Close()
		http.Error(w, "credential refresh failed", http.StatusBadGateway)
		return
	}
	if refreshed == token {
		defer func() { _ = resp.Body.Close() }()
		copyRelayResponse(w, resp)
		return
	}

	_ = resp.Body.Close()
	replayed, err := r.forward(req, body, refreshed)
	if err != nil {
		http.Error(w, "Switch request failed", http.StatusBadGateway)
		return
	}
	defer func() { _ = replayed.Body.Close() }()
	copyRelayResponse(w, replayed)
}

// hasPlaceholder reports whether the request carries the placeholder in
// Authorization: Bearer (OpenAI-shaped clients) or x-api-key (Anthropic-shaped
// clients). A request needs at least one of them, and every one it sends must
// match, so a wrong credential in either header is refused.
func (r *Relay) hasPlaceholder(headers http.Header) bool {
	matches := func(got, want string) bool {
		return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
	}
	_, hasAuth := headers["Authorization"]
	_, hasKey := headers["X-Api-Key"]
	if !hasAuth && !hasKey {
		return false
	}
	if hasAuth && !matches(headers.Get("Authorization"), "Bearer "+r.placeholder) {
		return false
	}
	if hasKey && !matches(headers.Get("X-Api-Key"), r.placeholder) {
		return false
	}
	return true
}

func (r *Relay) forward(req *http.Request, body []byte, token string) (*http.Response, error) {
	target := *r.upstream
	target.Path = joinPath(r.upstream.Path, req.URL.Path)
	target.RawPath = ""
	target.RawQuery = req.URL.RawQuery

	out, err := http.NewRequestWithContext(req.Context(), req.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	out.Header = req.Header.Clone()
	removeHopHeaders(out.Header)
	out.Header.Set("Authorization", "Bearer "+token)
	out.Header.Del("X-Api-Key")
	out.ContentLength = int64(len(body))
	if len(body) == 0 {
		out.Body = http.NoBody
	}
	return r.client.Do(out)
}

func joinPath(base, request string) string {
	switch {
	case base == "":
		return request
	case request == "":
		return base
	case strings.HasSuffix(base, "/") && strings.HasPrefix(request, "/"):
		return base + request[1:]
	case !strings.HasSuffix(base, "/") && !strings.HasPrefix(request, "/"):
		return base + "/" + request
	default:
		return base + request
	}
}

var hopHeaders = []string{
	"Connection",
	"Proxy-Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

func removeHopHeaders(headers http.Header) {
	for _, value := range headers.Values("Connection") {
		for _, name := range strings.Split(value, ",") {
			headers.Del(strings.TrimSpace(name))
		}
	}
	for _, name := range hopHeaders {
		headers.Del(name)
	}
}

func copyRelayResponse(w http.ResponseWriter, resp *http.Response) {
	headers := w.Header()
	for name, values := range resp.Header {
		for _, value := range values {
			headers.Add(name, value)
		}
	}
	removeHopHeaders(headers)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(relayFlushWriter{w: w}, resp.Body)
}

// relayFlushWriter forwards each response chunk as it arrives. This keeps
// SSE tokens visible before the upstream closes the response.
type relayFlushWriter struct {
	w http.ResponseWriter
}

func (w relayFlushWriter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	if flusher, ok := w.w.(http.Flusher); ok {
		flusher.Flush()
	}
	return n, err
}
