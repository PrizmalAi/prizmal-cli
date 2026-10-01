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

func TestNewWithModelsListsTheGivenIDs(t *testing.T) {
	srv := NewWithModels("claude-sonnet-house", "glm-5.1")
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+StubKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range body.Data {
		ids = append(ids, m.ID)
	}
	if len(ids) != 2 || ids[0] != "claude-sonnet-house" || ids[1] != "glm-5.1" {
		t.Fatalf("ids = %v, want [claude-sonnet-house glm-5.1]", ids)
	}
}

// sseEvents posts body to path and returns the event names of the SSE stream
// and the concatenated data lines.
func sseEvents(t *testing.T, url, body string) ([]string, string) {
	t.Helper()
	resp := postWithAuth(t, url, body)
	defer func() { _ = resp.Body.Close() }()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var events []string
	var data bytes.Buffer
	for _, line := range bytes.Split(raw, []byte("\n")) {
		switch {
		case bytes.HasPrefix(line, []byte("event: ")):
			events = append(events, string(line[len("event: "):]))
		case bytes.HasPrefix(line, []byte("data: ")):
			data.Write(line[len("data: "):])
			data.WriteByte('\n')
		}
	}
	return events, data.String()
}

func TestResponsesStreamEndsWithCompleted(t *testing.T) {
	srv := New()
	defer srv.Close()

	events, data := sseEvents(t, srv.URL+"/v1/responses", `{"model":"smart","input":"hi","stream":true}`)
	if len(events) == 0 || events[len(events)-1] != "response.completed" {
		t.Fatalf("events = %v, want a stream ending in response.completed", events)
	}
	if !bytes.Contains([]byte(data), []byte(Reply)) {
		t.Fatalf("stream data does not carry %q:\n%s", Reply, data)
	}
}

func TestMessagesStreamFramesTheTextBlock(t *testing.T) {
	srv := New()
	defer srv.Close()

	events, data := sseEvents(t, srv.URL+"/v1/messages", `{"model":"smart","max_tokens":10,"stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	want := []string{"message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop"}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events = %v, want %v", events, want)
		}
	}
	if !bytes.Contains([]byte(data), []byte(Reply)) {
		t.Fatalf("stream data does not carry %q:\n%s", Reply, data)
	}
}

func TestChatCompletionsStreamSendsFinishReason(t *testing.T) {
	srv := New()
	defer srv.Close()

	_, data := sseEvents(t, srv.URL+"/v1/chat/completions", `{"model":"smart","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if !bytes.Contains([]byte(data), []byte(`"finish_reason":"stop"`)) {
		t.Fatalf("stream has no finish_reason stop:\n%s", data)
	}
	if !bytes.Contains([]byte(data), []byte(Reply)) {
		t.Fatalf("stream data does not carry %q:\n%s", Reply, data)
	}
	if !bytes.HasSuffix(bytes.TrimSpace([]byte(data)), []byte("[DONE]")) {
		t.Fatalf("stream does not end with [DONE]:\n%s", data)
	}
}

func TestNewWithEntriesSendsTierAndDescriptionOnlyWhenSet(t *testing.T) {
	srv := NewWithEntries(Entry{ID: "smart", Tier: "opus", Description: "Fast"}, Entry{ID: "plain"})
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+StubKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	var body struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 2 || body.Data[0]["tier"] != "opus" || body.Data[0]["description"] != "Fast" {
		t.Fatalf("data = %v, want smart with its tier and description first", body.Data)
	}
	if _, ok := body.Data[1]["tier"]; ok {
		t.Fatalf("plain = %v, want no tier field", body.Data[1])
	}
}

// TestCLITokenPendingReturns404 pins the enrollment state: a stub that has not
// approved the device answers the refresh with the 404 "unknown device" the CLI
// polls through.
func TestCLITokenPendingReturns404(t *testing.T) {
	srv := New()
	defer srv.Close()

	// No bearer credential: the refresh carries its own signature in the body.
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/cli/token", bytes.NewBufferString(`{"device_id":"dev_x","ts":"t","sig":"s"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("pending refresh returned %d, want 404", resp.StatusCode)
	}
}

// TestCLITokenApprovedReturnsToken pins the approving stub: 200 with a fixed
// token, a 600-second expiry and a reauth_by, the shape the login screen reads.
func TestCLITokenApprovedReturnsToken(t *testing.T) {
	srv := NewWithDeviceApproval()
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/cli/token", bytes.NewBufferString(`{"device_id":"dev_x","ts":"t","sig":"s"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approved refresh returned %d, want 200", resp.StatusCode)
	}
	var body struct {
		DeviceToken string `json:"device_token"`
		ExpiresIn   int    `json:"expires_in"`
		ReauthBy    string `json:"reauth_by"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.DeviceToken != DeviceToken {
		t.Errorf("device_token = %q, want %q", body.DeviceToken, DeviceToken)
	}
	if body.ExpiresIn != 600 {
		t.Errorf("expires_in = %d, want 600", body.ExpiresIn)
	}
	if body.ReauthBy != DeviceReauthBy {
		t.Errorf("reauth_by = %q, want %q", body.ReauthBy, DeviceReauthBy)
	}
}
