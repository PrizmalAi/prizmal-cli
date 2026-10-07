// Package stubserver provides a deterministic HTTP server that speaks the
// three wire dialects the coding-agent harnesses use (OpenAI chat/completions,
// OpenAI responses, Anthropic messages), plus their probe endpoints. It is
// intended for CI end-to-end harness tests: point the harness at the stub
// server's URL, pass the stub key, and assert deterministic responses without
// billing real provider tokens.
package stubserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"
)

// StubKey is the API key tests should pass via envconfig.SetAPIKey.
const StubKey = "stub-key"

// Reply is the placeholder text of every answer. It is distinctive so that a
// test can find it in a harness's output, where the bare word "stub" could
// also come from a model id or an error message.
const Reply = "PRIZMAL-STUB-REPLY"

// New returns a running httptest.Server that speaks all three dialects.
// Callers must defer Close it.
func New() *httptest.Server {
	return NewServer()
}

// NewWithModels returns a running stub server whose /v1/models lists exactly
// ids, each as text-only, in place of the default fixture. It is for tests
// that render a catalog, where the names on screen are the point.
func NewWithModels(ids ...string) *httptest.Server {
	return NewServer(WithModels(ids...))
}

// NewWithDeviceApproval returns a running stub server whose POST /v1/cli/token
// approves a device: it answers 200 with a fixed device token, a 600-second
// expiry and a fixed reauth_by. Without it the route answers 404, so a login
// stays pending, which is the state the pending screen records.
func NewWithDeviceApproval() *httptest.Server {
	return NewServer(WithDeviceApproval())
}

// NewWithEntries is NewWithModels for entries that carry a tier or a
// description.
func NewWithEntries(entries ...Entry) *httptest.Server {
	return NewServer(WithEntries(entries...))
}

// DeviceToken and DeviceReauthBy are the fixed values the approving stub
// returns. They are constants so an approved login screen is deterministic.
const (
	DeviceToken    = "pz-d-dt-stub-token"
	DeviceReauthBy = "2026-10-08T00:00:00Z"
)

// Entry is one /v1/models entry. Tier and Description are sent only when set.
type Entry struct {
	ID          string
	Tier        string
	Description string
}

// serverConfig is what the options build: the /v1/models fixture, and whether
// POST /v1/cli/token approves the device. The two are independent, so a test
// can set either, both, or neither.
type serverConfig struct {
	models         []map[string]any
	deviceApproved bool
	deviceOnly     bool
	issuer         *DeviceTokenIssuer
	toolLoop       *piToolLoop
}

// ServerOption configures a stub server.
type ServerOption func(*serverConfig)

// WithDeviceTokenOnly makes the stub accept one credential: the device token
// its own POST /v1/cli/token hands out. Every other bearer and every x-api-key
// is refused with 401, so a launch that reaches the harness authenticated with
// the token a refresh minted, rather than with the switch key the config
// already held. It approves the device, so it stands alone as well as beside
// WithDeviceApproval.
func WithDeviceTokenOnly() ServerOption {
	return func(c *serverConfig) {
		c.deviceApproved = true
		c.deviceOnly = true
	}
}

// WithShortLivedDeviceTokens makes the stub mint a fresh token on every device
// refresh and accept only a token that has not expired. A token presented after
// its life is refused with 401, so a session that reuses its first token stops
// and only one that refreshes keeps running. issuer records what the stub
// handed out and what the harness presented, so a test can assert a session
// crossed an expiry. It approves the device as well.
func WithShortLivedDeviceTokens(issuer *DeviceTokenIssuer) ServerOption {
	return func(c *serverConfig) {
		c.deviceApproved = true
		c.issuer = issuer
	}
}

// WithPiToolLoop makes the chat-completions route answer a bash tool call that
// sleeps for sleep on the first turns requests, then answer with Reply. A real
// Pi session runs bash between turns, so one launch produces several inference
// requests spread over wall-clock time, which is what a test needs to cross a
// short device-token expiry. It is Pi-specific because it names Pi's bash tool.
func WithPiToolLoop(turns int, sleep time.Duration) ServerOption {
	return func(c *serverConfig) {
		c.toolLoop = &piToolLoop{turns: turns, sleep: sleep}
	}
}

// DeviceTokenIssuer mints short-lived device tokens and records what the stub
// handed out and what a harness presented. It is safe for concurrent use: an
// httptest handler may serve several requests at once.
type DeviceTokenIssuer struct {
	ttl time.Duration

	mu        sync.Mutex
	minted    []issuedToken
	presented []presentedToken
}

type issuedToken struct {
	value  string
	expiry time.Time
}

type presentedToken struct {
	value string
	at    time.Time
}

// NewDeviceTokenIssuer returns an issuer whose tokens live for ttl. A ttl
// shorter than device.CacheValidMargin is deliberate in a test: the helper
// reuses a cached token only while it still has that margin left, so a shorter
// life forces a fresh refresh on every request and an expiry the test can
// cross in seconds.
func NewDeviceTokenIssuer(ttl time.Duration) *DeviceTokenIssuer {
	return &DeviceTokenIssuer{ttl: ttl}
}

// Token mints a fresh token at now.
func (i *DeviceTokenIssuer) Token(now time.Time) string {
	i.mu.Lock()
	defer i.mu.Unlock()
	value := fmt.Sprintf("pz-d-dt-stub-%d", len(i.minted)+1)
	i.minted = append(i.minted, issuedToken{value: value, expiry: now.Add(i.ttl)})
	return value
}

// accept records a presented token and reports whether it is one the issuer
// minted and has not expired. An empty or unknown token is refused, as is one
// whose life has passed.
func (i *DeviceTokenIssuer) accept(value string, now time.Time) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.presented = append(i.presented, presentedToken{value: value, at: now})
	for _, minted := range i.minted {
		if minted.value == value {
			return now.Before(minted.expiry)
		}
	}
	return false
}

// Issued reports how many tokens the stub handed out.
func (i *DeviceTokenIssuer) Issued() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return len(i.minted)
}

// AcceptedValues reports the distinct token values the harness presented that
// the issuer accepted at the time. It is the count a test asserts to prove the
// harness presented a token minted after an earlier one expired.
func (i *DeviceTokenIssuer) AcceptedValues() []string {
	i.mu.Lock()
	defer i.mu.Unlock()
	seen := make(map[string]bool)
	var accepted []string
	for _, p := range i.presented {
		for _, m := range i.minted {
			if m.value == p.value && p.at.Before(m.expiry) && !seen[p.value] {
				seen[p.value] = true
				accepted = append(accepted, p.value)
			}
		}
	}
	return accepted
}

// RejectedExpired reports how many requests presented a token that had already
// expired, which the stub refused. A session that refreshes never produces one.
func (i *DeviceTokenIssuer) RejectedExpired() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	count := 0
	for _, p := range i.presented {
		for _, m := range i.minted {
			if m.value == p.value && !p.at.Before(m.expiry) {
				count++
			}
		}
	}
	return count
}

// CrossedExpiry reports whether the session ran past the life of its first
// token: an accepted request carried a token minted after the first token
// expired. It is the assertion a test makes to prove a session outlived one
// device token rather than merely refreshing while every token was still valid.
func (i *DeviceTokenIssuer) CrossedExpiry() bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	if len(i.minted) < 2 {
		return false
	}
	firstExpiry := i.minted[0].expiry
	for _, p := range i.presented {
		if !p.at.After(firstExpiry) {
			continue
		}
		for _, m := range i.minted {
			if m.value == p.value && p.at.Before(m.expiry) {
				return true
			}
		}
	}
	return false
}

// piToolLoop is the state of WithPiToolLoop: how many tool-call turns remain,
// and how long the bash call sleeps.
type piToolLoop struct {
	mu        sync.Mutex
	turns     int
	sleep     time.Duration
	completed int
}

// next reports whether the current request gets a tool call, and how long its
// bash call should sleep. It consumes a turn until the count is spent.
func (l *piToolLoop) next() (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.completed >= l.turns {
		return false, 0
	}
	l.completed++
	return true, l.sleep
}

// WithModels serves exactly ids from /v1/models, each as text-only.
func WithModels(ids ...string) ServerOption {
	return func(c *serverConfig) {
		models := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			models = append(models, map[string]any{
				"id":                id,
				"input_modalities":  []string{"text"},
				"output_modalities": []string{"text"},
			})
		}
		c.models = models
	}
}

// WithEntries serves entries from /v1/models, sending a tier or description
// only when the entry has one.
func WithEntries(entries ...Entry) ServerOption {
	return func(c *serverConfig) {
		models := make([]map[string]any, 0, len(entries))
		for _, e := range entries {
			model := map[string]any{
				"id":                e.ID,
				"input_modalities":  []string{"text"},
				"output_modalities": []string{"text"},
			}
			if e.Tier != "" {
				model["tier"] = e.Tier
			}
			if e.Description != "" {
				model["description"] = e.Description
			}
			models = append(models, model)
		}
		c.models = models
	}
}

// WithDeviceApproval makes POST /v1/cli/token approve the device. Without it
// that route answers 404, the pending state.
func WithDeviceApproval() ServerOption {
	return func(c *serverConfig) { c.deviceApproved = true }
}

// NewServer returns a running stub server built from options. Callers must
// defer Close it.
func NewServer(opts ...ServerOption) *httptest.Server {
	var c serverConfig
	for _, opt := range opts {
		opt(&c)
	}
	return httptest.NewServer(handler(&c))
}

// handler serves the stub API. A nil models serves the default fixture.
func handler(c *serverConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// POST /v1/cli/token is the device refresh. The real Switch serves it
		// without a bearer credential, because the request carries its own
		// ed25519 signature in the body and the Config API checks it, so it is
		// answered before the auth check below.
		if method, path := r.Method, r.URL.Path; method == http.MethodPost && path == "/v1/cli/token" {
			handleCLIToken(w, c)
			return
		}

		// Auth check: require either Authorization: Bearer or x-api-key.
		if !hasAuth(r) || !hasAcceptedAuth(r, c) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error": map[string]any{"message": "API key is required"},
			})
			return
		}

		path := r.URL.Path
		method := r.Method

		switch {
		case method == http.MethodGet && path == "/v1/models":
			handleModels(w, c.models)

		case method == http.MethodPost && path == "/v1/chat/completions":
			handleChatCompletions(w, r, c.toolLoop)

		case method == http.MethodPost && path == "/v1/responses":
			handleResponses(w, r)

		case method == http.MethodPost && path == "/v1/messages":
			handleMessages(w, r)

		case method == http.MethodPost && path == "/v1/messages/count_tokens":
			writeJSON(w, http.StatusOK, map[string]any{"input_tokens": 1})

		case method == http.MethodGet && path == "/v1/responses/input_tokens":
			writeJSON(w, http.StatusOK, map[string]any{"input_tokens": 1})

		default:
			writeJSON(w, http.StatusNotFound, map[string]any{
				"error": map[string]any{"message": fmt.Sprintf("not found: %s %s", method, path)},
			})
		}
	}
}

// handleCLIToken answers the device refresh. While pending it returns 404, the
// same "unknown device" the CLI polls through during enrollment. Approved
// returns 200 with a token: the fixed DeviceToken when the stub issues no
// short-lived tokens, otherwise a fresh short-lived one. The 600-second expiry
// and fixed reauth_by mirror the shape the Switch forwards from the Config API.
func handleCLIToken(w http.ResponseWriter, c *serverConfig) {
	if !c.deviceApproved {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": map[string]any{"code": "NOT_FOUND", "message": "unknown device"},
		})
		return
	}
	if c.issuer != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"device_token": c.issuer.Token(time.Now()),
			"expires_in":   int(c.issuer.ttl / time.Second),
			"reauth_by":    DeviceReauthBy,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"device_token": DeviceToken,
		"expires_in":   600,
		"reauth_by":    DeviceReauthBy,
	})
}

func hasAuth(r *http.Request) bool {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return true
	}
	if k := r.Header.Get("x-api-key"); k != "" {
		return true
	}
	return false
}

// bearerOf is the credential a request presents, from either header the stub
// accepts.
func bearerOf(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return r.Header.Get("x-api-key")
}

// hasAcceptedAuth reports whether a request carries a credential the stub
// accepts. With no mode every well-formed credential passes. With deviceOnly
// only the fixed device token does. With an issuer only a short-lived token
// the issuer minted and that has not expired does, so a session that keeps
// sending its first token stops once that token's life passes.
func hasAcceptedAuth(r *http.Request, c *serverConfig) bool {
	if c.issuer != nil {
		return c.issuer.accept(bearerOf(r), time.Now())
	}
	if !c.deviceOnly {
		return true
	}
	if h := r.Header.Get("Authorization"); h == "Bearer "+DeviceToken {
		return true
	}
	return r.Header.Get("x-api-key") == DeviceToken
}

// requestBody reads and JSON-decodes the request body into a map.
// Returns nil if the body is empty or not valid JSON (callers handle nil).
func requestBody(r *http.Request) map[string]any {
	body, err := io.ReadAll(r.Body)
	if err != nil || len(body) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil
	}
	return m
}

// wantsToolCall reports whether the last user message contains "stub-tool".
func wantsToolCall(body map[string]any) bool {
	if body == nil {
		return false
	}
	msgs, ok := body["messages"].([]any)
	if !ok || len(msgs) == 0 {
		return false
	}
	last := msgs[len(msgs)-1]
	m, ok := last.(map[string]any)
	if !ok {
		return false
	}
	role, _ := m["role"].(string)
	if role != "user" {
		return false
	}
	return messageContains(m, "stub-tool")
}

// messageContains checks if a message's content field contains the substring.
func messageContains(m map[string]any, sub string) bool {
	content := m["content"]
	switch v := content.(type) {
	case string:
		return strings.Contains(v, sub)
	case []any:
		for _, block := range v {
			if bm, ok := block.(map[string]any); ok {
				if text, ok := bm["text"].(string); ok && strings.Contains(text, sub) {
					return true
				}
			}
		}
	}
	return false
}

// wantsStream reports whether the request has stream:true.
func wantsStream(body map[string]any) bool {
	if body == nil {
		return false
	}
	s, ok := body["stream"].(bool)
	return ok && s
}

func handleModels(w http.ResponseWriter, models []map[string]any) {
	if models != nil {
		writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": models})
		return
	}

	// The Switch reports per-entry input modalities here, and omits the field
	// on some entries. Both shapes are in the fixture on purpose: an entry
	// without input_modalities is unknown, not text-only, and the launcher's
	// capability parse has to keep telling the two apart. "file" is the
	// Switch's document modality, separate from "image", so it gets its own
	// entry.
	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data": []map[string]any{
			{
				"id":                "prizmal/stub",
				"input_modalities":  []string{"text"},
				"output_modalities": []string{"text"},
			},
			{
				"id":                "prizmal/stub-vision",
				"input_modalities":  []string{"text", "image"},
				"output_modalities": []string{"text"},
			},
			{
				"id":                "prizmal/stub-file",
				"input_modalities":  []string{"text", "file"},
				"output_modalities": []string{"text"},
			},
			{"id": "prizmal/stub-unknown"},
		},
	})
}

func handleChatCompletions(w http.ResponseWriter, r *http.Request, loop *piToolLoop) {
	body := requestBody(r)
	toolCall := wantsToolCall(body)
	stream := wantsStream(body)

	// A Pi tool loop answers a bash call that sleeps, so the session's next
	// request lands after the sleep. A test uses it to spread a session's
	// requests over more than one short device-token life.
	toolName := "stub_tool"
	toolArgs := "{}"
	if loop != nil {
		if again, sleep := loop.next(); again {
			toolCall = true
			toolName = "bash"
			toolArgs = fmt.Sprintf(`{"command":%q}`, "sleep "+fmt.Sprintf("%g", sleep.Seconds()))
		}
	}

	if stream {
		flusher, ok := startSSE(w)
		if !ok {
			return
		}

		// Each chunk carries the id, object and model a client checks, and
		// the last one carries finish_reason and usage: pi fails a stream
		// that ends without a finish_reason.
		chunk := func(delta map[string]any, finish any) map[string]any {
			return map[string]any{
				"id":      "chatcmpl_stub",
				"object":  "chat.completion.chunk",
				"created": 0,
				"model":   "prizmal/stub",
				"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}},
			}
		}
		finish := "stop"
		if toolCall {
			finish = "tool_calls"
			writeSSE(w, flusher, chunk(map[string]any{
				"role": "assistant",
				"tool_calls": []map[string]any{{
					"index":    0,
					"id":       "call_stub",
					"type":     "function",
					"function": map[string]any{"name": toolName, "arguments": toolArgs},
				}},
			}, nil))
		} else {
			writeSSE(w, flusher, chunk(map[string]any{"role": "assistant", "content": Reply}, nil))
		}
		last := chunk(map[string]any{}, finish)
		last["usage"] = map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}
		writeSSE(w, flusher, last)
		_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}

	message := map[string]any{"role": "assistant", "content": Reply}
	finish := "stop"
	if toolCall {
		finish = "tool_calls"
		message = map[string]any{
			"role": "assistant",
			"tool_calls": []map[string]any{{
				"id":       "call_stub",
				"type":     "function",
				"function": map[string]any{"name": toolName, "arguments": toolArgs},
			}},
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":      "chatcmpl_stub",
		"object":  "chat.completion",
		"model":   "prizmal/stub",
		"choices": []map[string]any{{"index": 0, "message": message, "finish_reason": finish}},
		"usage":   map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
	})
}

func handleResponses(w http.ResponseWriter, r *http.Request) {
	body := requestBody(r)
	toolCall := wantsToolCall(body)

	item := map[string]any{
		"type":   "message",
		"id":     "msg_stub",
		"status": "completed",
		"role":   "assistant",
		"content": []map[string]any{{
			"type":        "output_text",
			"text":        Reply,
			"annotations": []any{},
		}},
	}
	if toolCall {
		item = map[string]any{
			"type":      "function_call",
			"id":        "fc_stub",
			"call_id":   "call_stub",
			"status":    "completed",
			"name":      "stub_tool",
			"arguments": "{}",
		}
	}
	response := map[string]any{
		"id":     "resp_stub",
		"object": "response",
		"status": "completed",
		"model":  "prizmal/stub",
		"output": []map[string]any{item},
		"usage":  map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
	}

	if !wantsStream(body) {
		writeJSON(w, http.StatusOK, response)
		return
	}

	flusher, ok := startSSE(w)
	if !ok {
		return
	}
	// Codex reads a turn as finished only on response.completed, and takes the
	// output items from response.output_item.done, so both are sent. The text
	// deltas are what a harness prints while the turn streams.
	created := map[string]any{}
	for k, v := range response {
		created[k] = v
	}
	created["status"] = "in_progress"
	created["output"] = []any{}
	writeSSEEvent(w, flusher, "response.created", map[string]any{"type": "response.created", "response": created})
	writeSSEEvent(w, flusher, "response.output_item.added", map[string]any{"type": "response.output_item.added", "output_index": 0, "item": item})
	if !toolCall {
		writeSSEEvent(w, flusher, "response.output_text.delta", map[string]any{
			"type": "response.output_text.delta", "item_id": "msg_stub", "output_index": 0, "content_index": 0, "delta": Reply,
		})
		writeSSEEvent(w, flusher, "response.output_text.done", map[string]any{
			"type": "response.output_text.done", "item_id": "msg_stub", "output_index": 0, "content_index": 0, "text": Reply,
		})
	}
	writeSSEEvent(w, flusher, "response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item})
	writeSSEEvent(w, flusher, "response.completed", map[string]any{"type": "response.completed", "response": response})
}

// startSSE sets the event-stream headers. It reports false, after answering
// with an error, when the writer cannot flush.
func startSSE(w http.ResponseWriter) (http.Flusher, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"message": "streaming not supported"}})
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	return flusher, true
}

func handleMessages(w http.ResponseWriter, r *http.Request) {
	body := requestBody(r)
	toolCall := wantsToolCall(body)
	stream := wantsStream(body)

	// The unified status header the real Switch sends, so a client that reads it
	// draws the funded-tenant screen.
	w.Header().Set("anthropic-ratelimit-unified-status", "allowed")

	if stream {
		flusher, ok := startSSE(w)
		if !ok {
			return
		}

		// The full Anthropic event sequence: a harness assembles the text
		// block between content_block_start and content_block_stop, and reads
		// the stop reason from message_delta.
		writeSSEEvent(w, flusher, "message_start", map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":            "msg_stub",
				"type":          "message",
				"role":          "assistant",
				"model":         "prizmal/stub",
				"content":       []any{},
				"stop_reason":   nil,
				"stop_sequence": nil,
				"usage":         map[string]any{"input_tokens": 1, "output_tokens": 0},
			},
		})
		writeSSEEvent(w, flusher, "content_block_start", map[string]any{
			"type":          "content_block_start",
			"index":         0,
			"content_block": map[string]any{"type": "text", "text": ""},
		})
		writeSSEEvent(w, flusher, "content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": 0,
			"delta": map[string]any{"type": "text_delta", "text": Reply},
		})
		writeSSEEvent(w, flusher, "content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
		writeSSEEvent(w, flusher, "message_delta", map[string]any{
			"type":  "message_delta",
			"delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil},
			"usage": map[string]any{"output_tokens": 1},
		})
		writeSSEEvent(w, flusher, "message_stop", map[string]any{"type": "message_stop"})
		return
	}

	content := []map[string]any{{"type": "text", "text": Reply}}
	if toolCall {
		content = []map[string]any{{"type": "tool_use", "id": "toolu_stub", "name": "stub_tool", "input": map[string]any{}}}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":          "msg_stub",
		"type":        "message",
		"role":        "assistant",
		"model":       "prizmal/stub",
		"content":     content,
		"stop_reason": "end_turn",
		"usage":       map[string]any{"input_tokens": 1, "output_tokens": 1},
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, v any) {
	data, _ := json.Marshal(v)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
}

func writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, eventType string, v any) {
	data, _ := json.Marshal(v)
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, data)
	flusher.Flush()
}
