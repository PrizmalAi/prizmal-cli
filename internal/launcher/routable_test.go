package launch

import (
	"slices"
	"strings"
	"testing"
)

// routableTenant holds the opus, sonnet and haiku tiers and not fable.
func routableTenant() []LaunchModel {
	return withClaudeTiers([]LaunchModel{
		{Name: "frontier", Tier: "opus"},
		{Name: "core", Tier: "sonnet"},
		{Name: "flash", Tier: "haiku"},
		{Name: "default"},
	})
}

// CheckRoutable accepts every name the Switch's model-name selection
// resolves, and the tier words a harness that resolves them sends as a Claude
// id. It refuses the rest.
func TestCheckRoutableAcceptsWhatTheSwitchResolves(t *testing.T) {
	for _, tc := range []struct {
		name      string
		model     string
		tierWords bool
		wantErr   string
	}{
		// `default` is a placeholder, not a model name, and the CLI never sends it.
		{name: "default", model: "default", wantErr: "is not a name the Switch accepts"},
		{name: "default decorated", model: "default[1m]", wantErr: "is not a name the Switch accepts"},
		{name: "the legacy default spelling", model: "prizmal/default", wantErr: "is not a name the Switch accepts"},

		// A name the tenant holds, with or without one [1m].
		{name: "a listed config", model: "frontier"},
		{name: "a listed config decorated", model: "frontier[1m]"},
		{name: "a held tier alias", model: "claude-tier-opus"},

		// A Claude vendor id maps onto the alias of its tier when held.
		{name: "an opus vendor id", model: "claude-opus-5"},
		{name: "an opus vendor id decorated", model: "claude-opus-5[1m]"},
		{name: "a sonnet vendor id", model: "claude-sonnet-4-6"},
		{name: "a haiku vendor id", model: "claude-haiku-4-5-20251001"},
		{name: "a vendor id of an unheld tier", model: "claude-fable-5-1", wantErr: "fable tier"},
		{name: "the alias of an unheld tier", model: "claude-tier-fable", wantErr: "is not a name this Switch routes"},
		// The CLI reads no holder for a mythos config, so it cannot tell.
		{name: "a mythos vendor id", model: "claude-mythos-5"},

		// Claude Code resolves a tier word to its own id for the tier.
		{name: "sonnet", model: "sonnet", tierWords: true},
		{name: "a capitalised tier word", model: "Opus", tierWords: true},
		{name: "a decorated tier word", model: "sonnet[1m]", tierWords: true},
		{name: "an upper-case word and suffix", model: "OPUS[1M]", tierWords: true},
		{name: "haiku", model: "haiku", tierWords: true},
		{name: "opusplan", model: "opusplan", tierWords: true},
		{name: "fable is unheld", model: "fable", tierWords: true, wantErr: "fable tier"},
		{name: "best runs the fable tier", model: "best", tierWords: true, wantErr: "fable tier"},
		{name: "a harness that sends the word as it is", model: "sonnet", wantErr: "is not a name this Switch routes"},

		{name: "an unknown name", model: "gpt-5.6-luna", wantErr: "is not a name this Switch routes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckRoutable(tc.model, routableTenant(), tc.tierWords)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckRoutable(%q) = %v, want nil", tc.model, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("CheckRoutable(%q) = %v, want an error containing %q", tc.model, err, tc.wantErr)
			}
		})
	}
}

// A catalog that lists `default` does not make it launchable: it is a
// placeholder, not a model name.
func TestCheckRoutableRefusesDefaultEvenWhenTheCatalogListsIt(t *testing.T) {
	catalog := withClaudeTiers([]LaunchModel{{Name: "default"}, {Name: "frontier"}})
	if err := CheckRoutable("default", catalog, false); err == nil {
		t.Fatal("CheckRoutable(default) = nil, want a refusal")
	}
}

// The names a listing prints are the names a launch can send, so the
// `default` placeholder is not one of them.
func TestLaunchableNamesDropTheDefaultPlaceholder(t *testing.T) {
	catalog := withClaudeTiers([]LaunchModel{{Name: "default"}, {Name: "prizmal/default"}, {Name: "frontier"}, {Name: ""}})
	if got, want := LaunchableNames(catalog), []string{"frontier"}; !slices.Equal(got, want) {
		t.Fatalf("LaunchableNames = %v, want %v", got, want)
	}
}

// An unread catalog is no evidence about a name, except for the `default`
// placeholder, which no catalogue makes launchable.
func TestCheckRoutablePassesEverythingWithoutACatalog(t *testing.T) {
	if err := CheckRoutable("gpt-5.6-luna", nil, false); err != nil {
		t.Fatalf("CheckRoutable with no catalog = %v, want nil", err)
	}
	if err := CheckRoutable("default", nil, false); err == nil {
		t.Fatal("CheckRoutable(default) with no catalog = nil, want a refusal")
	}
}

// A subagent's tier word routes whether or not the tenant holds the tier: a
// held tier maps onto its alias, and an unheld one lands on the launch model,
// which the launch check accepted.
func TestCheckSubagentRoutableTakesEveryTierWord(t *testing.T) {
	for _, name := range []string{"sonnet", "haiku", "fable", "best", "opusplan", "Opus[1m]"} {
		if err := CheckSubagentRoutable(name, routableTenant(), true); err != nil {
			t.Errorf("CheckSubagentRoutable(%q) = %v, want nil", name, err)
		}
	}
	if err := CheckSubagentRoutable("claude-tier-fable", routableTenant(), true); err == nil {
		t.Error("CheckSubagentRoutable(claude-tier-fable) = nil, want the launch rule")
	}
	if err := CheckSubagentRoutable("sonnet", routableTenant(), false); err == nil {
		t.Error("CheckSubagentRoutable(sonnet) with a harness that sends the word as it is = nil, want a refusal")
	}
}

// A picker row reads "tier-opus" and routes by "claude-tier-opus", so a name
// copied off the screen is decorated back into the name the catalog carries.
// A config the tenant named as the label spells it wins.
func TestRouteNameDecoratesAPickerLabel(t *testing.T) {
	catalog := withClaudeTiers([]LaunchModel{{Name: "claude-team", Tier: "opus"}, {Name: "core"}})
	for _, tc := range []struct{ in, want string }{
		{"tier-opus", "claude-tier-opus"},
		{"team", "claude-team"},
		{"claude-tier-opus", "claude-tier-opus"},
		{"core", "core"},
		{"sonnet", "sonnet"},
		{"default", "default"},
		{"", ""},
	} {
		if got := RouteName(catalog, tc.in); got != tc.want {
			t.Errorf("RouteName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := RouteName([]LaunchModel{{Name: "tier-opus"}}, "tier-opus"); got != "tier-opus" {
		t.Errorf("RouteName(tenant config named tier-opus) = %q, want it unchanged", got)
	}
	if got := RouteName(nil, "tier-opus"); got != "tier-opus" {
		t.Errorf("RouteName with no catalog = %q, want tier-opus", got)
	}
}
