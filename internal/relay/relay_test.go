package relay

import (
	"bufio"
	"context"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRelayRequiresPlaceholderAndForwardsModels(t *testing.T) {
	var calls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls++
		if req.Method != http.MethodGet || req.URL.Path != "/v1/models" || req.URL.RawQuery != "view=compact" {
			t.Errorf("upstream request = %s %s?%s", req.Method, req.URL.Path, req.URL.RawQuery)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer pz-s-dt-fresh" {
			t.Errorf("upstream Authorization = %q, want the source token", got)
		}
		if got := req.Header.Get("X-Relay-Probe"); got != "forwarded" {
			t.Errorf("upstream X-Relay-Probe = %q, want forwarded", got)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"probe-model"}]}`))
	}))
	defer upstream.Close()

	r, err := New(upstream.URL, func(context.Context) (string, error) {
		return "pz-s-dt-fresh", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	request := func(auth string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, r.URL()+"/models?view=compact", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", auth)
		req.Header.Set("X-Relay-Probe", "forwarded")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	wrong := request("Bearer wrong")
	_ = wrong.Body.Close()
	if wrong.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong placeholder status = %d, want 401", wrong.StatusCode)
	}
	if calls != 0 {
		t.Fatalf("wrong placeholder reached upstream %d times", calls)
	}

	resp := request("Bearer " + r.Placeholder())
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "probe-model") {
		t.Fatalf("models response = %q", body)
	}
	if calls != 1 {
		t.Fatalf("upstream calls = %d, want 1", calls)
	}
}

func TestRelayPlaceholderIs256Bits(t *testing.T) {
	r, err := New("https://switch.example.test", func(context.Context) (string, error) {
		return "token", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	raw, err := hex.DecodeString(r.Placeholder())
	if err != nil {
		t.Fatalf("placeholder is not hexadecimal: %v", err)
	}
	if len(raw) != 32 {
		t.Fatalf("placeholder carries %d bits, want 256", len(raw)*8)
	}
}

func TestRelayStreamsBeforeUpstreamFinishes(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseUpstream := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseUpstream)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-release
		_, _ = io.WriteString(w, "data: second\n\n")
	}))
	defer upstream.Close()

	r, err := New(upstream.URL, func(context.Context) (string, error) {
		return "pz-s-dt-stream", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	req, err := http.NewRequest(http.MethodPost, r.URL()+"/chat/completions", strings.NewReader(`{"stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+r.Placeholder())
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	first := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(resp.Body).ReadString('\n')
		first <- line
	}()
	select {
	case line := <-first:
		if line != "data: first\n" {
			t.Fatalf("first streamed line = %q", line)
		}
	case <-time.After(2 * time.Second):
		releaseUpstream()
		t.Fatal("the first SSE chunk did not reach the client before the upstream finished")
	}

	releaseUpstream()
	rest, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rest), "data: second") {
		t.Fatalf("remaining stream = %q", rest)
	}
}

func TestRelayCallsTokenSourceForEveryRequest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = io.WriteString(w, req.Header.Get("Authorization"))
	}))
	defer upstream.Close()

	var calls int
	r, err := New(upstream.URL, func(context.Context) (string, error) {
		calls++
		return "token-for-every-request", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	for range 2 {
		req, err := http.NewRequest(http.MethodGet, r.URL()+"/models", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+r.Placeholder())
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	if calls != 2 {
		t.Fatalf("token source calls = %d, want one per request", calls)
	}
}

func TestRelayRefreshesOnceAndReplaysAfter401(t *testing.T) {
	var mu sync.Mutex
	var authorizations []string
	var bodies []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		mu.Lock()
		authorizations = append(authorizations, req.Header.Get("Authorization"))
		bodies = append(bodies, string(body))
		attempt := len(authorizations)
		mu.Unlock()
		if attempt == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"expired"}`)
			return
		}
		_, _ = io.WriteString(w, "replayed")
	}))
	defer upstream.Close()

	var refreshes []bool
	r, err := New(upstream.URL, func(ctx context.Context) (string, error) {
		refresh := IsRefresh(ctx)
		refreshes = append(refreshes, refresh)
		if refresh {
			return "pz-s-dt-new", nil
		}
		return "pz-s-dt-old", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	req, err := http.NewRequest(http.MethodPost, r.URL()+"/chat/completions?trace=one", strings.NewReader(`{"probe":"body"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+r.Placeholder())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "replayed" {
		t.Fatalf("response = %d %q, want 200 replayed", resp.StatusCode, body)
	}
	if len(refreshes) != 2 || refreshes[0] || !refreshes[1] {
		t.Fatalf("token refresh calls = %v, want false then true", refreshes)
	}
	mu.Lock()
	defer mu.Unlock()
	wantAuth := []string{"Bearer pz-s-dt-old", "Bearer pz-s-dt-new"}
	if strings.Join(authorizations, "\n") != strings.Join(wantAuth, "\n") {
		t.Fatalf("upstream authorizations = %v, want %v", authorizations, wantAuth)
	}
	if len(bodies) != 2 || bodies[0] != `{"probe":"body"}` || bodies[1] != bodies[0] {
		t.Fatalf("replayed bodies = %v", bodies)
	}
}

func TestRelayReturns401WhenTokenSourceHasNothingToRefresh(t *testing.T) {
	var calls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "expired")
	}))
	defer upstream.Close()

	r, err := New(upstream.URL, func(context.Context) (string, error) {
		calls++
		return "static-key", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	req, err := http.NewRequest(http.MethodGet, r.URL()+"/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+r.Placeholder())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want the upstream 401", resp.StatusCode)
	}
	if calls != 2 {
		t.Fatalf("token source calls = %d, want an initial call and one refresh attempt", calls)
	}
}
