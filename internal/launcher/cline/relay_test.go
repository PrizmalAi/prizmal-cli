package cline

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PrizmalAi/prizmal-cli/internal/device"
	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
)

func TestClineSupportsDeviceMode(t *testing.T) {
	if !(&Cline{}).SupportsDeviceMode() {
		t.Fatal("Cline does not support device mode")
	}
}

func TestClineRelayRequiresPlaceholderAndForwardsModels(t *testing.T) {
	var calls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls++
		if req.Method != http.MethodGet || req.URL.Path != "/v1/models" || req.URL.RawQuery != "view=compact" {
			t.Errorf("upstream request = %s %s?%s", req.Method, req.URL.Path, req.URL.RawQuery)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer pz-s-dt-fresh" {
			t.Errorf("upstream Authorization = %q, want the device token", got)
		}
		if got := req.Header.Get("X-Cline-Probe"); got != "forwarded" {
			t.Errorf("upstream X-Cline-Probe = %q, want forwarded", got)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"probe-model"}]}`))
	}))
	defer upstream.Close()

	relay, err := startClineRelay(upstream.URL, func(refresh bool) (string, error) {
		if refresh {
			t.Error("a 200 response triggered a token refresh")
		}
		return "pz-s-dt-fresh", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	request := func(auth string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, relay.providerBaseURL()+"/models?view=compact", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", auth)
		req.Header.Set("X-Cline-Probe", "forwarded")
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

	resp := request("Bearer " + relay.placeholder)
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

func TestClineRelayStreamsBeforeUpstreamFinishes(t *testing.T) {
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

	relay, err := startClineRelay(upstream.URL, func(bool) (string, error) {
		return "pz-s-dt-stream", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	req, err := http.NewRequest(http.MethodPost, relay.providerBaseURL()+"/chat/completions", strings.NewReader(`{"stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+relay.placeholder)
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

func TestClineRelayRefreshesOnceAndReplaysAfter401(t *testing.T) {
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
	relay, err := startClineRelay(upstream.URL, func(refresh bool) (string, error) {
		refreshes = append(refreshes, refresh)
		if refresh {
			return "pz-s-dt-new", nil
		}
		return "pz-s-dt-old", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	req, err := http.NewRequest(http.MethodPost, relay.providerBaseURL()+"/chat/completions?trace=one", strings.NewReader(`{"probe":"body"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+relay.placeholder)
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

func TestClineRelayGetsADeviceTokenForEveryRequest(t *testing.T) {
	internaltest.SandboxedHome(t)
	if _, _, err := device.LoadOrCreateKey(); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var refreshes int
	var apiAuthorizations []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/v1/cli/token":
			mu.Lock()
			refreshes++
			attempt := refreshes
			mu.Unlock()
			token := "pz-s-dt-old"
			if attempt == 2 {
				token = "pz-s-dt-new"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_token": token,
				"expires_in":   600,
			})
		case "/v1/models":
			auth := req.Header.Get("Authorization")
			mu.Lock()
			apiAuthorizations = append(apiAuthorizations, auth)
			attempt := len(apiAuthorizations)
			mu.Unlock()
			if attempt == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = io.WriteString(w, `{"data":[]}`)
		default:
			http.NotFound(w, req)
		}
	}))
	defer upstream.Close()
	envconfig.SetBaseURL(upstream.URL)
	t.Cleanup(func() { envconfig.SetBaseURL("") })

	relay, err := newDeviceClineRelay()
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	requestModels := func() {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, relay.providerBaseURL()+"/models", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+relay.placeholder)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("models status = %d, want 200", resp.StatusCode)
		}
	}

	requestModels()
	requestModels()
	mu.Lock()
	defer mu.Unlock()
	if refreshes != 2 {
		t.Fatalf("device refreshes = %d, want one initial token and one forced refresh", refreshes)
	}
	want := []string{"Bearer pz-s-dt-old", "Bearer pz-s-dt-new", "Bearer pz-s-dt-new"}
	if strings.Join(apiAuthorizations, "\n") != strings.Join(want, "\n") {
		t.Fatalf("models authorizations = %v, want %v", apiAuthorizations, want)
	}
}
