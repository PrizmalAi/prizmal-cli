package launch

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// ErrNoModels is returned when the tenant serves nothing to pick from. It is
// a distinct error because the remedy differs from a failed fetch: the key is
// valid and the tenant is reachable, it simply has no models.
var ErrNoModels = errors.New("this switch key's tenant serves no models")

// modelTier is the Claude Code model family a model is recognised as, from a
// tier word in its name.
type modelTier string

const (
	modelTierOpus   modelTier = "opus"
	modelTierSonnet modelTier = "sonnet"
	modelTierHaiku  modelTier = "haiku"
	modelTierFable  modelTier = "fable"
)

// tierWords are the tier names a model can be recognised by, in the order they
// are tried. A name carrying more than one is pathological; the first match
// wins and the row still routes by its own model id.
var tierWords = []modelTier{modelTierOpus, modelTierSonnet, modelTierHaiku, modelTierFable}

// tierProfile is how Claude Code treats a model of one tier.
type tierProfile struct {
	// behavesAs is the first-party id whose client-side handling (prompt
	// profile, capability and effort defaults) Claude Code applies to the
	// model. Claude Code resolves the field through its own model catalog, so
	// a tier word would resolve to nothing: the session would warn that the
	// model is unknown and run on the unknown-model profile. Each id is one
	// every supported Claude Code release carries.
	behavesAs string
	// oneMillion reports whether that model accepts a 1M context window.
	// Claude Code drops a /model row that asks for 1M on a model without one.
	oneMillion bool
	// description is the row's text in Claude Code's /model picker, which
	// otherwise reads "Custom model (<model>)" whatever behavesAs says.
	description string
}

// tierProfiles maps each tier to how Claude Code treats it.
var tierProfiles = map[modelTier]tierProfile{
	modelTierOpus:   {behavesAs: "claude-opus-5", oneMillion: true, description: "Opus tier"},
	modelTierSonnet: {behavesAs: "claude-sonnet-5", oneMillion: true, description: "Sonnet tier"},
	modelTierHaiku:  {behavesAs: "claude-haiku-4-5-20251001", oneMillion: false, description: "Haiku tier"},
	modelTierFable:  {behavesAs: "claude-fable-5-1", oneMillion: true, description: "Fable tier"},
}

// claudeModelPrefix is the decoration the tenant's aliases carry so that
// Claude Code's row filter keeps them. It is stripped from a display label
// only: the id sent to the Switch keeps it, because that is the name that
// routes.
const claudeModelPrefix = "claude-"

// inferTier reports which tier a model name names, matching the tier words
// anywhere in the name and case-insensitively. A name without one has no tier,
// which is the common case for a model the tenant named itself.
func inferTier(name string) (modelTier, bool) {
	lower := strings.ToLower(name)
	for _, tier := range tierWords {
		if strings.Contains(lower, string(tier)) {
			return tier, true
		}
	}
	return "", false
}

// modelDisplayLabel is the row label for a model: its name without the [1m]
// decoration and without a leading claude- prefix, which is decoration the
// operator never chose. A name that is nothing but decoration falls back to
// the name it came from, so a row is never blank.
func modelDisplayLabel(name string) string {
	label := strings.TrimSuffix(name, oneMillionSuffix)
	if len(label) >= len(claudeModelPrefix) &&
		strings.EqualFold(label[:len(claudeModelPrefix)], claudeModelPrefix) {
		label = label[len(claudeModelPrefix):]
	}
	if label == "" {
		return name
	}
	return label
}

// ModelRow is one row of the model picker, and one option of the launched
// harness's own picker. Label is what a person reads and Model is the id that
// routes. BehavesAs and Description are empty for a model with no tier word.
type ModelRow struct {
	Label       string
	Model       string
	BehavesAs   string
	Description string

	// tier is the row's Claude tier, empty for a model with none.
	tier modelTier
}

// reservedModelNames are the placeholder rows the picker never offers.
//
// The switch publishes this row as `default`: GET /v1/models answers
// {"id":"default[1m]","name":"default","owned_by":"prizmal.ai"}, so the name to
// match is `default`, not the `prizmal/default` spelling this CLI used to send.
// Both are listed because the name routes through configselect, and a tenant
// could carry either during the transition.
//
// A picker row exists to name a model that routes. This one routes to whatever
// the switch key is already bound to, which is what a launch does with no model
// at all, so offering it asks the operator to choose the thing they get by not
// choosing.
const (
	reservedModelName       = "default"
	reservedModelNameLegacy = "prizmal/default"
)

// isReservedModelName reports whether a catalog name is a placeholder row.
func isReservedModelName(name string) bool {
	return name == reservedModelName || name == reservedModelNameLegacy
}

// ModelRows builds the picker's rows from a switch catalog.
//
// Rows are de-duplicated on the model id, so a catalog that lists the same
// model twice is not offered twice. Two distinct models whose labels collide
// fall back to their full names, because two identical labels leave an
// operator no way to tell the rows apart.
//
// The reserved placeholder is skipped: choosing it would send a name the Switch
// is retiring, and the point of the picker is to name a model that routes.
//
// A tier word in the model's name gives the row its tier's behavesAs and
// description, which shape how Claude Code runs and shows the model. Neither
// selects what serves the request: that is the Model id alone, resolved by
// configselect.
func ModelRows(models []LaunchModel) []ModelRow {
	rows := make([]ModelRow, 0, len(models))
	seen := make(map[string]bool, len(models))

	for _, m := range models {
		name := strings.TrimSuffix(m.Name, oneMillionSuffix)
		if name == "" || seen[name] || isReservedModelName(name) || m.FoldedInto != "" {
			continue
		}
		seen[name] = true

		row := ModelRow{Label: modelDisplayLabel(name), Model: name, Description: m.Description}
		tier, ok := modelTier(m.Tier), m.Tier != ""
		if !ok {
			tier, ok = inferTier(name)
		}
		if ok {
			row.tier = tier
			row.BehavesAs = tierProfiles[tier].behavesAs
			if row.Description == "" {
				row.Description = tierProfiles[tier].description
			}
		}
		rows = append(rows, row)
	}

	disambiguateLabels(rows)
	return rows
}

// disambiguateLabels makes every row's label unique, so an operator can tell
// two rows apart by the name shown.
//
// It runs to a fixed point rather than making one pass. One pass is not enough:
// a fallback label is itself a model id, and a model id can equal another row's
// stripped label. A catalog holding "claude-foo", "foo" and "claude-claude-foo"
// strips to a collision between the first two, whose fallback to full names
// then collides with the third row's "claude-foo". Repeating until no label
// repeats closes that.
//
// The loop terminates because each round replaces a repeated label with a
// strictly longer string (the full model id), and the number of distinct
// labels can only grow. A catalog of n rows admits at most n rounds.
func disambiguateLabels(rows []ModelRow) {
	for range rows {
		counts := make(map[string]int, len(rows))
		for _, row := range rows {
			counts[row.Label]++
		}

		changed := false
		for i := range rows {
			if counts[rows[i].Label] > 1 && rows[i].Label != rows[i].Model {
				rows[i].Label = rows[i].Model
				changed = true
			}
		}
		if !changed {
			return
		}
	}
}

// ModelPickerMenu renders the interactive menu and returns the chosen model
// id. It is a package variable so tests can drive the selection without a
// terminal, in the same way StdinIsTerminal is.
var ModelPickerMenu func(rows []ModelRow) (string, error)

// PickModel asks the operator to choose from rows. It returns ErrNoModels
// rather than opening an empty menu, which would render a list with nothing to
// select and no way to leave with a model.
func PickModel(rows []ModelRow) (string, error) {
	if len(rows) == 0 {
		return "", ErrNoModels
	}
	menu := ModelPickerMenu
	if menu == nil {
		menu = defaultModelPickerMenu
	}
	return menu(rows)
}

// pickerMaxVisible is how many models the picker shows at once. A display
// choice carried over from the huh picker, never sized against the chrome.
const pickerMaxVisible = 12

// pickerMenuHeight is the list frame's height in rows.
//
// The list reserves three rows besides the models: the title bar above them,
// and the help block below, whose default style pads a blank line above the
// help text. Every remaining row is a model, so the frame is the number of
// models to show, plus those three.
//
// A height below the chrome plus one model draws nothing useful, so that is
// the floor. The cap bounds the frame so the heading and the help line stay
// on screen for a tenant with dozens of models.
func pickerMenuHeight(rows int) int {
	// The title bar (1) plus the help block (2: top padding + text).
	const chrome = 3
	if rows < 1 {
		return chrome + 1
	}
	if rows > pickerMaxVisible {
		return pickerMaxVisible + chrome
	}
	return rows + chrome
}

// modelItem is one selectable model in the list. bubbles/list identifies items
// by their FilterValue, which is the text a search matches against.
type modelItem struct {
	label       string
	model       string
	description string
}

// FilterValue is what a typed search matches. It is the label, because that is
// what the operator reads on screen and would search for, while the model id it
// resolves to may carry decoration the label drops.
func (i modelItem) FilterValue() string { return i.label }

// Title is the row's display text.
func (i modelItem) Title() string { return i.label }

// Description is empty, so bubbles reserves no second line per model, which
// would halve how many fit on screen. modelDelegate draws the row's
// description on the label's own line.
func (i modelItem) Description() string { return "" }

// pickerWidth is the menu's drawing width. The list renders its rows in a
// fixed-width column, so a value wide enough for a long model name keeps them
// on one line.
const pickerWidth = 80

// pickerCursor marks the selected row. A model name can be long, so the marker
// is one cell wide and the row's text follows it.
const pickerCursor = "▸ "

// pickerHeading is the menu's title, drawn in the list's title bar.
const pickerHeading = "Select a model"

// pickerSelectedColor is the brand green: oklch(55% 0.06 150), a muted sage.
// It is spelled as sRGB because lipgloss takes hex, and the OKLCH form it was
// chosen in is noted here so the next change can match it. It is exported so
// the first-run banner can colour the logo with the same value, rather than
// carrying a second copy that could drift.
const pickerSelectedColor = "#587C5F"

// BrandGreen is pickerSelectedColor for callers outside this package.
const BrandGreen = pickerSelectedColor

// selectedStyle is the selected row: the cursor and the label in the green
// above, bold as well so the row still stands out where colour is stripped.
func selectedStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(pickerSelectedColor)).Bold(true)
}

// BrandLogoStyle colours a mark in the brand green, without the bold the picker
// row uses.
func BrandLogoStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(pickerSelectedColor))
}

// modelDelegate renders one model per line.
//
// bubbles' own delegate reserves a description line and a cell of spacing
// between rows, which made a 12-row frame show three models. One line per row
// with no spacing is what a list of short names wants, and the page size
// follows from it: bubbles divides the frame's available height by
// Height()+Spacing(), so a height of 1 and no spacing fills the frame.
//
// labelWidth is the widest label, so every description starts in one column.
type modelDelegate struct{ labelWidth int }

func (modelDelegate) Height() int  { return 1 }
func (modelDelegate) Spacing() int { return 0 }

func (modelDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

// Render draws a row with the cursor on the selected one.
//
// The cursor is an arrow rather than bubbles' left border, and it replaces the
// two leading spaces an unselected row gets, so every label starts in the same
// column whether or not it is selected.
func (d modelDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	row, ok := item.(modelItem)
	if !ok {
		return
	}

	label := row.label
	if m.Width() > 0 {
		label = ansi.Truncate(label, m.Width()-len(pickerCursor), "…")
	}

	prefix := "  "
	style := lipgloss.NewStyle()
	if index == m.Index() {
		prefix = pickerCursor
		style = selectedStyle()
	}
	line := style.Render(prefix + label)
	if row.description != "" {
		pad := strings.Repeat(" ", max(0, d.labelWidth-ansi.StringWidth(label))+2)
		line += pad + lipgloss.NewStyle().Faint(true).Render(row.description)
	}
	if m.Width() > 0 {
		line = ansi.Truncate(line, m.Width(), "…")
	}
	_, _ = fmt.Fprint(w, line)
}

// quitBinding is Esc and ctrl+c, the two ways out of the menu.
func quitBinding() key.Binding {
	return key.NewBinding(key.WithKeys("esc", "ctrl+c"), key.WithHelp("esc", "quit without saving"))
}

// pickerKeyMap is the list's keymap with only the bindings this menu uses.
//
// A zero Binding is disabled, so leaving one out removes it from the help line
// as well as from the keys the list reacts to. That matters here because the
// default map carries bindings for a general-purpose list: page jumps this
// twelve-row menu does not need, a bare `q` that would quit on a keystroke an
// operator might aim at a model name, and a `? more` hint for a help screen
// there is nothing more to show in.
func pickerKeyMap() list.KeyMap {
	return list.KeyMap{
		CursorUp:   key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		CursorDown: key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		Filter:     key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		// While filtering, enter applies the filter and esc clears it.
		AcceptWhileFiltering: key.NewBinding(key.WithKeys("enter", "tab", "shift+tab"), key.WithHelp("enter", "apply")),
		CancelWhileFiltering: key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "clear filter")),
		Quit:                 quitBinding(),
	}
}

// pickerModel is the bubbletea program behind the menu.
//
// It exists because huh pins its viewport to the selected row on every
// keystroke (Select.updateViewportHeight sets YOffset = selected), so its list
// scrolls under the cursor instead of letting the cursor travel down a fixed
// frame. bubbles/list moves the cursor within a page and flips the page only
// when the cursor would leave it, which is the behaviour wanted here, and it
// brings the fuzzy filter with it.
type pickerModel struct {
	list    list.Model
	chosen  string
	aborted bool
}

func newPickerModel(rows []ModelRow) pickerModel {
	items := make([]list.Item, 0, len(rows))
	for _, row := range rows {
		items = append(items, modelItem{label: row.Label, model: row.Model, description: row.Description})
	}
	// The column is capped, so one long model name cannot push every other
	// row's description past the menu's width.
	labelWidth := 0
	for _, row := range rows {
		labelWidth = max(labelWidth, min(ansi.StringWidth(row.Label), pickerWidth/2))
	}

	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = false

	l := list.New(items, modelDelegate{labelWidth: labelWidth}, pickerWidth, pickerMenuHeight(len(items)))
	// The list draws the heading in its title bar. That row is reserved anyway
	// while filtering is available, so putting the heading there costs nothing
	// and the bar doubles as the filter input while searching.
	l.Title = pickerHeading
	l.Styles.Title = lipgloss.NewStyle().Bold(true)
	// The title bar's own padding is what pushed the first model down a line.
	l.Styles.TitleBar = lipgloss.NewStyle()
	// The status line repeats the item count and the pagination line draws dots
	// for a list this short; both spend rows the models could use.
	l.SetShowStatusBar(false)
	l.SetShowPagination(false)
	// The filter input replaces the title while searching, so it has to stay on.
	l.SetShowFilter(true)

	// The default help lists keys this menu does not offer (`q quit`, `→ next
	// page`, a `? more` hint) because those bindings exist even when they do
	// nothing here. Only the bindings that work are populated, so the one help
	// line names exactly the keys an operator can press.
	l.KeyMap = pickerKeyMap()
	// Re-derive which bindings are enabled. list.New sized the default map for
	// the list's filter state, and the swap replaced its bindings with fresh
	// ones that are all enabled, so without this the help line offers the
	// filtering keys before anything is typed. Every path that touches the
	// keymap afterwards (SetFilterState, filter edits) re-runs this itself.
	l.SetFilterState(list.Unfiltered)
	l.SetShowHelp(true)
	l.Help.Styles.ShortKey = lipgloss.NewStyle()
	l.Help.Styles.ShortDesc = lipgloss.NewStyle().Faint(true)
	l.Help.Styles.ShortSeparator = lipgloss.NewStyle().Faint(true)

	return pickerModel{list: l}
}

func (m pickerModel) Init() tea.Cmd { return nil }

func (m pickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Esc clears an active filter before it quits. The keymap binds esc to
		// CancelWhileFiltering with the help text "clear filter", so quitting
		// here would contradict what the menu tells the operator, and a
		// mistyped search would cost the whole menu rather than the search.
		// Only once there is no filter to clear does esc mean quit.
		if m.list.FilterState() == list.Filtering && msg.String() == "esc" {
			var cmd tea.Cmd
			m.list, cmd = m.list.Update(msg)
			return m, cmd
		}

		switch {
		case key.Matches(msg, quitBinding()):
			m.aborted = true
			return m, tea.Quit
		case msg.String() == "enter":
			// Enter selects, but only when the list is not mid-search: while
			// filtering, the list's own keymap uses enter to apply the filter.
			if m.list.FilterState() != list.Filtering {
				if item, ok := m.list.SelectedItem().(modelItem); ok {
					m.chosen = item.model
					return m, tea.Quit
				}
			}
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m pickerModel) View() string { return m.list.View() }

// defaultModelPickerMenu is the menu: a filterable list where typing narrows
// the rows and Enter selects.
//
// The program reads stdin and draws on stderr, so the launched harness's own
// stdout stays clean.
func defaultModelPickerMenu(rows []ModelRow) (string, error) {
	p := tea.NewProgram(newPickerModel(rows), tea.WithInput(os.Stdin), tea.WithOutput(os.Stderr))
	final, err := p.Run()
	if err != nil {
		return "", fmt.Errorf("model picker: %w", err)
	}

	done, ok := final.(pickerModel)
	if !ok || done.aborted || done.chosen == "" {
		return "", ErrCancelled
	}
	return done.chosen, nil
}
