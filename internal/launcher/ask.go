package launch

import (
	"errors"
	"os"

	"golang.org/x/term"
)

// Asking is how a launch reaches a person: the probe that says whether there is
// one, and the menu that asks them.
//
// It replaced two exported package variables a caller overwrote — the terminal
// probe and the picker menu — because whether a launch can ask, and what it
// asks with, are inputs to that launch, not properties of the process. A
// variable cannot say what a launch will use: a reader sees one name and
// cannot tell whether it has been replaced, and a test that overwrites one
// inherits whatever the next test leaves behind unless it saves and restores
// by hand. Stating both halves in one value, built where the launch is made,
// makes the decision readable in one place and turns a test double into a
// field rather than a save-and-restore dance.
//
// The zero value asks nobody. It is safe rather than useful: CanAsk reports
// false, so a launch with nothing else to resolve stops with the message it
// stops with on a pipe, and Pick names the missing menu rather than panicking
// on a nil call.
type Asking struct {
	// Interactive reports whether a person is there to answer a prompt.
	Interactive func() bool

	// Menu asks which of rows to run and returns the chosen model id. It
	// reports ErrCancelled when the operator backed out, so a caller exits
	// quietly instead of printing an error the operator caused on purpose.
	Menu func(rows []ModelRow) (string, error)
}

// AskOperator returns the Asking a real launch uses: this process's own
// standard input for the probe, and the real picker menu.
//
// A launch that will open a menu for a person passes this. A test passes an
// Asking of its own instead, and has nothing to restore afterwards.
func AskOperator() Asking {
	return Asking{
		Interactive: func() bool { return term.IsTerminal(int(os.Stdin.Fd())) },
		Menu:        defaultModelPickerMenu,
	}
}

// CanAsk reports whether there is a person to ask a question of.
//
// A launch that would have to ask, and cannot, is refused before the menu
// opens: a full-screen list on a pipe reads whatever arrives on stdin as
// keystrokes, and a prompt with no answer behind it blocks forever.
func (a Asking) CanAsk() bool {
	if a.Interactive == nil {
		return false
	}
	return a.Interactive()
}

// Pick asks the operator to choose from rows.
//
// It refuses an empty list with ErrNoModels rather than opening a menu over
// nothing, which would render a frame with no rows to select and no way to
// leave with a model.
func (a Asking) Pick(rows []ModelRow) (string, error) {
	if len(rows) == 0 {
		return "", ErrNoModels
	}
	if a.Menu == nil {
		return "", errors.New("no model picker is wired for this launch")
	}
	return a.Menu(rows)
}
