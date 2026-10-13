package launch

import (
	"bufio"
	"strings"
	"testing"
)

// answerPrompt points the shared confirm reader at a canned stdin for one
// test, standing in for whatever the operator would have typed. Without it a
// test of the prompt path reads the real stdin, which in `go test` is
// immediately at EOF: every answer comes back false, and a test proves nothing.
func answerPrompt(t *testing.T, answer string) {
	t.Helper()
	previous := confirmReader
	// Registered before the reader is replaced, so the restore is in place for
	// the whole test rather than depending on the order of two statements.
	t.Cleanup(func() { confirmReader = previous })
	confirmReader = bufio.NewReader(strings.NewReader(answer))
}

// --yes auto-approves without asking. That is the whole point of the flag: a
// script that passes it has no terminal to answer on, so a prompt that
// reached for stdin anyway would return the read's EOF — a refusal wearing a
// question's clothes, on a launch the operator asked to be unattended.
func TestSetConfirmPolicyAutoApprovesWithoutReadingStdin(t *testing.T) {
	t.Cleanup(func() { SetConfirmPolicy(false) })
	answerPrompt(t, "n\n")

	SetConfirmPolicy(true)
	ok, err := ConfirmPrompt("Install Claude Code now?")
	if err != nil {
		t.Fatalf("ConfirmPrompt under --yes: %v", err)
	}
	if !ok {
		t.Fatal("--yes did not approve; an unattended launch would stop on a prompt it cannot answer")
	}
}

// Without --yes the prompt still asks and still obeys the answer, so the flag
// is an opt-in and clearing it restores the interactive path rather than
// leaving the gate stuck open.
func TestConfirmPromptWithoutYesReadsTheAnswer(t *testing.T) {
	t.Cleanup(func() { SetConfirmPolicy(false) })

	for _, tc := range []struct {
		answer string
		want   bool
	}{
		{"y\n", true},
		{"yes\n", true},
		{"n\n", false},
		{"no\n", false},
		{"\n", true}, // the default, an empty line accepting it
		{"nope\n", false},
	} {
		answerPrompt(t, tc.answer)
		got, err := ConfirmPrompt("Install Claude Code now?")
		if err != nil {
			t.Fatalf("ConfirmPrompt answering %q: %v", tc.answer, err)
		}
		if got != tc.want {
			t.Fatalf("ConfirmPrompt answering %q = %v, want %v", tc.answer, got, tc.want)
		}
	}
}
