package tiers

import "testing"

// Each harness labels the four generic tiers with its own words, and the
// generic name is what a launch sends.
func TestWordsLabelEachTierPerHarness(t *testing.T) {
	for _, tc := range []struct {
		vocab Vocabulary
		want  []string
	}{
		{ClaudeCode, []string{"Opus", "Sonnet", "Haiku", "Fable"}},
		{Codex, []string{"Sol", "Terra", "Luna", "Astra"}},
		{Generic, []string{"frontier", "core", "flash", "premier"}},
	} {
		for i, tier := range All {
			if got := tc.vocab.Word(tier); got != tc.want[i] {
				t.Errorf("%s.Word(%s) = %q, want %q", tc.vocab, tier, got, tc.want[i])
			}
		}
	}
	if got := ConfigNames.Word(Frontier); got != "" {
		t.Errorf("ConfigNames.Word(frontier) = %q, want no word", got)
	}
}

// Codex names a tier row after its config and says the tier in the
// description, so a row never reads as an OpenAI model.
func TestCodexLabelsTierRowsByConfig(t *testing.T) {
	if !Codex.LabelsByConfig() || ClaudeCode.LabelsByConfig() {
		t.Error("want only Codex to label tier rows by config")
	}
	if got := Codex.Like(Frontier); got != "Sol-like" {
		t.Errorf("Codex.Like(frontier) = %q, want Sol-like", got)
	}
}

func TestParseAcceptsOnlyTheGenericNames(t *testing.T) {
	for in, want := range map[string]Tier{"frontier": Frontier, " Core ": Core, "FLASH": Flash, "premier": Premier, "opus": "", "smart": "", "genius": "", "sol": ""} {
		got, ok := Parse(in)
		if got != want || ok != (want != "") {
			t.Errorf("Parse(%q) = %q, %v, want %q", in, got, ok, want)
		}
	}
}

// A Codex user may type the row's word. It maps back to the alias, which is
// the name the Switch routes.
func TestFromWordMapsAHarnessWordToItsTier(t *testing.T) {
	if got, ok := Codex.FromWord("sol"); !ok || got != Frontier {
		t.Errorf("Codex.FromWord(sol) = %q, %v, want frontier", got, ok)
	}
	if got, ok := Codex.FromWord("Astra"); !ok || got != Premier {
		t.Errorf("Codex.FromWord(Astra) = %q, %v, want premier", got, ok)
	}
	if _, ok := Codex.FromWord("opus"); ok {
		t.Error("Codex.FromWord(opus) maps, want no tier: opus is a Claude Code word")
	}
}
