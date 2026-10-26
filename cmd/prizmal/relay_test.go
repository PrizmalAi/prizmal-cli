package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/device"
	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
	"github.com/PrizmalAi/prizmal-cli/internal/relay"
)

func TestDeviceRelayTokenSourceRefreshesAndReplays(t *testing.T) {
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
	source, err := launchTokenSource(true, "")
	if err != nil {
		t.Fatal(err)
	}
	r, err := relay.New(envconfig.UnversionedBaseURL(), source)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	requestModels := func() {
		t.Helper()
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

func TestShouldUseRelay(t *testing.T) {
	originalFlag := relayFlag
	originalDeviceMode := envconfig.DeviceMode()
	t.Cleanup(func() {
		relayFlag = originalFlag
		envconfig.SetDeviceMode(originalDeviceMode)
	})

	for _, tc := range []struct {
		name        string
		flag        bool
		deviceMode  bool
		integration string
		want        bool
	}{
		{name: "flag any harness", flag: true, integration: "claude", want: true},
		{name: "Cline device login without flag", deviceMode: true, integration: "cline", want: true},
		{name: "other harness device login without flag", deviceMode: true, integration: "codex", want: false},
		{name: "Cline switch key without flag", integration: "cline", want: true},
		{name: "other harness switch key without flag", integration: "codex", want: false},
		{name: "nothing selected", integration: "pi", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			relayFlag = tc.flag
			envconfig.SetDeviceMode(tc.deviceMode)
			if got := shouldUseRelay(tc.integration); got != tc.want {
				t.Fatalf("shouldUseRelay(%q) = %v, want %v", tc.integration, got, tc.want)
			}
		})
	}
}
