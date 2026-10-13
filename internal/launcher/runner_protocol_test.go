package launch

import (
	"reflect"
	"testing"
)

// runnerPosture is what each harness declares about itself: whether
// `prizmal --restore <harness>` can take its configuration back, whether it
// writes a config file of its own, and whether it carries a key on the child
// environment instead of on disk.
//
// The table exists so a capability cannot be invented on one adapter and read
// by nobody. Codex carried SkipRestoreInstallCheck for a year on exactly that
// footing; here a runner that claims restore has to satisfy Restorer, and a
// runner whose posture changes has to change this table with it.
type runnerPosture struct {
	restorable bool
	editor     bool
	// keyOnEnvironment records where the Switch key travels. It is the reason
	// restore is unevenly supported, so it is written down next to the
	// restorable flag it explains rather than left to each adapter's prose.
	keyOnEnvironment bool
}

var runnerPostures = map[string]runnerPosture{
	// claude: fully ephemeral — ANTHROPIC_BASE_URL and ANTHROPIC_AUTH_TOKEN
	// ride the child environment and nothing is written, so there is nothing
	// to undo. It is also the one runner that shows its own model list and
	// owns its --model, because Claude Code treats a flag model as its own
	// picker choice.
	"claude": {restorable: false, editor: false, keyOnEnvironment: true},
	// codex: the key travels as OPENAI_API_KEY on the child environment and
	// the generated profile names it via env_key, so restore only has to take
	// out the profile and the model catalog it wrote. It is not an Editor:
	// config.toml is written by ensureCodexConfig inside Run, which is why
	// `--persist codex` reports no standalone configuration.
	"codex": {restorable: true, editor: false, keyOnEnvironment: true},
	// cline: providers.json carries the provider entry and, on a legacy
	// install, the live key. cline's openai-compatible provider reads
	// OPENAI_API_KEY when the settings hold no apiKey, so the key can stay on
	// the environment — but the entry itself is on disk and needs the undo.
	"cline": {restorable: true, editor: true, keyOnEnvironment: true},
	// opencode: OPENCODE_CONFIG_CONTENT carries the config at launch, but Edit
	// also writes ~/.local/state/opencode/model.json through
	// fileutil.WriteWithBackup and a search tool under ~/.cache/prizmal/.
	// Neither is reachable from --restore, so this harness has no Restorer.
	// That gap is tracked separately; it is a product question, not a gap
	// this table papers over.
	"opencode": {restorable: false, editor: true, keyOnEnvironment: true},
	// pi: models.json carries the provider entry and, on a legacy install, the
	// live key. pi reads the key from the environment when the entry names
	// none, so the entry is the only thing written that restore must remove.
	"pi": {restorable: true, editor: true, keyOnEnvironment: true},
}

// TestRunnerProtocolCompleteness walks the registry and fails on any runner
// that disagrees with its declared posture. Adding a harness to the registry
// without saying anything about restore is the failure this catches: the entry
// has no row, and a row nobody reviewed is a capability nothing checks.
func TestRunnerProtocolCompleteness(t *testing.T) {
	specs := ListAllIntegrationSpecs()
	if len(specs) == 0 {
		t.Fatal("registry returned no integrations")
	}
	for _, spec := range specs {
		want, ok := runnerPostures[spec.Name]
		if !ok {
			t.Errorf("integration %q has no entry in runnerPostures: say whether it is restorable", spec.Name)
			continue
		}

		if _, ok := spec.Runner.(Restorer); ok != want.restorable {
			t.Errorf("integration %q implements Restorer = %v, want %v", spec.Name, ok, want.restorable)
		}
		if _, ok := spec.Runner.(Editor); ok != want.editor {
			t.Errorf("integration %q implements Editor = %v, want %v", spec.Name, ok, want.editor)
		}
		// Every runner answers for its own installation; without this the
		// registry cannot tell a missing binary from a present one and skips
		// the install prompt for everyone.
		if _, ok := spec.Runner.(Installed); !ok {
			t.Errorf("integration %q does not implement Installed", spec.Name)
		}
		// A runner that restores must also be able to report what it did:
		// RestoreOutcome alone cannot distinguish "nothing was configured"
		// from "your previous settings are back".
		if _, ok := spec.Runner.(RestoreMesager); ok != want.restorable {
			t.Errorf("integration %q implements RestoreMesager = %v, want %v", spec.Name, ok, want.restorable)
		}
	}
	for name := range runnerPostures {
		if _, err := LookupIntegrationSpec(name); err != nil {
			t.Errorf("runnerPostures entry %q has no matching registry integration", name)
		}
	}
}

// TestNoHypotheticalSeams fails when a runner exports a method that no part of
// the protocol declares. An exported method on a Runner is an offer, and an
// offer with one provider and no reader is the shape Codex.SkipRestoreInstallCheck
// had for a year: it looked like an extension point and nothing could observe
// it. A new capability must arrive with its interface and a reader in the same
// commit, which is what this test asks for.
func TestNoHypotheticalSeams(t *testing.T) {
	protocol := make(map[string]bool)
	for _, iface := range []reflect.Type{
		reflect.TypeOf((*Runner)(nil)).Elem(),
		reflect.TypeOf((*SupportedIntegration)(nil)).Elem(),
		reflect.TypeOf((*ModelListShower)(nil)).Elem(),
		reflect.TypeOf((*OwningModelFlag)(nil)).Elem(),
		reflect.TypeOf((*Editor)(nil)).Elem(),
		reflect.TypeOf((*Restorer)(nil)).Elem(),
		reflect.TypeOf((*RestoreMesager)(nil)).Elem(),
		reflect.TypeOf((*Installed)(nil)).Elem(),
		// DeviceModeRunner is part of the protocol: cmd/prizmal's device login
		// asks a runner whether it can refresh its own credential, so its
		// method has two readers and a declared interface.
		reflect.TypeOf((*DeviceModeRunner)(nil)).Elem(),
	} {
		for i := range iface.NumMethod() {
			protocol[iface.Method(i).Name] = true
		}
	}

	for _, spec := range ListAllIntegrationSpecs() {
		runner := reflect.ValueOf(spec.Runner)
		typ := runner.Type()
		for i := range typ.NumMethod() {
			name := typ.Method(i).Name
			if protocol[name] {
				continue
			}
			t.Errorf("%s.%s is an exported method outside the runner protocol: "+
				"declare it on an interface with a reader, or make it unexported",
				typ, name)
		}
	}
}
