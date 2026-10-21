package cline

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// SearchTimeout bounds one search, so a stalled Switch answers Cline with
// an error instead of holding the tool call open.
const SearchTimeout = 2 * time.Minute

// clineSearchInstruction is the prompt each query is wrapped in.
const clineSearchInstruction = "Search the web and answer with a short factual summary followed by the source URLs.\n\nQuery: "

// clineSearchTool is the one tool the server lists.
var clineSearchTool = map[string]any{
	"name": clineSearchToolName,
	"description": "Search the web through the Prizmal Switch. Returns a summary of current sources with their URLs. " +
		"Use it for anything that postdates the model's training data, or that needs a citable source.",
	"inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string", "description": "What to search the web for."},
		},
		"required": []any{"query"},
	},
}

type rpcMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ServeSearch runs the MCP stdio server behind `prizmal mcp search`. It reads
// newline-delimited JSON-RPC from in and writes one line per answer to out,
// until in closes. The Switch's /v1 base and the model come from
// PRIZMAL_CLINE_SEARCH_URL and PRIZMAL_CLINE_SEARCH_MODEL, and the key from
// OPENAI_API_KEY, which the server inherits from Cline.
func ServeSearch(ctx context.Context, in io.Reader, out io.Writer, getenv func(string) string, client *http.Client) error {
	if client == nil {
		client = &http.Client{Timeout: SearchTimeout}
	}
	var mu sync.Mutex
	send := func(id json.RawMessage, result any, rerr *rpcError) {
		msg := map[string]any{"jsonrpc": "2.0", "id": id}
		if rerr != nil {
			msg["error"] = rerr
		} else {
			msg["result"] = result
		}
		data, _ := json.Marshal(msg)
		mu.Lock()
		defer mu.Unlock()
		_, _ = out.Write(append(data, '\n'))
	}

	var calls sync.WaitGroup
	defer calls.Wait()
	lines := bufio.NewScanner(in)
	lines.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for lines.Scan() {
		line := bytes.TrimSpace(lines.Bytes())
		if len(line) == 0 {
			continue
		}
		var msg rpcMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			send(json.RawMessage("null"), nil, &rpcError{Code: -32700, Message: "parse error"})
			continue
		}
		isRequest := len(msg.ID) > 0 && string(msg.ID) != "null"
		switch msg.Method {
		case "initialize":
			var params struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			if params.ProtocolVersion == "" {
				params.ProtocolVersion = "2025-06-18"
			}
			send(msg.ID, map[string]any{
				"protocolVersion": params.ProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": clineSearchServerName, "version": "1"},
			}, nil)
		case "ping":
			if isRequest {
				send(msg.ID, map[string]any{}, nil)
			}
		case "tools/list":
			send(msg.ID, map[string]any{"tools": []any{clineSearchTool}}, nil)
		case "tools/call":
			var params struct {
				Name      string `json:"name"`
				Arguments struct {
					Query string `json:"query"`
				} `json:"arguments"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			if params.Name != clineSearchToolName {
				send(msg.ID, nil, &rpcError{Code: -32602, Message: "unknown tool: " + params.Name})
				continue
			}
			// A search takes seconds; answer it off the read loop so a ping
			// or a second call is not stuck behind it.
			calls.Add(1)
			go func(id json.RawMessage, query string) {
				defer calls.Done()
				send(id, clineSearch(ctx, client, getenv, query), nil)
			}(msg.ID, params.Arguments.Query)
		default:
			// Notifications such as notifications/initialized get no answer.
			if isRequest {
				send(msg.ID, nil, &rpcError{Code: -32601, Message: "method not found: " + msg.Method})
			}
		}
	}
	return lines.Err()
}

func toolText(text string, isError bool) map[string]any {
	result := map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}
	if isError {
		result["isError"] = true
	}
	return result
}

// clineSearch posts one query to the Switch's /responses with web_search and
// returns the tool result Cline shows the model.
func clineSearch(ctx context.Context, client *http.Client, getenv func(string) string, query string) map[string]any {
	base := strings.TrimRight(strings.TrimSpace(getenv(clineSearchURLEnv)), "/")
	if base == "" {
		return toolText("prizmal search is unconfigured: "+clineSearchURLEnv+" is unset. Relaunch through the prizmal CLI.", true)
	}
	if strings.TrimSpace(query) == "" {
		return toolText("prizmal search needs a non-empty query.", true)
	}
	body, _ := json.Marshal(map[string]any{
		"model": getenv(clineSearchModelEnv),
		"tools": []any{map[string]any{"type": "web_search"}},
		"input": clineSearchInstruction + query,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/responses", bytes.NewReader(body))
	if err != nil {
		return toolText("prizmal search: "+err.Error(), true)
	}
	req.Header.Set("Content-Type", "application/json")
	if key := getenv("OPENAI_API_KEY"); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := client.Do(req)
	if err != nil {
		return toolText("prizmal search could not reach the Switch: "+err.Error(), true)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		text := fmt.Sprintf("prizmal search: the Switch answered %d on /responses.", resp.StatusCode)
		if len(detail) > 0 {
			text += " Response: " + string(detail)
		}
		return toolText(text, true)
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return toolText("prizmal search: the Switch answered with a body that is not JSON.", true)
	}
	text := collectResponseText(payload)
	if text == "" {
		text = "No results for that query."
	}
	return toolText(text, false)
}

// collectResponseText reads a Responses API payload's text: output_text when
// the Switch sets it, else every text chunk of every output item.
func collectResponseText(payload map[string]any) string {
	if text, _ := payload["output_text"].(string); text != "" {
		return text
	}
	var parts []string
	items, _ := payload["output"].([]any)
	for _, item := range items {
		m, _ := item.(map[string]any)
		chunks, _ := m["content"].([]any)
		for _, chunk := range chunks {
			c, _ := chunk.(map[string]any)
			if text, _ := c["text"].(string); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n\n")
}
