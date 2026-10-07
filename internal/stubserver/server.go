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
	deviceOnly     bool
	issuer         *DeviceTokenIssuer
	toolLoop       *piToolLoop
	usagePlan      *UsagePlan
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
//
// An issuer takes precedence over WithDeviceTokenOnly: the refresh endpoint
// hands out the issuer's tokens and never the fixed DeviceToken, and the stub
// accepts only the issuer's unexpired tokens, so the fixed one is refused.
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
	// overflowLimit, when above zero, makes the stub refuse a request whose
	// planned usage passes it, with a 400 carrying overflowMessage.
	overflowLimit   int
	overflowMessage string
}

// NewUsagePlan builds a plan whose input_tokens follow values turn by turn.
func NewUsagePlan(inputTokens ...int) *UsagePlan {
	return &UsagePlan{inputTokens: inputTokens}
}

// OverflowAbove makes the stub refuse, with HTTP 400 invalid_request_error, a
// request whose planned input_tokens passes limit, the way an endpoint refuses
// a prompt past its window. In message, {tokens} stands for the planned count
// and {limit} for limit, so a test can spell the refusal the way a provider
// does or in a shape no client recognizes. A compaction request is never
// refused: the stub answers it with the summary. It returns the plan.
func (p *UsagePlan) OverflowAbove(limit int, message string) *UsagePlan {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.overflowLimit = limit
	p.overflowMessage = message
	return p
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

// nextUsage records the request body and answers the plan's next turn: the
// message id, which counts the request up, and the usage. The last plan
// value repeats once the list runs out, so a conversation whose compaction
// request arrives crosses the threshold on the turn the plan aims at and
// stays past it afterward.
//
// The id is per request, spelled with the request's count: deterministic,
// and distinct between replies. A client that anchors on a reply's usage
// groups the conversation by that id, and one shared id would read as one
// long turn.
//
// A request whose messages carry the compaction instructions ("CRITICAL:
// Respond with TEXT ONLY", tQt in the 2.1.x binaries) is answered with an
// analysis and a summary instead of the plan's reply: the compaction needs a
// summary it can parse, and the shape it asks for is the one it parses. The
// summary's text is distinctive so a test can find the compaction's answer.
func (p *UsagePlan) nextUsage(body map[string]any) (string, map[string]any) {
	raw, _ := json.Marshal(body)
	p.mu.Lock()
	p.bodies = append(p.bodies, string(raw))
	n := p.received
	p.received++
	p.mu.Unlock()

	if strings.Contains(string(raw), "CRITICAL: Respond with TEXT ONLY") {
		return fmt.Sprintf("msg_stub_compact_%d", n), map[string]any{"input_tokens": 1, "output_tokens": 80, "summary": true}
	}

	tokens := 1
	if len(p.inputTokens) > 0 {
		tokens = p.inputTokens[len(p.inputTokens)-1]
		if n < len(p.inputTokens) {
			tokens = p.inputTokens[n]
		}
	}
	if p.overflowLimit > 0 && tokens > p.overflowLimit {
		message := strings.NewReplacer(
			"{tokens}", fmt.Sprint(tokens),
			"{limit}", fmt.Sprint(p.overflowLimit),
		).Replace(p.overflowMessage)
		return "", map[string]any{"overflow": message}
	}
	return fmt.Sprintf("msg_stub_%d", n), map[string]any{"input_tokens": tokens, "output_tokens": 1}
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
			handleMessages(w, r, c.usagePlan)

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

	// Each served reply carries its own message id. A client that anchors on
	// a reply's usage (Claude Code's token counter) groups the conversation
	// by the reply id, and two replies with one id read as one long turn.
	id := "msg_stub"
	usage := map[string]any{"input_tokens": 1, "output_tokens": 1}
	replyText := Reply
	if plan != nil {
		id, usage = plan.nextUsage(body)
		if message, ok := usage["overflow"].(string); ok {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"type":  "error",
				"error": map[string]any{"type": "invalid_request_error", "message": message},
			})
			return
		}
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
				"id":            id,
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
		"id":          id,
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
