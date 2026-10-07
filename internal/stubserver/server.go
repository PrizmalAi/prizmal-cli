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
)

// StubKey is the API key tests should pass via envconfig.SetAPIKey.
const StubKey = "stub-key"

// Reply is the placeholder text of every answer. It is distinctive so that a
// test can find it in a harness's output, where the bare word "stub" could
// also come from a model id or an error message.
const Reply = "PRIZMAL-STUB-REPLY"

// SummaryReply is the answer a usage plan gives a compaction request: an
// analysis block and a summary, the shape the compaction's parser looks for
// (the request's instructions end with a REMINDER to produce it). Its text is
// distinctive, so a test can find the compaction's answer in the reply of the
// request that asked for it.
const SummaryReply = "<analysis>None needed.</analysis>\n<summary>\nSummary: the stub switch answered the compaction request.\n</summary>"

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
	usagePlan      *UsagePlan
}

// ServerOption configures a stub server.
type ServerOption func(*serverConfig)

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

// UsagePlan is a test knob that makes the stub report usage, /v1/messages
// by /v1/messages, the way Claude Code reads it. A plan that starts a
// conversation near a compaction window makes Claude Code's next turn cross
// the threshold, so a test can watch the requests the client sends as its
// token count passes it.
//
// The plan counts every request the stub serves, so a test that wants a
// threshold crossed mid-conversation reads Requests() and asserts on what the
// client did as the numbers grew.
type UsagePlan struct {
	// InputTokens is the usage.input_tokens of request n, in order. The last
	// value repeats after the list runs out.
	inputTokens []int
	// Requests records one entry per /v1/messages request: the JSON-encoded
	// body, so a test can find the last message's text. Guarded by the mutex.
	mu       sync.Mutex
	bodies   []string
	received int
}

// NewUsagePlan builds a plan whose input_tokens follow values turn by turn.
func NewUsagePlan(inputTokens ...int) *UsagePlan {
	return &UsagePlan{inputTokens: inputTokens}
}

// WithUsagePlan installs the plan on the stub's /v1/messages route.
func WithUsagePlan(plan *UsagePlan) ServerOption {
	return func(c *serverConfig) { c.usagePlan = plan }
}

// Requests returns how many /v1/messages calls the stub has served.
func (p *UsagePlan) Requests() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.received
}

// LastBody returns the body of the most recent /v1/messages request, decoded
// into a map, or nil when no request arrived.
func (p *UsagePlan) LastBody() map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.bodies) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(p.bodies[len(p.bodies)-1]), &m); err != nil {
		return nil
	}
	return m
}

// Bodies returns every /v1/messages request body the stub has served, in
// order, each decoded into a map.
func (p *UsagePlan) Bodies() []map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	bodies := make([]map[string]any, 0, len(p.bodies))
	for _, raw := range p.bodies {
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			continue
		}
		bodies = append(bodies, m)
	}
	return bodies
}

// nextUsage records the request body and answers the plan's next
// input_tokens. The last value repeats once the list runs out, so a
// conversation whose compaction request arrives crosses the threshold on the
// turn the plan aims at and stays past it afterward.
//
// A request whose messages carry the compaction instructions ("CRITICAL:
// Respond with TEXT ONLY", tQt in the 2.1.x binaries) is answered with an
// analysis and a summary instead of the plan's reply: the compaction needs a
// summary it can parse, and the shape it asks for is the one it parses. The
// summary's text is distinctive so a test can find the compaction's answer.
func (p *UsagePlan) nextUsage(body map[string]any) map[string]any {
	raw, _ := json.Marshal(body)
	p.mu.Lock()
	p.bodies = append(p.bodies, string(raw))
	n := p.received
	p.received++
	p.mu.Unlock()

	if messageContainsStrings(raw, "CRITICAL: Respond with TEXT ONLY") {
		return map[string]any{"input_tokens": 1, "output_tokens": 80, "summary": true}
	}

	tokens := 1
	if len(p.inputTokens) > 0 {
		tokens = p.inputTokens[len(p.inputTokens)-1]
		if n < len(p.inputTokens) {
			tokens = p.inputTokens[n]
		}
	}
	return map[string]any{"input_tokens": tokens, "output_tokens": 1}
}

// messageContainsStrings reports whether the message JSON carries sub.
func messageContainsStrings(raw []byte, sub string) bool {
	return strings.Contains(string(raw), sub)
}

// NewServer returns a running stub server built from options. Callers must
// defer Close it.
func NewServer(opts ...ServerOption) *httptest.Server {
	var c serverConfig
	for _, opt := range opts {
		opt(&c)
	}
	return httptest.NewServer(handler(c.models, c.deviceApproved, c.usagePlan))
}

// handler serves the stub API. A nil models serves the default fixture.
func handler(models []map[string]any, deviceApproved bool, usagePlan *UsagePlan) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// POST /v1/cli/token is the device refresh. The real Switch serves it
		// without a bearer credential, because the request carries its own
		// ed25519 signature in the body and the Config API checks it, so it is
		// answered before the auth check below.
		if method, path := r.Method, r.URL.Path; method == http.MethodPost && path == "/v1/cli/token" {
			handleCLIToken(w, deviceApproved)
			return
		}

		// Auth check: require either Authorization: Bearer or x-api-key.
		if !hasAuth(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error": map[string]any{"message": "API key is required"},
			})
			return
		}

		path := r.URL.Path
		method := r.Method

		switch {
		case method == http.MethodGet && path == "/v1/models":
			handleModels(w, models)

		case method == http.MethodPost && path == "/v1/chat/completions":
			handleChatCompletions(w, r)

		case method == http.MethodPost && path == "/v1/responses":
			handleResponses(w, r)

		case method == http.MethodPost && path == "/v1/messages":
			handleMessages(w, r, usagePlan)

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
// returns 200 with a fixed token, a 600-second expiry and a fixed reauth_by,
// mirroring the shape the Switch forwards from the Config API.
func handleCLIToken(w http.ResponseWriter, approved bool) {
	if !approved {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": map[string]any{"code": "NOT_FOUND", "message": "unknown device"},
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

// wantsToolCall reports whether any user message contains "stub-tool".
//
// Every user message is scanned, not just the last one: a harness appends
// context injections (an environment block, a system reminder) as further
// user-role messages after the prompt, and a trigger word in the prompt must
// still fire through them.
func wantsToolCall(body map[string]any) bool {
	if body == nil {
		return false
	}
	msgs, ok := body["messages"].([]any)
	if !ok {
		return false
	}
	for _, msg := range msgs {
		m, ok := msg.(map[string]any)
		if !ok {
			continue
		}
		role, _ := m["role"].(string)
		if role != "user" {
			continue
		}
		if messageContains(m, "stub-tool") {
			return true
		}
	}
	return false
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

func handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	body := requestBody(r)
	toolCall := wantsToolCall(body)
	stream := wantsStream(body)

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
					"function": map[string]any{"name": "stub_tool", "arguments": "{}"},
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
	if toolCall {
		message = map[string]any{
			"role": "assistant",
			"tool_calls": []map[string]any{{
				"id":       "call_stub",
				"type":     "function",
				"function": map[string]any{"name": "stub_tool", "arguments": "{}"},
			}},
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":      "chatcmpl_stub",
		"object":  "chat.completion",
		"model":   "prizmal/stub",
		"choices": []map[string]any{{"index": 0, "message": message, "finish_reason": "stop"}},
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

// handleMessages answers the Anthropic dialect. With a usage plan installed,
// each served request records its body for the test and carries the plan's
// next input_tokens, so the conversation's token count moves the way the test
// aims it.
func handleMessages(w http.ResponseWriter, r *http.Request, plan *UsagePlan) {
	body := requestBody(r)
	toolCall := wantsToolCall(body)
	stream := wantsStream(body)

	// The unified status header the real Switch sends, so a client that reads it
	// draws the funded-tenant screen.
	w.Header().Set("anthropic-ratelimit-unified-status", "allowed")

	usage := map[string]any{"input_tokens": 1, "output_tokens": 1}
	replyText := Reply
	if plan != nil {
		usage = plan.nextUsage(body)
		if isSummary, _ := usage["summary"].(bool); isSummary {
			usage = map[string]any{"input_tokens": 1, "output_tokens": 80}
			replyText = SummaryReply
		}
	}

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
		start := contentBlockStart(toolCall)
		writeSSEEvent(w, flusher, "content_block_start", start)
		writeSSEEvent(w, flusher, "content_block_delta", contentBlockDelta(toolCall, replyText))
		writeSSEEvent(w, flusher, "content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
		writeSSEEvent(w, flusher, "message_delta", map[string]any{
			"type":  "message_delta",
			"delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil},
			"usage": usage,
		})
		writeSSEEvent(w, flusher, "message_stop", map[string]any{"type": "message_stop"})
		return
	}

	// The tool answer rides the same shapes the other dialects reply with, and
	// carries stop_reason tool_use in the non-streamed body: a harness reads
	// the stop reason to decide whether to run the tool and continue the turn.
	stopReason := "end_turn"
	if toolCall {
		stopReason = "tool_use"
	}

	content := []map[string]any{{"type": "text", "text": replyText}}
	if toolCall {
		content = []map[string]any{{"type": "tool_use", "id": "toolu_stub", "name": "stub_tool", "input": map[string]any{}}}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":          "msg_stub",
		"type":        "message",
		"role":        "assistant",
		"model":       "prizmal/stub",
		"content":     content,
		"stop_reason": stopReason,
		"usage":       usage,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// contentBlockStart is the content_block_start event's value: a tool_use block
// when the request asked for a tool, else the text block.
func contentBlockStart(toolCall bool) map[string]any {
	if toolCall {
		return map[string]any{
			"type":          "content_block_start",
			"index":         0,
			"content_block": map[string]any{"type": "tool_use", "id": "toolu_stub", "name": "stub_tool", "input": map[string]any{}},
		}
	}
	return map[string]any{
		"type":          "content_block_start",
		"index":         0,
		"content_block": map[string]any{"type": "text", "text": ""},
	}
}

// contentBlockDelta is the content_block_delta event's value: the tool input
// as a partial JSON delta when the request asked for a tool, else the reply
// as a text delta. replyText replaces the default reply, which the summary
// answer of a usage plan sets.
func contentBlockDelta(toolCall bool, replyText string) map[string]any {
	if toolCall {
		return map[string]any{
			"type":  "content_block_delta",
			"index": 0,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": "{}"},
		}
	}
	return map[string]any{
		"type":  "content_block_delta",
		"index": 0,
		"delta": map[string]any{"type": "text_delta", "text": replyText},
	}
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
