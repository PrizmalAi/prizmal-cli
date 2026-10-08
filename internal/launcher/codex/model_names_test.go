package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

// Codex names a model in more places than its `model` setting. Each slot below
// either follows the session model or takes a name from the launch, so a
// launch can only send a name the tenant routes. The slots were read from
// Codex 0.160 (codex-rs at tag rust-v0.160.0):
//
//   - model, review_model, agents.default_subagent_model: set from the launch.
//   - compaction, the auto reviewer, spawned subagents and /review: the session
//     model, unless a setting above names another. The auto reviewer prefers
//     the provider's own model id only when the catalog lists it, and falls
//     back to the session model, so a catalog without it never sends it.
//   - memories.extract_model and memories.consolidation_model: Codex's
//     provider defaults are OpenAI ids, sent whether or not the catalog lists
//     them, so the launch pins both.
//   - the thread title, realtime and image models: gated on the OpenAI
//     provider or ChatGPT sign-in, which a launch never has.

func TestCodexArgsPinMemoryModelsToTheLaunchModel(t *testing.T) {
	launch.SetSubagentModel("")
	args, err := (&Codex{}).args("prizmal-flash", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`memories.extract_model="prizmal-flash"`,
		`memories.consolidation_model="prizmal-flash"`,
	} {
		if !codexHasOverride(args, want) {
			t.Errorf("args lack -c %s: %v", want, args)
		}
	}
}

// The catalog is the list Codex checks a requested model against, and the
// only place the launch writes a model name for it to send. It holds the
// launch's own names and nothing else.
func TestCodexCatalogNamesOnlyTheLaunchsModels(t *testing.T) {
	launch.SetSubagentModel("prizmal-core")
	t.Cleanup(func() { launch.SetSubagentModel("") })
	fakeCodexBundle(t, fakeCodexCatalog)

	models := []launch.LaunchModel{{Name: "prizmal-flash"}, {Name: "prizmal-core"}, {Name: "prizmal-frontier"}}
	path := filepath.Join(t.TempDir(), "catalog.json")
	if err := writeCodexModelCatalog(path, codexCatalogModels("prizmal-flash", models)); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatal(err)
	}
	allowed := []string{"prizmal-flash", "prizmal-core", "prizmal-frontier"}
	for _, entry := range catalog.Models {
		slug, _ := entry["slug"].(string)
		if !slices.Contains(allowed, slug) {
			t.Errorf("catalog lists %q, which is not one of the launch's models", slug)
		}
		// These fields name another model. The launch never sets them, so
		// Codex never redirects a request to the model they would name.
		for _, field := range []string{"auto_review_model_override", "upgrade", "availability_nux"} {
			if _, set := entry[field]; set {
				t.Errorf("entry %q sets %s, which names a model the tenant did not list", slug, field)
			}
		}
	}
	if strings.Contains(string(raw), "gpt-") && !strings.Contains(string(fakeCodexCatalog), "gpt-") {
		t.Errorf("the catalog carries an OpenAI model id the bundled prompt did not")
	}
}

// An operator who passes their own memory model keeps it.
func TestCodexArgsKeepAnOperatorsMemoryModel(t *testing.T) {
	launch.SetSubagentModel("")
	args, err := (&Codex{}).args("prizmal-flash", "", []string{"-c", `memories.extract_model="mine"`})
	if err != nil {
		t.Fatal(err)
	}
	if codexHasOverride(args, `memories.extract_model="prizmal-flash"`) {
		t.Errorf("the launch replaced the operator's memories.extract_model: %v", args)
	}
	if !codexHasOverride(args, `memories.consolidation_model="prizmal-flash"`) {
		t.Errorf("the launch dropped the pin the operator did not set: %v", args)
	}
}
