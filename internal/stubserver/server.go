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

// New returns a running httptest.Server that speaks all three dialects.
// Callers must defer Close it.
func New() *httptest.Server {
	return httptest.NewServer(handler())
}

func handler() http.HandlerFunc {
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
			handleModels(w)

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

func handleModels(w http.ResponseWriter) {
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
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"message": "streaming not supported"}})
			return
		}

		if toolCall {
			chunk := map[string]any{
				"choices": []map[string]any{{
					"delta": map[string]any{
						"tool_calls": []map[string]any{{
							"id":       "call_stub",
							"type":     "function",
							"function": map[string]any{"name": "stub_tool", "arguments": "{}"},
						}},
					},
				}},
			}
			writeSSE(w, flusher, chunk)
		} else {
			chunk := map[string]any{
				"choices": []map[string]any{{
					"delta": map[string]any{"role": "assistant", "content": "stub"},
				}},
			}
			writeSSE(w, flusher, chunk)
		}
		_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}

	message := map[string]any{"role": "assistant", "content": "stub"}
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

	output := []map[string]any{{
		"type": "message",
		"role": "assistant",
		"content": []map[string]any{{
			"type": "output_text",
			"text": "stub",
		}},
	}}

	if toolCall {
		output = []map[string]any{{
			"type":      "function_call",
			"id":        "call_stub",
			"name":      "stub_tool",
			"arguments": "{}",
		}}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":     "resp_stub",
		"object": "response",
		"status": "completed",
		"model":  "prizmal/stub",
		"output": output,
		"usage":  map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
	})
}

func handleMessages(w http.ResponseWriter, r *http.Request) {
	body := requestBody(r)
	toolCall := wantsToolCall(body)
	stream := wantsStream(body)

	if stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]any{"message": "streaming not supported"}})
			return
		}

		// message_start
		writeSSEEvent(w, flusher, "message_start", map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":    "msg_stub",
				"type":  "message",
				"role":  "assistant",
				"model": "prizmal/stub",
				"usage": map[string]any{"input_tokens": 1, "output_tokens": 0},
			},
		})

		// content_block_delta
		delta := map[string]any{"type": "text_delta", "text": "stub"}
		writeSSEEvent(w, flusher, "content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": 0,
			"delta": delta,
		})

		// message_stop
		writeSSEEvent(w, flusher, "message_stop", map[string]any{"type": "message_stop"})
		return
	}

	content := []map[string]any{{"type": "text", "text": "stub"}}
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
