package stubserver

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

// postWithAuth sends a POST request with the stub auth header.
func postWithAuth(t *testing.T, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+StubKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestChatCompletionsReturns200(t *testing.T) {
	srv := New()
	defer srv.Close()

	resp := postWithAuth(t, srv.URL+"/v1/chat/completions", `{"model":"prizmal/stub","messages":[{"role":"user","content":"hi"}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	_ = resp.Body.Close()
	if model, _ := body["model"].(string); model != "prizmal/stub" {
		t.Fatalf("model = %q, want prizmal/stub", model)
	}
	choices, _ := body["choices"].([]any)
	if len(choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(choices))
	}
}

func TestResponsesReturns200(t *testing.T) {
	srv := New()
	defer srv.Close()

	resp := postWithAuth(t, srv.URL+"/v1/responses", `{"model":"prizmal/stub","input":"hi"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	_ = resp.Body.Close()
	if status, _ := body["status"].(string); status != "completed" {
		t.Fatalf("status = %q, want completed", status)
	}
}

func TestMessagesReturns200(t *testing.T) {
	srv := New()
	defer srv.Close()

	resp := postWithAuth(t, srv.URL+"/v1/messages", `{"model":"prizmal/stub","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	_ = resp.Body.Close()
	if msgType, _ := body["type"].(string); msgType != "message" {
		t.Fatalf("type = %q, want message", msgType)
	}
}

func TestNoAuthReturns401(t *testing.T) {
	srv := New()
	defer srv.Close()

	// Use a raw client that strips auth headers.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", bytes.NewBufferString(`{"model":"x","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", resp.StatusCode)
	}
}

func TestToolCallPath(t *testing.T) {
	srv := New()
	defer srv.Close()

	// OpenAI chat with stub-tool trigger
	resp := postWithAuth(t, srv.URL+"/v1/chat/completions", `{"model":"prizmal/stub","messages":[{"role":"user","content":"use stub-tool"}]}`)
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	_ = resp.Body.Close()
	choices, _ := body["choices"].([]any)
	first, _ := choices[0].(map[string]any)
	msg, _ := first["message"].(map[string]any)
	toolCalls, ok := msg["tool_calls"].([]any)
	if !ok || len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool_call, got %v", msg)
	}

	// Anthropic with stub-tool trigger
	resp2 := postWithAuth(t, srv.URL+"/v1/messages", `{"model":"prizmal/stub","max_tokens":8,"messages":[{"role":"user","content":"use stub-tool"}]}`)
	var body2 map[string]any
	_ = json.NewDecoder(resp2.Body).Decode(&body2)
	_ = resp2.Body.Close()
	content, _ := body2["content"].([]any)
	block, _ := content[0].(map[string]any)
	if blockType, _ := block["type"].(string); blockType != "tool_use" {
		t.Fatalf("expected tool_use, got %q", blockType)
	}
}

func TestModelsEndpoint(t *testing.T) {
	srv := New()
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+StubKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if obj, _ := body["object"].(string); obj != "list" {
		t.Fatalf("object = %q, want list", obj)
	}
}

func TestCountTokensEndpoint(t *testing.T) {
	srv := New()
	defer srv.Close()

	resp := postWithAuth(t, srv.URL+"/v1/messages/count_tokens", `{"model":"x","messages":[{"role":"user","content":"hi"}]}`)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if tokens, _ := body["input_tokens"].(float64); tokens != 1 {
		t.Fatalf("input_tokens = %v, want 1", body["input_tokens"])
	}
}

func TestAuthWithBearer(t *testing.T) {
	srv := New()
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", bytes.NewBufferString(`{"model":"x","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+StubKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d, want 200 with Bearer auth", resp.StatusCode)
	}
}

func TestAuthWithXAPIKey(t *testing.T) {
	srv := New()
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/messages", bytes.NewBufferString(`{"model":"x","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", StubKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d, want 200 with x-api-key auth", resp.StatusCode)
	}
}

func TestStreamingChatCompletions(t *testing.T) {
	srv := New()
	defer srv.Close()

	resp := postWithAuth(t, srv.URL+"/v1/chat/completions", `{"model":"x","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(body, []byte("data: [DONE]")) {
		t.Fatal("expected SSE stream to end with data: [DONE]")
	}
}

func TestStreamingMessages(t *testing.T) {
	srv := New()
	defer srv.Close()

	resp := postWithAuth(t, srv.URL+"/v1/messages", `{"model":"x","max_tokens":8,"stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(body, []byte("event: message_stop")) {
		t.Fatal("expected SSE stream to contain message_stop event")
	}
}
