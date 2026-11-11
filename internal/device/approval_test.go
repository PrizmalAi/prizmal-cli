package device

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The Switch reports where a person approves a device. The request is a plain
// GET with no credential: the CLI asks before it holds one.
func TestApprovalURLRequestsTheConfigRouteWithoutACredential(t *testing.T) {
	var method, path, auth, apiKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		auth, apiKey = r.Header.Get("Authorization"), r.Header.Get("x-api-key")
		_, _ = w.Write([]byte(`{"approval_url":"https://app.example.invalid/cli/authorize"}`))
	}))
	defer srv.Close()

	got, err := NewClient(srv.URL).ApprovalURL()
	if err != nil || got != "https://app.example.invalid/cli/authorize" {
		t.Fatalf("ApprovalURL = %q, %v", got, err)
	}
	if method != http.MethodGet || path != "/v1/cli/config" {
		t.Fatalf("request = %s %s, want GET /v1/cli/config", method, path)
	}
	if auth != "" || apiKey != "" {
		t.Fatalf("the request carried a credential: Authorization=%q x-api-key=%q", auth, apiKey)
	}
}

// A Switch that reports no usable approval page is ErrNoApprovalPage, whether
// it lacks the route (404, or 401 from a Switch that predates it) or answers
// without a URL. A Switch that fails is a different error.
func TestApprovalURLClassifiesTheAnswer(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body       string
		wantNoPage bool
	}{
		{"not found", http.StatusNotFound, `{}`, true},
		{"key required", http.StatusUnauthorized, `{}`, true},
		{"empty field", http.StatusOK, `{"approval_url":""}`, true},
		{"missing field", http.StatusOK, `{}`, true},
		{"not json", http.StatusOK, `<html>`, true},
		{"relative", http.StatusOK, `{"approval_url":"/cli/authorize"}`, true},
		{"wrong scheme", http.StatusOK, `{"approval_url":"ftp://app.example.invalid/x"}`, true},
		{"server error", http.StatusInternalServerError, `{}`, false},
		{"unavailable", http.StatusServiceUnavailable, `{}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			_, err := NewClient(srv.URL).ApprovalURL()
			if err == nil {
				t.Fatal("ApprovalURL returned no error")
			}
			if got := errors.Is(err, ErrNoApprovalPage); got != tc.wantNoPage {
				t.Fatalf("errors.Is(err, ErrNoApprovalPage) = %v, want %v (err: %v)", got, tc.wantNoPage, err)
			}
		})
	}
}
