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
	return httptest.NewServer(handler(nil))
}

// NewWithModels returns a running stub server whose /v1/models lists exactly
// ids, each as text-only, in place of the default fixture. It is for tests
// that render a catalog, where the names on screen are the point.
func NewWithModels(ids ...string) *httptest.Server {
	models := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		models = append(models, map[string]any{
			"id":                id,
			"input_modalities":  []string{"text"},
			"output_modalities": []string{"text"},
		})
	}
	return httptest.NewServer(handler(models))
}

// Entry is one /v1/models entry for NewWithEntries. Tier and Description are
// sent only when set.
type Entry struct {
	ID          string
	Tier        string
	Description string
}

// NewWithEntries is NewWithModels for entries that carry a tier or a
// description.
func NewWithEntries(entries ...Entry) *httptest.Server {
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
	return httptest.NewServer(handler(models))
}

// handler serves the stub API. A nil models serves the default fixture.
func handler(models []map[string]any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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

func handleMessages(w http.ResponseWriter, r *http.Request) {
	body := requestBody(r)
	toolCall := wantsToolCall(body)
	stream := wantsStream(body)

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
