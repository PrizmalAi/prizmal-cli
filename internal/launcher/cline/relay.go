package cline

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/PrizmalAi/prizmal-cli/internal/device"
	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
)

// clineRelay is the loopback credential adapter for Cline. Cline reads
// OPENAI_API_KEY once, so the child gets a per-launch placeholder. The relay
// replaces that placeholder with a device token for every request, including
// one refresh and replay after an upstream 401.
type clineRelay struct {
	server      *http.Server
	listener    net.Listener
	placeholder string
	upstream    *url.URL
	client      *http.Client
	token       func(refresh bool) (string, error)
}

func newDeviceClineRelay() (*clineRelay, error) {
	key, err := device.LoadKey()
	if err != nil {
		return nil, err
	}
	client := device.NewClient(envconfig.BaseURL())
	var tokenMu sync.Mutex
	token := func(refresh bool) (string, error) {
		// Cline can send parallel requests. Serialize cache reads, refreshes,
		// and writes so the relay's own helpers do not race the cache file.
		tokenMu.Lock()
		defer tokenMu.Unlock()

		loads := 0
		load := func() *device.CachedToken {
			loads++
			// HelperToken checks the cache before refreshing. A 401 means the
			// cached bearer was refused, so the retry starts without it. On a
			// 429, HelperToken calls load again and can reuse the token a
			// parallel helper just saved.
			if refresh && loads == 1 {
				return nil
			}
			cached, err := device.LoadCachedToken()
			if err != nil {
				return nil
			}
			return cached
		}
		value, cached, err := device.HelperToken(client, key, time.Now(), load, time.Sleep)
		if err != nil {
			return "", err
		}
		if cached != nil {
			if err := cached.Save(); err != nil {
				return "", err
			}
		}
		return value, nil
	}
	return startClineRelay(envconfig.UnversionedBaseURL(), token)
}

func startClineRelay(upstream string, token func(bool) (string, error)) (*clineRelay, error) {
	target, err := url.Parse(upstream)
	if err != nil || target.Scheme == "" || target.Host == "" {
		return nil, fmt.Errorf("invalid Switch URL %q", upstream)
	}
	placeholder, err := newClineRelayPlaceholder()
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	r := &clineRelay{
		listener:    listener,
		placeholder: placeholder,
		upstream:    target,
		client:      &http.Client{Transport: http.DefaultTransport},
		token:       token,
	}
	r.server = &http.Server{
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() { _ = r.server.Serve(listener) }()
	return r, nil
}

func newClineRelayPlaceholder() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate Cline relay placeholder: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

func (r *clineRelay) providerBaseURL() string {
	return "http://" + r.listener.Addr().String() + "/v1"
}

func (r *clineRelay) providerHost() string {
	return "http://" + r.listener.Addr().String()
}

func (r *clineRelay) Close() {
	_ = r.server.Close()
}

func (r *clineRelay) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	want := "Bearer " + r.placeholder
	got := req.Header.Get("Authorization")
	if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, "read request body", http.StatusBadRequest)
		return
	}
	_ = req.Body.Close()

	for attempt := 0; attempt < 2; attempt++ {
		token, err := r.token(attempt == 1)
		if err != nil {
			http.Error(w, "device credential unavailable", http.StatusBadGateway)
			return
		}
		resp, err := r.forward(req, body, token)
		if err != nil {
			http.Error(w, "Switch request failed", http.StatusBadGateway)
			return
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			_ = resp.Body.Close()
			continue
		}
		defer func() { _ = resp.Body.Close() }()
		copyClineRelayResponse(w, resp)
		return
	}
}

func (r *clineRelay) forward(req *http.Request, body []byte, token string) (*http.Response, error) {
	target := *r.upstream
	target.Path = joinClineRelayPath(r.upstream.Path, req.URL.Path)
	target.RawPath = ""
	target.RawQuery = req.URL.RawQuery

	out, err := http.NewRequestWithContext(req.Context(), req.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	out.Header = req.Header.Clone()
	removeClineRelayHopHeaders(out.Header)
	out.Header.Set("Authorization", "Bearer "+token)
	out.ContentLength = int64(len(body))
	if len(body) == 0 {
		out.Body = http.NoBody
	}
	return r.client.Do(out)
}

func joinClineRelayPath(base, request string) string {
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

var clineRelayHopHeaders = []string{
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

func removeClineRelayHopHeaders(headers http.Header) {
	for _, value := range headers.Values("Connection") {
		for _, name := range strings.Split(value, ",") {
			headers.Del(strings.TrimSpace(name))
		}
	}
	for _, name := range clineRelayHopHeaders {
		headers.Del(name)
	}
}

func copyClineRelayResponse(w http.ResponseWriter, resp *http.Response) {
	headers := w.Header()
	for name, values := range resp.Header {
		for _, value := range values {
			headers.Add(name, value)
		}
	}
	removeClineRelayHopHeaders(headers)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(clineRelayFlushWriter{w: w}, resp.Body)
}

// clineRelayFlushWriter forwards each response chunk as it arrives. This keeps
// SSE tokens visible before the upstream closes the response.
type clineRelayFlushWriter struct {
	w http.ResponseWriter
}

func (w clineRelayFlushWriter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	if flusher, ok := w.w.(http.Flusher); ok {
		flusher.Flush()
	}
	return n, err
}
