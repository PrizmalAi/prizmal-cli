package pi

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The turn-charge handlers run in Pi, which a unit test does not launch. The
// test drives the extension's handlers with node's built-in TypeScript
// stripping, against a stand-in for Pi's extension API that records them, and
// replays the events Pi emits for a turn. It skips without a node that strips
// types.

const chargeHarness = `
const mod = await import(process.argv[2]);
const handlers = {};
const pi = { on: (name, fn) => { (handlers[name] ||= []).push(fn); }, registerProvider() {} };
mod.default(pi);
const emit = async (name, event) => {
  let out;
  for (const fn of handlers[name] || []) { out = (await fn(event)) ?? out; }
  return out;
};
const turns = JSON.parse(process.argv[3]);
const results = [];
for (const turn of turns) {
  await emit("message_start", { message: { role: "assistant" } });
  for (const data of turn.chunks) {
    await emit("provider_stream_event", { provider: turn.provider ?? "prizmal", data });
  }
  const message = { role: "assistant", provider: turn.provider ?? "prizmal",
    usage: { input: 1, cost: { input: 0, output: 0, total: 0 } } };
  const out = await emit("message_end", { message });
  results.push(out ? out.message.usage.cost.total : null);
}
console.log(JSON.stringify(results));
`

func runChargeHarness(t *testing.T, turns any) []*float64 {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	harness := filepath.Join(dir, "harness.mjs")
	if err := os.WriteFile(harness, []byte(chargeHarness), 0o600); err != nil {
		t.Fatal(err)
	}
	ext := filepath.Join(dir, "prizmal-device.ts")
	if err := os.WriteFile(ext, piDeviceExtensionSource, 0o600); err != nil {
		t.Fatal(err)
	}
	arg, _ := json.Marshal(turns)
	out, err := exec.Command(node, "--experimental-strip-types", "--no-warnings", harness, ext, string(arg)).CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "strip-types") || strings.Contains(string(out), "bad option") {
			t.Skipf("node cannot strip types: %s", out)
		}
		t.Fatalf("run the extension: %v\n%s", err, out)
	}
	var got []*float64
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &got); err != nil {
		t.Fatalf("parse %q: %v", out, err)
	}
	return got
}

type chargeTurn struct {
	Provider string `json:"provider,omitempty"`
	Chunks   []any  `json:"chunks"`
}

func TestTurnChargeReplacesMessageCost(t *testing.T) {
	got := runChargeHarness(t, []chargeTurn{
		// pico is exact and wins over the float cost
		{Chunks: []any{
			map[string]any{"choices": []any{}},
			map[string]any{"usage": map[string]any{"cost": 0.0482100000001, "charged_total_pico": 48210000000}},
		}},
		// only the float cost
		{Chunks: []any{map[string]any{"usage": map[string]any{"cost": 0.25}}}},
		// a tool step carries no charge, and the previous turn's must not leak in
		{Chunks: []any{map[string]any{"usage": map[string]any{"prompt_tokens": 5}}}},
		// a free tenant: no charge fields
		{Chunks: []any{map[string]any{"choices": []any{}}}},
		// another provider's stream keeps Pi's own pricing
		{Provider: "anthropic", Chunks: []any{map[string]any{"usage": map[string]any{"cost": 9.0}}}},
	})
	want := []float64{0.04821, 0.25, -1, -1, -1}
	if len(got) != len(want) {
		t.Fatalf("got %d results, want %d", len(got), len(want))
	}
	for i, w := range want {
		switch {
		case w < 0 && got[i] != nil:
			t.Errorf("turn %d: cost set to %v, want left alone", i, *got[i])
		case w >= 0 && (got[i] == nil || *got[i] != w):
			t.Errorf("turn %d: cost = %v, want %v", i, got[i], w)
		}
	}
}

// TestChargeExtensionProviderMatchesLaunch keeps the extension's provider id
// equal to the one the launch selects.
func TestChargeExtensionProviderMatchesLaunch(t *testing.T) {
	want := `const switchProviderID = "` + piProviderID + `";`
	if !strings.Contains(string(piDeviceExtensionSource), want) {
		t.Fatalf("extension lacks %s", want)
	}
}
