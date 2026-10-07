package launch

import (
	"reflect"
	"strings"
	"testing"
)

// piTestModel is the model these argument tests pass in. It stands for whatever
// model a launch resolved, which Pi selects as given: Pi substitutes no name of
// its own.
const piTestModel = "test-model"

// The launch must select the prizmal provider explicitly. pi honors
// settings.json's defaultProvider only when the provider entry passes its
// schema validation; an entry it rejects (an empty apiKey, say) is dropped
// and the bare launch falls through to pi's built-in provider — Anthropic —
// which is exactly the misroute `prizmal pi` shipped before this fix. These
// tests pin the command assembly directly.

func TestPiLaunchArgsInjectsProviderForBarePassthrough(t *testing.T) {
	got := piLaunchArgs(piTestModel, nil, []string{"--print", "Reply with the token"})
	want := []string{"--provider", piProviderID, "--model", piTestModel, "--print", "Reply with the token"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("piLaunchArgs bare passthrough = %v, want %v", got, want)
	}
}

func TestPiLaunchArgsInjectsForEmptyArgs(t *testing.T) {
	// The interactive launch passes no extra args at all; it must still
	// select the provider.
	got := piLaunchArgs(piTestModel, nil, nil)
	want := []string{"--provider", piProviderID, "--model", piTestModel}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("piLaunchArgs empty args = %v, want %v", got, want)
	}
}

func TestPiLaunchArgsDoesNotOverrideExplicitProviderFlag(t *testing.T) {
	extra := []string{"--provider", "anthropic", "--print", "hi"}
	got := piLaunchArgs(piTestModel, nil, extra)
	if !reflect.DeepEqual(got, extra) {
		t.Fatalf("piLaunchArgs with user --provider = %v, want passthrough %v", got, extra)
	}
}

func TestPiLaunchArgsDoesNotOverrideProviderEqualsForm(t *testing.T) {
	// pi rejects --provider=x at parse time, but the launcher must still
	// treat it as an explicit selection and pass it through untouched.
	extra := []string{"--provider=anthropic", "--print", "hi"}
	got := piLaunchArgs(piTestModel, nil, extra)
	if !reflect.DeepEqual(got, extra) {
		t.Fatalf("piLaunchArgs with --provider= = %v, want passthrough %v", got, extra)
	}
}

func TestPiLaunchArgsDoesNotOverrideProviderQualifiedModel(t *testing.T) {
	// A provider-qualified --model selects a provider in pi (prefix
	// resolution), so injecting would override the user's selection.
	extra := []string{"--model", "openai/gpt-5-mini", "--print", "hi"}
	got := piLaunchArgs(piTestModel, nil, extra)
	if !reflect.DeepEqual(got, extra) {
		t.Fatalf("piLaunchArgs with provider-qualified --model = %v, want passthrough %v", got, extra)
	}
}

func TestPiLaunchArgsIgnoresFlagsAfterSeparator(t *testing.T) {
	// pi's own `--` separator ends its option section; tokens after it are
	// files and message text, not flags. A message that merely mentions
	// --provider must not suppress the injection.
	extra := []string{"--", "--print", "explain the --provider flag"}
	got := piLaunchArgs(piTestModel, nil, extra)
	want := []string{"--provider", piProviderID, "--model", piTestModel, "--", "--print", "explain the --provider flag"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("piLaunchArgs with post-separator text = %v, want %v", got, want)
	}
}

func TestPiLaunchArgsUsesRequestedModel(t *testing.T) {
	// The model id must come from the same source Edit writes
	// (models[0].Name, plumbed through Run), not a second literal.
	got := piLaunchArgs("prizmal/custom-model", nil, []string{"--print", "hi"})
	want := []string{"--provider", piProviderID, "--model", "prizmal/custom-model", "--print", "hi"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("piLaunchArgs requested model = %v, want %v", got, want)
	}
}

// Pi substitutes no model of its own. A launch resolves one first, so an empty
// model here passes through as empty rather than becoming a reserved
// placeholder the Switch would have to interpret.
func TestPiLaunchArgsInjectsNoModelOfItsOwn(t *testing.T) {
	got := piLaunchArgs("", nil, []string{"--print", "hi"})
	want := []string{"--provider", piProviderID, "--model", "", "--print", "hi"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("piLaunchArgs empty model = %v, want %v", got, want)
	}
	for _, arg := range got {
		if strings.Contains(arg, "prizmal/default") {
			t.Fatalf("piLaunchArgs injected the sunset prizmal/default name: %v", got)
		}
	}
}

func TestPiProviderSymbolsMatchEditAndRestore(t *testing.T) {
	// Edit writes the provider under this id and Restore removes it; both
	// must use the same constant the launch injects, so a rename cannot
	// desynchronize the three.
	if piProviderID != piRestoredProviderID {
		t.Fatalf("piProviderID = %q but piRestoredProviderID = %q", piProviderID, piRestoredProviderID)
	}
}
