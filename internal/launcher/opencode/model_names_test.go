package opencode

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/PrizmalAi/prizmal-cli/internal/stubserver"
)

// OpenCode names a model in more places than its `model` setting. The ones a
// launch can reach, read from OpenCode 1.19.3:
//
//   - model: the session model, set from the launch.
//   - small_model: the title of a new session. Unset, OpenCode looks in the
//     provider for a model of a small family (gemini-flash, gpt-nano,
//     claude-haiku). A launch's entries carry no family, so it finds none and
//     the title runs on the session model.
//   - an agent's own model: none is set, so every agent runs the session model.
//   - the picker: it lists the provider's models, which are the launch's.

// namesCatalog is the tenant: three router configs and no Claude tier.
var namesCatalog = []string{"prizmal-flash", "prizmal-frontier", "prizmal-core"}

// The inline config names only the launch's models.
func TestOpenCodeInlineConfigNamesOnlyTheLaunchsModels(t *testing.T) {
	models := []launch.LaunchModel{{Name: "prizmal-flash"}, {Name: "prizmal-core"}}
	content, err := buildInlineConfig(models[0], models)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(content), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["model"] != "prizmal/prizmal-flash" {
		t.Errorf("model = %v, want prizmal/prizmal-flash", cfg["model"])
	}
	for _, field := range []string{"small_model", "agent", "mode"} {
		if _, set := cfg[field]; set {
			t.Errorf("the config sets %s, which can name a model the tenant did not list", field)
		}
	}
	entries := opencodePrizmalProvider(t, models[0], models)["models"].(map[string]any)
	for name, entry := range entries {
		if !slices.Contains(launch.LaunchModelNames(models), name) {
			t.Errorf("the provider lists %q, which is not one of the launch's models", name)
		}
		if _, set := entry.(map[string]any)["family"]; set {
			t.Errorf("entry %q carries a family, which would make it OpenCode's small model", name)
		}
	}
}

// A real OpenCode one-shot run against a stub that refuses any name it does
// not route: every request, the title included, names the session model.
func TestOpenCodeRunSendsOnlyRoutableNames(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the OpenCode launch in short mode")
	}
	if _, err := exec.LookPath("opencode"); err != nil {
		if os.Getenv("PRIZMAL_HARNESS_LAUNCH") == "require" {
			t.Fatal("opencode is not on PATH")
		}
		t.Skip("opencode is not on PATH")
	}
	sandboxedHome(t)
	t.Chdir(t.TempDir())
	for _, name := range []string{"OPENCODE_CONFIG", "OPENCODE_CONFIG_CONTENT", "XDG_STATE_HOME", "XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME"} {
		unsetEnv(t, name)
	}

	rec := &stubserver.Recorder{}
	srv := stubserver.NewServer(stubserver.WithModels(namesCatalog...), stubserver.WithRecorder(rec))
	defer srv.Close()
	envconfig.SetBaseURL(srv.URL)
	envconfig.SetAPIKey(stubserver.StubKey)
	t.Cleanup(func() { envconfig.SetBaseURL(""); envconfig.SetAPIKey("") })

	models := []launch.LaunchModel{{Name: "prizmal-flash"}, {Name: "prizmal-core"}, {Name: "prizmal-frontier"}}
	o := &OpenCode{}
	if err := o.Edit(models); err != nil {
		t.Fatal(err)
	}
	if err := o.Run("prizmal-flash", models, []string{"run", "hi"}); err != nil {
		t.Fatalf("opencode run: %v", err)
	}

	calls := rec.ModelCalls()
	if len(calls) == 0 {
		t.Fatal("OpenCode sent the stub no inference request")
	}
	var sent []string
	for _, call := range calls {
		sent = append(sent, call.Path+" "+call.Model)
		if call.Model != "prizmal-flash" {
			t.Errorf("a request named %q, want the session model prizmal-flash", call.Model)
		}
	}
	if refused := rec.RefusedModels(); len(refused) > 0 {
		t.Errorf("OpenCode sent names the Switch would refuse: %q (all: %s)", refused, strings.Join(sent, ", "))
	}
}

// Two projects under one home launch with different models. Each run's
// requests name only its own model, and the launch leaves the operator's
// model.json exactly as it found it.
func TestOpenCodeRunsInTwoProjectsKeepTheirModelsAndLeaveState(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the OpenCode launch in short mode")
	}
	if _, err := exec.LookPath("opencode"); err != nil {
		if os.Getenv("PRIZMAL_HARNESS_LAUNCH") == "require" {
			t.Fatal("opencode is not on PATH")
		}
		t.Skip("opencode is not on PATH")
	}
	home := sandboxedHome(t)
	for _, name := range []string{"OPENCODE_CONFIG", "OPENCODE_CONFIG_CONTENT", "XDG_STATE_HOME", "XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME"} {
		unsetEnv(t, name)
	}

	rec := &stubserver.Recorder{}
	srv := stubserver.NewServer(stubserver.WithModels("smart", "flash"), stubserver.WithRecorder(rec))
	defer srv.Close()
	envconfig.SetBaseURL(srv.URL)
	envconfig.SetAPIKey(stubserver.StubKey)
	t.Cleanup(func() { envconfig.SetBaseURL(""); envconfig.SetAPIKey("") })

	// The operator's picker state, which a launch must not touch.
	statePath := filepath.Join(home, ".local", "state", "opencode", "model.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, []byte(`{"recent":[{"providerID":"other","modelID":"theirs"}],"favorite":[],"variant":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot := func() []byte {
		data, err := os.ReadFile(statePath)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			t.Fatal(err)
		}
		return data
	}

	models := []launch.LaunchModel{{Name: "smart"}, {Name: "flash"}}
	for _, model := range []string{"smart", "flash"} {
		t.Chdir(t.TempDir())
		before := snapshot()
		seen := len(rec.ModelCalls())

		o := &OpenCode{}
		if err := o.Edit(models); err != nil {
			t.Fatal(err)
		}
		if err := o.Run(model, models, []string{"run", "hi"}); err != nil {
			t.Fatalf("opencode run -m %s: %v", model, err)
		}

		calls := rec.ModelCalls()[seen:]
		if len(calls) == 0 {
			t.Fatalf("-m %s: OpenCode sent the stub no inference request", model)
		}
		for _, call := range calls {
			if call.Model != model {
				t.Errorf("-m %s: a request named %q", model, call.Model)
			}
		}
		if after := snapshot(); !bytes.Equal(before, after) {
			t.Errorf("-m %s: the launch changed model.json\nbefore: %s\nafter:  %s", model, before, after)
		}
	}
}
