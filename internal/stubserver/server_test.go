package stubserver

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
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

// TestDeviceTokenOnlyAcceptsOnlyTheDeviceToken pins the strict mode a device
// launch test uses: the stub accepts the token its own refresh handed out and
// refuses every other credential, so a harness that reaches a reply proves it
// authenticated with a token the helper minted, not with the switch key.
func TestDeviceTokenOnlyAcceptsOnlyTheDeviceToken(t *testing.T) {
	srv := NewServer(WithDeviceTokenOnly())
	defer srv.Close()

	for _, tc := range []struct {
		name       string
		authorize  string
		apiKey     string
		wantStatus int
	}{
		{"the device token as a bearer", "Bearer " + DeviceToken, "", http.StatusOK},
		{"the device token as x-api-key", "", DeviceToken, http.StatusOK},
		{"another bearer", "Bearer stub-key", "", http.StatusUnauthorized},
		{"another x-api-key", "", "stub-key", http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, srv.URL+"/v1/models", nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.authorize != "" {
				req.Header.Set("Authorization", tc.authorize)
			}
			if tc.apiKey != "" {
				req.Header.Set("x-api-key", tc.apiKey)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}
		})
	}
}

// TestDeviceIssuerRefusesExpiredToken pins the short-lived-token stub: a token
// is accepted while its life remains and refused with 401 after it passes, so a
// session that keeps sending its first token stops once that token expires.
func TestDeviceIssuerRefusesExpiredToken(t *testing.T) {
	issuer := NewDeviceTokenIssuer(50 * time.Millisecond)
	srv := NewServer(WithShortLivedDeviceTokens(issuer), WithModels("smart"))
	defer srv.Close()

	// Refresh mints the first token.
	tok := refreshDeviceToken(t, srv.URL)
	if !issuer.accept(tok, time.Now()) {
		t.Fatal("a fresh token was refused")
	}

	// Wait out the life, then present the same token: the stub must refuse it.
	time.Sleep(80 * time.Millisecond)
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expired token returned %d, want 401", resp.StatusCode)
	}
	if issuer.RejectedExpired() == 0 {
		t.Fatal("the issuer did not record the expired presentation")
	}
}

// TestDeviceIssuerAcceptsARefreshedToken pins the other half: a token minted
// after the first expired is accepted, which is what a refreshing session gets.
func TestDeviceIssuerAcceptsARefreshedToken(t *testing.T) {
	issuer := NewDeviceTokenIssuer(50 * time.Millisecond)
	srv := NewServer(WithShortLivedDeviceTokens(issuer), WithModels("smart"))
	defer srv.Close()

	refreshDeviceToken(t, srv.URL)
	time.Sleep(80 * time.Millisecond)
	second := refreshDeviceToken(t, srv.URL)

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+second)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("refreshed token returned %d, want 200", resp.StatusCode)
	}
	if got := issuer.Issued(); got != 2 {
		t.Fatalf("Issued() = %d, want 2", got)
	}
}

// refreshDeviceToken runs the device refresh against a stub and returns the
// token it minted.
func refreshDeviceToken(t *testing.T, baseURL string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, baseURL+"/v1/cli/token", bytes.NewBufferString(`{"device_id":"dev_x","ts":"t","sig":"s"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("refresh returned %d, want 200", resp.StatusCode)
	}
	var body struct {
		DeviceToken string `json:"device_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body.DeviceToken
}

// TestIssuerCrossedExpiryDistinguishesARefreshWithinOneLife pins the assertion
// the expiry test leans on: a session that refreshed while every token was
// still valid has not crossed an expiry, and one whose accepted request landed
// after the first token's life did.
func TestIssuerCrossedExpiryDistinguishesARefreshWithinOneLife(t *testing.T) {
	ttl := 100 * time.Millisecond

	within := NewDeviceTokenIssuer(ttl)
	start := time.Now()
	tok := within.Token(start)
	within.accept(tok, start.Add(10*time.Millisecond))
	within.Token(start.Add(10 * time.Millisecond))
	if within.CrossedExpiry() {
		t.Fatal("CrossedExpiry = true for a refresh inside the first token's life")
	}

	across := NewDeviceTokenIssuer(ttl)
	first := across.Token(start)
	across.accept(first, start.Add(10*time.Millisecond))
	// A second token minted after the first expired, presented while valid.
	second := across.Token(start.Add(200 * time.Millisecond))
	across.accept(second, start.Add(210*time.Millisecond))
	if !across.CrossedExpiry() {
		t.Fatal("CrossedExpiry = false for a request after the first token's life")
	}
}

// TestNonStreamingToolCallReportsToolCallsFinishReason pins the non-streaming
// chat-completions answer: a tool-call message has to carry finish_reason
// "tool_calls", as the streamed answer does. A client that keys on
// finish_reason treats "stop" as a finished turn and never runs the tool.
func TestNonStreamingToolCallReportsToolCallsFinishReason(t *testing.T) {
	srv := New()
	defer srv.Close()

	resp := postWithAuth(t, srv.URL+"/v1/chat/completions", `{"model":"prizmal/stub","messages":[{"role":"user","content":"use stub-tool"}]}`)
	defer func() { _ = resp.Body.Close() }()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	choices, _ := body["choices"].([]any)
	if len(choices) == 0 {
		t.Fatalf("no choices in %v", body)
	}
	first, _ := choices[0].(map[string]any)
	msg, _ := first["message"].(map[string]any)
	if _, ok := msg["tool_calls"].([]any); !ok {
		t.Fatalf("expected a tool_calls message, got %v", msg)
	}
	if got, _ := first["finish_reason"].(string); got != "tool_calls" {
		t.Fatalf("finish_reason = %q, want tool_calls", got)
	}
}

// TestAnthropicToolUseReportsToolUseStopReason is the Anthropic-side twin of
// the chat-completions finish_reason test: a message whose content is a
// tool_use block has to carry stop_reason "tool_use". "end_turn" tells a client
// the turn is done, so it never runs the tool.
func TestAnthropicToolUseReportsToolUseStopReason(t *testing.T) {
	srv := New()
	defer srv.Close()

	resp := postWithAuth(t, srv.URL+"/v1/messages", `{"model":"prizmal/stub","max_tokens":8,"messages":[{"role":"user","content":"use stub-tool"}]}`)
	defer func() { _ = resp.Body.Close() }()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	content, _ := body["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("no content in %v", body)
	}
	block, _ := content[0].(map[string]any)
	if blockType, _ := block["type"].(string); blockType != "tool_use" {
		t.Fatalf("expected tool_use, got %q", blockType)
	}
	if got, _ := body["stop_reason"].(string); got != "tool_use" {
		t.Fatalf("stop_reason = %q, want tool_use", got)
	}
}

// A usage plan answers request n with its n-th value, in order, and repeats
// the last value once the list runs out. The first request gets the first
// value, never the last.
func TestUsagePlanAnswersInOrderThenRepeatsTheLast(t *testing.T) {
	plan := NewUsagePlan(11, 22)
	var got []int
	for range 4 {
		_, usage := plan.nextUsage(map[string]any{"messages": []any{}})
		got = append(got, usage["input_tokens"].(int))
	}
	want := []int{11, 22, 22, 22}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("request %d reported %d input tokens, want %d (all: %v)", i, got[i], want[i], got)
		}
	}
	if plan.Requests() != 4 {
		t.Fatalf("Requests() = %d, want 4", plan.Requests())
	}
}

// Every reply carries its own message id. A client that counts tokens from a
// reply's usage groups the conversation by that id, and replies that share
// one read as a single long turn.
func TestUsagePlanGivesEachReplyItsOwnMessageID(t *testing.T) {
	plan := NewUsagePlan(1)
	first, _ := plan.nextUsage(map[string]any{})
	second, _ := plan.nextUsage(map[string]any{})
	if first == second {
		t.Fatalf("two replies share the message id %q", first)
	}
}

// A request that carries the compaction instructions gets the summary answer,
// and the plan's own value is spent on the request all the same, so the
// request count stays in step with the plan.
func TestUsagePlanAnswersACompactionRequestWithTheSummary(t *testing.T) {
	plan := NewUsagePlan(5, 6)
	_, usage := plan.nextUsage(map[string]any{
		"messages": []any{map[string]any{"role": "user", "content": "CRITICAL: Respond with TEXT ONLY. Do NOT call any tools."}},
	})
	if summary, _ := usage["summary"].(bool); !summary {
		t.Fatalf("a compaction request was answered with %v, want the summary flag", usage)
	}
	_, next := plan.nextUsage(map[string]any{})
	if next["input_tokens"].(int) != 6 {
		t.Fatalf("the request after a compaction request reported %v input tokens, want the plan's second value 6", next["input_tokens"])
	}
}

// A request whose planned count passes the overflow limit gets a 400
// invalid_request_error with the message spelled from the template. A request
// at or under the limit, and a compaction request, are answered as usual.
func TestUsagePlanRefusesARequestPastTheOverflowLimit(t *testing.T) {
	plan := NewUsagePlan(10, 2000, 10).OverflowAbove(1000, "prompt is too long: {tokens} tokens > {limit} maximum")
	srv := NewServer(WithUsagePlan(plan))
	defer srv.Close()

	statuses := []int{}
	var refusal map[string]any
	for range 3 {
		resp := postWithAuth(t, srv.URL+"/v1/messages", `{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
		statuses = append(statuses, resp.StatusCode)
		if resp.StatusCode == http.StatusBadRequest {
			_ = json.NewDecoder(resp.Body).Decode(&refusal)
		}
		_ = resp.Body.Close()
	}
	if statuses[0] != 200 || statuses[1] != 400 || statuses[2] != 200 {
		t.Fatalf("statuses = %v, want [200 400 200]", statuses)
	}
	errObj, _ := refusal["error"].(map[string]any)
	if errObj["type"] != "invalid_request_error" || errObj["message"] != "prompt is too long: 2000 tokens > 1000 maximum" {
		t.Fatalf("refusal = %v", refusal)
	}

	resp := postWithAuth(t, srv.URL+"/v1/messages", `{"model":"m","messages":[{"role":"user","content":"CRITICAL: Respond with TEXT ONLY."}]}`)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("a compaction request got %d, want 200", resp.StatusCode)
	}
}
