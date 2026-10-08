package stubserver

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// strictPaths are the inference routes the stub checks the model name on, each
// with a body that carries the given model.
var strictPaths = map[string]string{
	"/v1/responses":             `{"model":%q,"input":"hi"}`,
	"/v1/chat/completions":      `{"model":%q,"messages":[{"role":"user","content":"hi"}]}`,
	"/v1/messages":              `{"model":%q,"max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`,
	"/v1/messages/count_tokens": `{"model":%q,"messages":[{"role":"user","content":"hi"}]}`,
}

func post(t *testing.T, srv string, path, model string) *http.Response {
	t.Helper()
	body := strings.Replace(strictPaths[path], "%q", `"`+model+`"`, 1)
	return postWithAuth(t, srv+path, body)
}

func TestUnknownModelIsRefusedOnEveryInferenceRoute(t *testing.T) {
	srv := NewWithModels("prizmal-flash")
	defer srv.Close()

	for path := range strictPaths {
		resp := post(t, srv.URL, path, "gpt-5.6-luna")
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", path, resp.StatusCode)
		}
		raw, _ := json.Marshal(body)
		if !strings.Contains(string(raw), "model_not_found") || !strings.Contains(string(raw), "gpt-5.6-luna") {
			t.Errorf("%s: error does not name the code and the model: %s", path, raw)
		}
	}
}

func TestListedModelIsServedWithOrWithoutTheMillionSuffix(t *testing.T) {
	srv := NewWithModels("prizmal-flash")
	defer srv.Close()

	for _, name := range []string{"prizmal-flash", "prizmal-flash[1m]"} {
		for path := range strictPaths {
			resp := post(t, srv.URL, path, name)
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("%s %s: got %d, want 200", path, name, resp.StatusCode)
			}
		}
	}
}

func TestDefaultIsNotAName(t *testing.T) {
	srv := NewWithModels("prizmal-flash")
	defer srv.Close()

	resp := post(t, srv.URL, "/v1/responses", "default")
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", resp.StatusCode)
	}
}

func TestEmptyModelIsRefused(t *testing.T) {
	srv := NewWithModels("prizmal-flash")
	defer srv.Close()

	resp := postWithAuth(t, srv.URL+"/v1/responses", `{"input":"hi"}`)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", resp.StatusCode)
	}
}

func TestTierAliasesAnswerOnlyForAHeldTier(t *testing.T) {
	srv := NewWithEntries(
		Entry{ID: "prizmal-flash", Tier: "haiku"},
		Entry{ID: "prizmal-core"},
	)
	defer srv.Close()

	cases := map[string]int{
		"claude-tier-haiku":         http.StatusOK,
		"claude-haiku-4-5-20251001": http.StatusOK, // the Switch maps a vendor id to its held tier
		"claude-tier-opus":          http.StatusBadRequest,
		"claude-opus-4-7":           http.StatusBadRequest,
		"claude-sonnet-5-5":         http.StatusBadRequest,
	}
	for name, want := range cases {
		resp := post(t, srv.URL, "/v1/messages", name)
		_ = resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("%s: got %d, want %d", name, resp.StatusCode, want)
		}
	}
}

func TestRecorderKeepsEveryModelWithItsVerdict(t *testing.T) {
	rec := &Recorder{}
	srv := NewServer(WithModels("prizmal-flash"), WithRecorder(rec))
	defer srv.Close()

	for _, call := range []struct{ path, model string }{
		{"/v1/responses", "prizmal-flash"},
		{"/v1/messages", "nope"},
	} {
		resp := post(t, srv.URL, call.path, call.model)
		_ = resp.Body.Close()
	}
	got := rec.ModelCalls()
	if len(got) != 2 {
		t.Fatalf("recorded %d calls, want 2: %+v", len(got), got)
	}
	if got[0].Model != "prizmal-flash" || !got[0].Served || got[0].Path != "/v1/responses" {
		t.Errorf("first call = %+v", got[0])
	}
	if got[1].Model != "nope" || got[1].Served {
		t.Errorf("second call = %+v", got[1])
	}
	if refused := rec.RefusedModels(); len(refused) != 1 || refused[0] != "nope" {
		t.Errorf("RefusedModels = %v, want [nope]", refused)
	}
}
