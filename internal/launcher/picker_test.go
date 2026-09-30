package launch

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// A model whose name carries a tier word behaves as that tier, matched anywhere
// in the name and case-insensitively. The tier sets how Claude Code runs and
// shows the model. It never decides what serves the request.
func TestInferTier(t *testing.T) {
	for _, tc := range []struct {
		name string
		want modelTier
		ok   bool
	}{
		{name: "claude-tier-haiku", want: modelTierHaiku, ok: true},
		{name: "claude-tier-sonnet", want: modelTierSonnet, ok: true},
		{name: "claude-tier-opus", want: modelTierOpus, ok: true},
		{name: "claude-tier-fable", want: modelTierFable, ok: true},
		{name: "claude-Opus-5", want: modelTierOpus, ok: true},
		{name: "my-sonnet-thing", want: modelTierSonnet, ok: true},
		{name: "gpt-oss:20b"},
		{name: "ollama-open-china-1"},
		{name: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := inferTier(tc.name)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("inferTier(%q) = (%q, %v), want (%q, %v)", tc.name, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// The row label drops the [1m] suffix and the claude- prefix, which are
// decoration the operator never chose.
func TestModelDisplayLabel(t *testing.T) {
	for _, tc := range []struct {
		name string
		want string
	}{
		{name: "claude-tier-haiku", want: "tier-haiku"},
		{name: "claude-tier-haiku[1m]", want: "tier-haiku"},
		{name: "claude-ollama-open-china-1", want: "ollama-open-china-1"},
		{name: "gpt-oss:20b", want: "gpt-oss:20b"},
		{name: "CLAUDE-Tier-Haiku", want: "Tier-Haiku"},
		// A name that is nothing but decoration falls back to the name it came
		// from, so a row is never blank.
		{name: "claude-", want: "claude-"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := modelDisplayLabel(tc.name); got != tc.want {
				t.Fatalf("modelDisplayLabel(%q) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

// Rows carry the bare model id, a display label, and the inferred tier.
func TestModelRowsBuildsRows(t *testing.T) {
	rows := ModelRows([]LaunchModel{
		{Name: "claude-tier-opus"},
		{Name: "claude-tier-haiku"},
		{Name: "gpt-oss:20b"},
	})

	if len(rows) != 3 {
		t.Fatalf("built %d rows, want 3: %+v", len(rows), rows)
	}
	if rows[0].Model != "claude-tier-opus" || rows[0].Label != "tier-opus" || rows[0].BehavesAs != "claude-opus-5" {
		t.Errorf("row 0 = %+v, want the opus tier with a bare label", rows[0])
	}
	if rows[2].Model != "gpt-oss:20b" || rows[2].BehavesAs != "" || rows[2].Description != "" {
		t.Errorf("row 2 = %+v, want no behavesAs and no description for a name with no tier word", rows[2])
	}
}

// A row's behavesAs is the id of a model Claude Code knows. Claude Code reads
// the field through its model catalog, so a tier word such as "opus" resolves
// to nothing: the launch warns that the model is unknown and runs on the
// unknown-model prompt profile. Each id is one every supported Claude Code
// release carries, and one the tier's modelOverrides lineage already names.
func TestModelRowsMapEachTierToAFirstPartyID(t *testing.T) {
	for _, tc := range []struct {
		name string
		tier modelTier
		want string
	}{
		{name: "claude-tier-opus", tier: modelTierOpus, want: "claude-opus-5"},
		{name: "claude-tier-sonnet", tier: modelTierSonnet, want: "claude-sonnet-5"},
		{name: "claude-tier-haiku", tier: modelTierHaiku, want: "claude-haiku-4-5-20251001"},
		{name: "claude-tier-fable", tier: modelTierFable, want: "claude-fable-5-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := ModelRows([]LaunchModel{{Name: tc.name}})
			if len(rows) != 1 {
				t.Fatalf("built %d rows, want 1", len(rows))
			}
			if rows[0].BehavesAs != tc.want {
				t.Fatalf("behavesAs = %q, want %q", rows[0].BehavesAs, tc.want)
			}
			if !slices.Contains(claudeFamilyIDs[tc.tier], rows[0].BehavesAs) {
				t.Fatalf("behavesAs %q is not in the %s lineage %v", rows[0].BehavesAs, tc.tier, claudeFamilyIDs[tc.tier])
			}
		})
	}
}

// Claude Code shows a row's description as its text and falls back to
// "Custom model (<model>)" without one. behavesAs never changes that text, so
// a tier row names its tier through the description.
func TestModelRowsDescribeTheirTier(t *testing.T) {
	for _, tc := range []struct {
		name string
		want string
	}{
		{name: "claude-tier-opus", want: "Opus tier"},
		{name: "claude-tier-sonnet", want: "Sonnet tier"},
		{name: "claude-tier-haiku", want: "Haiku tier"},
		{name: "claude-tier-fable", want: "Fable tier"},
		{name: "gpt-oss:20b", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := ModelRows([]LaunchModel{{Name: tc.name}})
			if len(rows) != 1 || rows[0].Description != tc.want {
				t.Fatalf("rows = %+v, want one row described %q", rows, tc.want)
			}
		})
	}
}

// The switch decorates its ids with [1m]; a row sends the undecorated name,
// which is what routes.
func TestModelRowsStripTheSuffixFromTheModel(t *testing.T) {
	rows := ModelRows([]LaunchModel{{Name: "claude-tier-haiku[1m]"}})

	if len(rows) != 1 {
		t.Fatalf("built %d rows, want 1", len(rows))
	}
	if rows[0].Model != "claude-tier-haiku" {
		t.Fatalf("row model = %q, want the bare name; the suffix is re-applied in the settings JSON", rows[0].Model)
	}
	if rows[0].Label != "tier-haiku" {
		t.Fatalf("row label = %q, want tier-haiku", rows[0].Label)
	}
}

// A catalog that lists the same model twice is not offered twice. The harness's
// picker deduplicates on the model value anyway, and a duplicate row would only
// be dropped there with a log line.
func TestModelRowsDeduplicateByModel(t *testing.T) {
	rows := ModelRows([]LaunchModel{
		{Name: "cheap"},
		{Name: "cheap[1m]"},
		{Name: "other"},
	})

	if len(rows) != 2 {
		t.Fatalf("built %d rows, want 2: %+v", len(rows), rows)
	}
	if rows[0].Model != "cheap" || rows[1].Model != "other" {
		t.Fatalf("rows = %+v, want [cheap other]", rows)
	}
}

// Two distinct models whose labels collide fall back to their full names: two
// identical labels leave an operator no way to tell the rows apart.
func TestModelRowsDisambiguateCollidingLabels(t *testing.T) {
	rows := ModelRows([]LaunchModel{
		{Name: "a/tier-opus"},
		{Name: "b/tier-opus"},
	})

	if len(rows) != 2 {
		t.Fatalf("built %d rows, want 2", len(rows))
	}
	if rows[0].Label == rows[1].Label {
		t.Fatalf("both rows are labelled %q; the operator cannot tell them apart", rows[0].Label)
	}
	for _, row := range rows {
		if row.Label != row.Model {
			t.Errorf("colliding row label = %q, want the full model %q", row.Label, row.Model)
		}
	}
}

// The fallback must leave every label distinct, including when a fallback label
// itself collides with another row's label.
//
// Stripping the claude- prefix maps three different models onto two labels:
// claude-foo and claude-claude-foo both display as "foo"-family strings, and
// falling back to the model id once is not enough, because one row's full name
// ("claude-foo") is the other row's stripped label. A single pass would leave
// two rows labelled "claude-foo", which is the outcome the fallback exists to
// prevent.
func TestModelRowsDisambiguateChainedLabelCollisions(t *testing.T) {
	rows := ModelRows([]LaunchModel{
		{Name: "claude-foo"},
		{Name: "foo"},
		{Name: "claude-claude-foo"},
	})

	if len(rows) != 3 {
		t.Fatalf("built %d rows, want 3: %+v", len(rows), rows)
	}

	seen := map[string]string{}
	for _, row := range rows {
		if other, dup := seen[row.Label]; dup {
			t.Errorf("rows for %q and %q are both labelled %q; the operator cannot tell them apart", other, row.Model, row.Label)
			continue
		}
		seen[row.Label] = row.Model
	}

	// Every model must still be reachable by its own id.
	models := map[string]bool{}
	for _, row := range rows {
		models[row.Model] = true
	}
	for _, want := range []string{"claude-foo", "foo", "claude-claude-foo"} {
		if !models[want] {
			t.Errorf("model %q is missing from the rows: %+v", want, rows)
		}
	}
}

// A row never carries an empty model, which would launch nothing.
func TestModelRowsSkipEmptyNames(t *testing.T) {
	rows := ModelRows([]LaunchModel{{Name: ""}, {Name: "[1m]"}, {Name: "good"}})

	if len(rows) != 1 || rows[0].Model != "good" {
		t.Fatalf("rows = %+v, want only [good]", rows)
	}
}

// The picker offers no default row. The reserved placeholder is being sunset,
// so a tenant catalog that still lists it must not put it in the menu.
//
// The switch publishes the row as `default`, not `prizmal/default`: it serves
// {"id":"default[1m]","name":"default"} with owned_by prizmal.ai. The filter
// therefore has to match the name the catalog actually uses, or it silently
// does nothing and the row reaches the menu.
func TestModelRowsSkipTheReservedPlaceholder(t *testing.T) {
	rows := ModelRows([]LaunchModel{
		{Name: "default"},
		{Name: "default[1m]"},
		{Name: "real-model"},
	})

	if len(rows) != 1 || rows[0].Model != "real-model" {
		t.Fatalf("rows = %+v, want only real-model; the picker must offer no default row", rows)
	}
	for _, row := range rows {
		if row.Model == "default" || row.Label == "default" {
			t.Fatalf("row %+v is the sunset default row", row)
		}
	}
}

// A catalog that lists only the reserved name leaves nothing to choose, which
// is an error rather than an empty menu.
func TestModelRowsOfOnlyThePlaceholderAreEmpty(t *testing.T) {
	if rows := ModelRows([]LaunchModel{{Name: "default"}}); len(rows) != 0 {
		t.Fatalf("rows = %+v, want none", rows)
	}
}

// Esc quits the picker without saving, alongside ctrl+c.
//
// The picker is a menu an operator opens on purpose and may well change their
// mind in, and Esc is what people press to back out of one, so it is bound
// rather than left to the list's default keymap.
func TestPickerQuitBindingIncludesEsc(t *testing.T) {
	keys := quitBinding().Keys()

	for _, want := range []string{"ctrl+c", "esc"} {
		if !slices.Contains(keys, want) {
			t.Errorf("quit binding does not include %q; keys = %v", want, keys)
		}
	}
}

// The quit binding must stay narrow: a bare letter or enter would make the menu
// unusable, since those select a row or filter the list.
func TestPickerQuitBindingExcludesSelectionKeys(t *testing.T) {
	keys := quitBinding().Keys()

	for _, unwanted := range []string{"enter", "q", "j", "k", "/", " "} {
		if slices.Contains(keys, unwanted) {
			t.Errorf("quit binding includes %q, which the menu needs for selection", unwanted)
		}
	}
}

// An aborted pick is a decision, not a failure. PickModel reports it as a
// cancellation so a caller can exit quietly instead of printing an error the
// operator caused on purpose.
func TestPickModelReportsCancellation(t *testing.T) {
	original := ModelPickerMenu
	t.Cleanup(func() { ModelPickerMenu = original })
	ModelPickerMenu = func([]ModelRow) (string, error) { return "", ErrCancelled }

	_, err := PickModel(ModelRows([]LaunchModel{{Name: "a-model"}}))
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("error = %v, want ErrCancelled so the caller exits quietly", err)
	}
}

// The menu's cursor walks the list in order: pressing down n times and
// selecting gives the nth row after the first, including across a page
// boundary. bubbles/list moves the cursor within a page and flips the page only
// when the cursor would leave it, which is why the index still counts straight
// through a flip.
//
// Driving the model directly is the only way to assert this: a pty harness
// delivers keystrokes faster than bubbletea redraws, so a real-terminal test
// reads as flaky rather than failing.
func TestPickerCursorWalksTheListInOrder(t *testing.T) {
	const count = 20
	rows := make([]ModelRow, 0, count)
	for i := 1; i <= count; i++ {
		name := fmt.Sprintf("model-%02d", i)
		rows = append(rows, ModelRow{Label: name, Model: name})
	}

	// 0, a mid-page index, the first row of the second page, and the last row.
	for _, downs := range []int{0, 5, 12, count - 1} {
		m := newPickerModel(rows)
		var model tea.Model = m
		for i := 0; i < downs; i++ {
			model, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
		}
		model, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})

		want := fmt.Sprintf("model-%02d", downs+1)
		if got := model.(pickerModel).chosen; got != want {
			t.Errorf("%d downs selected %q, want %q", downs, got, want)
		}
	}
}

// A model's own id is what a row sends, whatever its label reads. The list
// filters on the label, so the two are separate strings and selecting must not
// return the one the operator searched with.
func TestPickerSelectsTheModelNotTheLabel(t *testing.T) {
	rows := []ModelRow{{Label: "tier-haiku", Model: "claude-tier-haiku"}}

	m := newPickerModel(rows)
	var model tea.Model = m
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if got := model.(pickerModel).chosen; got != "claude-tier-haiku" {
		t.Fatalf("selected %q, want the model id", got)
	}
}

// Esc aborts: no model is chosen, so the caller sees a cancellation rather than
// a selection it never made.
func TestPickerEscAbortsWithoutChoosing(t *testing.T) {
	rows := []ModelRow{{Label: "a", Model: "a"}}

	m := newPickerModel(rows)
	var model tea.Model = m
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})

	got := model.(pickerModel)
	if !got.aborted {
		t.Error("Esc did not mark the menu aborted")
	}
	if got.chosen != "" {
		t.Errorf("Esc chose %q, want no choice", got.chosen)
	}
}

// An empty catalog is an error rather than an empty menu: a list with nothing
// in it and no way to leave with a model has nothing to offer.
func TestPickModelRefusesAnEmptyList(t *testing.T) {
	_, err := PickModel(nil)
	if !errors.Is(err, ErrNoModels) {
		t.Fatalf("PickModel(nil) error = %v, want ErrNoModels", err)
	}
}

// A selection returns the chosen model id, not its label.
func TestPickModelReturnsTheChosenModel(t *testing.T) {
	original := ModelPickerMenu
	t.Cleanup(func() { ModelPickerMenu = original })

	ModelPickerMenu = func(rows []ModelRow) (string, error) {
		if len(rows) != 2 {
			t.Fatalf("menu saw %d rows, want 2", len(rows))
		}
		// The label is what a person reads; the model is what routes.
		if rows[0].Label != "tier-haiku" || rows[0].Model != "claude-tier-haiku" {
			t.Fatalf("row 0 = %+v", rows[0])
		}
		return rows[1].Model, nil
	}

	got, err := PickModel(ModelRows([]LaunchModel{{Name: "claude-tier-haiku"}, {Name: "gpt-oss:20b"}}))
	if err != nil {
		t.Fatalf("PickModel: %v", err)
	}
	if got != "gpt-oss:20b" {
		t.Fatalf("chosen = %q, want gpt-oss:20b", got)
	}
}

// The list frame shows as many models as asked for, plus the rows the list
// spends on its own chrome: the title bar and the help block, which
// bubbles' default help style pads with a blank line above the help text.
func TestPickerMenuHeightShowsTheModelsAsked(t *testing.T) {
	// One row per model, plus the title row and the two-row help block.
	for _, tc := range []struct{ models, want int }{
		{models: 1, want: 4},
		{models: 3, want: 6},
		{models: 12, want: 15},
	} {
		if got := pickerMenuHeight(tc.models); got != tc.want {
			t.Errorf("pickerMenuHeight(%d) = %d, want %d", tc.models, got, tc.want)
		}
	}
}

// The frame is capped so a tenant with dozens of models still leaves the
// heading and the help line on screen, and it never asks for a frame too short
// to hold its chrome.
func TestPickerMenuHeightIsCapped(t *testing.T) {
	if got := pickerMenuHeight(0); got != 4 {
		t.Errorf("pickerMenuHeight(0) = %d, want the chrome plus one model row", got)
	}
	if got := pickerMenuHeight(500); got != pickerMaxVisible+3 {
		t.Errorf("pickerMenuHeight(500) = %d, want the cap of %d models plus the chrome", got, pickerMaxVisible)
	}
}

// The picker draws one row per model, capped at twelve: the frame carries the
// list's chrome, and the models fill every row the chrome leaves.
//
// This test renders the picker rather than restating the pickerMenuHeight
// arithmetic, so a future change to the list's chrome (a re-enabled status
// line, a bubbles help-style padding change) fails here instead of hiding as
// a menu that shows fewer models than the tenant serves.
//
// A catalog of three once drew a single row: the frame was sized to rows + 1
// while the title and the help block cost three, so the visible count worked
// out to rows - 2.
func TestPickerDrawsARowPerModel(t *testing.T) {
	for models := 1; models <= 20; models++ {
		rows := make([]ModelRow, 0, models)
		for i := 1; i <= models; i++ {
			name := fmt.Sprintf("model-%02d", i)
			rows = append(rows, ModelRow{Label: name, Model: name})
		}

		m := newPickerModel(rows)
		drawn := strings.Count(m.View(), "model-")
		want := min(models, pickerMaxVisible)
		if drawn != want {
			t.Errorf("a catalog of %d drew %d rows, want %d", models, drawn, want)
		}
	}
}

// The row labels are what an operator types against when searching, since the
// list filters on the label rather than the model id, so a suffix left on a
// label would be a string nobody would think to search for.
func TestModelRowsLabelsAreSearchable(t *testing.T) {
	rows := ModelRows([]LaunchModel{{Name: "claude-tier-sonnet[1m]"}})
	if len(rows) != 1 {
		t.Fatalf("built %d rows, want 1", len(rows))
	}
	label := rows[0].Label
	for _, noisy := range []string{"[1m]", "claude-"} {
		if strings.Contains(label, noisy) {
			t.Errorf("label %q still carries %q", label, noisy)
		}
	}
}

// The selected row is green, and stays recognisable where colour is reduced.
//
// oklch(55% 0.06 150) is #587C5F, a muted green. Truecolor renders it exactly;
// a 256-colour terminal approximates it; plain ANSI falls back to the standard
// green. Any of those still marks the row, which is the point of choosing a
// green value rather than an arbitrary one.
func TestSelectedStyleRendersGreen(t *testing.T) {
	original := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(original) })

	lipgloss.SetColorProfile(termenv.TrueColor)
	got := selectedStyle().Render("model-01")
	// Bold is emitted before the colour, so the escape reads 1;38;2;...
	if !strings.HasPrefix(got, "\x1b[1;38;2;88;124;95m") {
		t.Errorf("selected row = %q, want it bold and green (38;2;88;124;95)", got)
	}
}

// Esc clears an active filter rather than quitting the menu.
//
// The keymap binds esc to "clear filter" while filtering, so quitting there
// would contradict the help line the operator is reading, and a mistyped search
// would cost the whole menu instead of the search. Esc quits only when there is
// no filter to clear.
func TestPickerEscClearsTheFilterBeforeQuitting(t *testing.T) {
	rows := []ModelRow{{Label: "aaa", Model: "aaa"}, {Label: "bbb", Model: "bbb"}}

	m := newPickerModel(rows)
	var model tea.Model = m
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})

	if got := model.(pickerModel).list.FilterState(); got != list.Filtering {
		t.Fatalf("setup failed: filter state = %v, want filtering", got)
	}

	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})

	got := model.(pickerModel)
	if got.aborted {
		t.Error("esc aborted the whole menu while filtering; it should clear the filter")
	}
	if fs := got.list.FilterState(); fs == list.Filtering {
		t.Errorf("filter state = %v, want the filter cleared", fs)
	}
}

// pickerHelpLine is the help text the picker draws on its last line, stripped
// of styling.
func pickerHelpLine(m tea.Model) string {
	lines := strings.Split(m.View(), "\n")
	return ansi.Strip(lines[len(lines)-1])
}

// A fresh picker's help line names only the keys that work before any search:
// moving, starting a filter, and quitting. The filtering keys (enter to apply,
// esc to clear) appear once a search starts.
//
// The picker swaps in its own keymap after bubbles has already set each
// binding for the list's filter state. The new bindings arrive enabled, so a
// fresh menu once advertised "enter apply" and "esc clear filter" with nothing
// typed yet, and the line corrected itself only after a filter round-trip.
func TestPickerHelpLineNamesFilteringKeysOnlyWhileFiltering(t *testing.T) {
	var model tea.Model = newPickerModel([]ModelRow{{Label: "a", Model: "a"}, {Label: "b", Model: "b"}})

	browsing := pickerHelpLine(model)
	for _, want := range []string{"up", "down", "/ filter", "esc quit without saving"} {
		if !strings.Contains(browsing, want) {
			t.Errorf("help line before a search = %q, want it to name %q", browsing, want)
		}
	}
	for _, unwanted := range []string{"apply", "clear filter"} {
		if strings.Contains(browsing, unwanted) {
			t.Errorf("help line before a search = %q, want no %q", browsing, unwanted)
		}
	}

	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})

	filtering := pickerHelpLine(model)
	for _, want := range []string{"enter apply", "esc clear filter"} {
		if !strings.Contains(filtering, want) {
			t.Errorf("help line while filtering = %q, want it to name %q", filtering, want)
		}
	}
}

// With no filter active, esc still quits, which is the way out of the menu.
func TestPickerEscQuitsWhenNotFiltering(t *testing.T) {
	m := newPickerModel([]ModelRow{{Label: "a", Model: "a"}})
	var model tea.Model = m

	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})

	if !model.(pickerModel).aborted {
		t.Error("esc did not quit with no filter active")
	}
}

// OwnsModelFlag is opt-in, and only Claude Code opts in. pi and codex read
// --model for something other than the launch model, so the CLI must leave
// their arguments alone; a runner that starts returning true would change which
// provider pi runs.
func TestOnlyClaudeOwnsTheModelFlag(t *testing.T) {
	owner := func(r Runner) bool {
		o, ok := r.(OwningModelFlag)
		return ok && o.OwnsModelFlag()
	}
	if !owner(&Claude{}) {
		t.Error("Claude does not own --model; the CLI would forward it and the harness would outrank prizmal's settings")
	}
	for _, r := range []Runner{&Pi{}, &Codex{}, &OpenCode{}, &Cline{}} {
		if owner(r) {
			t.Errorf("%s claims --model; pi reads a provider-qualified --model as a provider choice, and codex already refuses the flag", r.String())
		}
	}
}

// A tier the Switch sends outranks the tier word in the name. The row gets the
// tier's behavesAs, and the tier's text unless the Switch sent a description.
func TestModelRowsTakeTheSwitchsTier(t *testing.T) {
	rows := ModelRows([]LaunchModel{
		{Name: "smart", Tier: "opus"},
		{Name: "flash", Tier: "sonnet", Description: "Fast and cheap"},
		{Name: "team-opus-blend", Tier: "haiku"},
	})
	if len(rows) != 3 {
		t.Fatalf("built %d rows, want 3", len(rows))
	}
	if rows[0].BehavesAs != "claude-opus-5" || rows[0].Description != "Opus tier" {
		t.Errorf("smart = %+v, want the opus profile and text", rows[0])
	}
	if rows[1].BehavesAs != "claude-sonnet-5" || rows[1].Description != "Fast and cheap" {
		t.Errorf("flash = %+v, want the sonnet profile and the Switch's text", rows[1])
	}
	if rows[2].BehavesAs != "claude-haiku-4-5-20251001" {
		t.Errorf("team-opus-blend = %+v, want the Switch's haiku tier over the name's opus", rows[2])
	}
}

// The prizmal picker shows each row's description beside its label, in one
// column, as Claude Code's /model does.
func TestPickerShowsDescriptionsInOneColumn(t *testing.T) {
	view := newPickerModel(ModelRows([]LaunchModel{
		{Name: "claude-tier-opus"},
		{Name: "smart", Tier: "sonnet"},
		{Name: "plain"},
	})).View()

	column := -1
	for _, want := range []struct{ label, description string }{
		{"tier-opus", "Opus tier"},
		{"smart", "Sonnet tier"},
	} {
		line := ""
		for _, l := range strings.Split(ansi.Strip(view), "\n") {
			if strings.Contains(l, want.label) {
				line = l
				break
			}
		}
		at := strings.Index(line, want.description)
		if at < 0 {
			t.Fatalf("row %q = %q, want it to show %q\n%s", want.label, line, want.description, view)
		}
		at = ansi.StringWidth(line[:at])
		if column >= 0 && at != column {
			t.Fatalf("description of %q starts at column %d, want %d\n%s", want.label, at, column, view)
		}
		column = at
	}
}

// One long model name must not push every other row's description past the
// menu's width.
func TestPickerCapsTheLabelColumn(t *testing.T) {
	view := ansi.Strip(newPickerModel(ModelRows([]LaunchModel{
		{Name: "smart", Description: "Everyday coding"},
		{Name: strings.Repeat("x", 76)},
	})).View())
	if !strings.Contains(view, "Everyday coding") {
		t.Fatalf("a long label hid the other row's description:\n%s", view)
	}
}
