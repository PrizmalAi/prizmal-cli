package claude

import (
	"encoding/json"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/internaltest"
	launch "github.com/PrizmalAi/prizmal-cli/internal/launcher"
	"github.com/PrizmalAi/prizmal-cli/internal/tiers"
)

// genericRows are the rows of a tenant whose Switch reports generic tiers: one
// config on frontier and one on flash.
func genericRows(t *testing.T) []launch.ModelRow {
	t.Helper()
	catalog, err := launch.ParseSwitchCatalog([]byte(`{"data":[
		{"id":"smart[1m]","tier":"frontier","tiers":["frontier"],"description":"Everyday"},
		{"id":"quick[1m]","tier":"flash","tiers":["flash"]},
		{"id":"sample-model-a[1m]"}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	return launch.ModelRowsFor(launch.LaunchModels("frontier", launch.WithTierRows(catalog), true), tiers.ClaudeCode)
}

// Claude Code resolves --model opus to its own id. The launch maps the id onto
// the frontier alias, so the session routes to the config that holds frontier.
func TestOpusRoutesToTheFrontierConfig(t *testing.T) {
	rows := genericRows(t)
	overrides := claudeOrderedModelOverrides(claudeHeldTiers(rows))
	if len(overrides) == 0 || overrides[0].key != "claude-opus-5" || overrides[0].value != "frontier[1m]" {
		t.Fatalf("first override = %+v, want claude-opus-5 -> frontier[1m]", overrides)
	}
	for _, o := range overrides {
		if o.value != "frontier[1m]" {
			t.Errorf("modelOverrides[%q] = %q, want only the frontier alias", o.key, o.value)
		}
	}
	env := claudeChildEnv("frontier", rows)
	if got := internaltest.EnvValue(env, "ANTHROPIC_DEFAULT_OPUS_MODEL="); got != "frontier[1m]" {
		t.Errorf("ANTHROPIC_DEFAULT_OPUS_MODEL = %q, want frontier[1m]", got)
	}
	if got := internaltest.EnvValue(env, "ANTHROPIC_DEFAULT_HAIKU_MODEL="); got != "" {
		t.Errorf("ANTHROPIC_DEFAULT_HAIKU_MODEL = %q, want it unset for the held flash tier", got)
	}
	if got := internaltest.EnvValue(env, "ANTHROPIC_DEFAULT_SONNET_MODEL="); got != "frontier[1m]" {
		t.Errorf("ANTHROPIC_DEFAULT_SONNET_MODEL = %q, want the launch model for the unheld core tier", got)
	}
}

// --model opus is a Claude Code word. Claude Code resolves it through
// ANTHROPIC_DEFAULT_OPUS_MODEL, which names the frontier alias.
func TestModelOpusLaunchesTheFrontierAlias(t *testing.T) {
	env := claudeChildEnv("opus", genericRows(t))
	if got := internaltest.EnvValue(env, "ANTHROPIC_DEFAULT_OPUS_MODEL="); got != "frontier[1m]" {
		t.Errorf("ANTHROPIC_DEFAULT_OPUS_MODEL = %q, want frontier[1m]", got)
	}
}

// The /model rows read Opus and Haiku, and send the generic alias.
func TestClaudePickerLabelsGenericRowsWithClaudeWords(t *testing.T) {
	settings, err := claudeSettingsJSON("frontier", genericRows(t))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Model       string `json:"model"`
		ModelPicker struct {
			Options []struct {
				Label, Model, BehavesAs, Description string
			} `json:"options"`
		} `json:"modelPicker"`
	}
	if err := json.Unmarshal([]byte(settings), &got); err != nil {
		t.Fatal(err)
	}
	if got.Model != "frontier[1m]" {
		t.Errorf("model = %q, want frontier[1m]", got.Model)
	}
	want := []struct{ label, model, behavesAs string }{
		{"Opus", "frontier[1m]", "claude-opus-5"},
		{"Haiku", "flash[1m]", "claude-sonnet-5"},
		{"sample-model-a", "sample-model-a[1m]", ""},
	}
	if len(got.ModelPicker.Options) != len(want) {
		t.Fatalf("options = %+v, want %d rows", got.ModelPicker.Options, len(want))
	}
	for i, w := range want {
		o := got.ModelPicker.Options[i]
		if o.Label != w.label || o.Model != w.model || o.BehavesAs != w.behavesAs {
			t.Errorf("option %d = %+v, want %+v", i, o, w)
		}
	}
}
