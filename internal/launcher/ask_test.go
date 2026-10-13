package launch

import (
	"reflect"
	"testing"
)

// The production Asking is the real terminal probe and the real menu. The
// zero value refuses, so a launch that reached it would stop with the message
// a launch on a pipe gets — on a machine with a terminal in front of it. The
// pointers are compared rather than the fields' presence because a nil check
// passes just as well for a probe wired to the wrong thing.
func TestAskOperatorWiresTheRealTerminalAndMenu(t *testing.T) {
	ask := AskOperator()

	if ask.Interactive == nil {
		t.Fatal("AskOperator has no probe: every launch would report nobody to ask")
	}
	if ask.Menu == nil {
		t.Fatal("AskOperator has no menu: a real launch could never open the picker")
	}
	if got, want := reflect.ValueOf(ask.Menu).Pointer(), reflect.ValueOf(defaultModelPickerMenu).Pointer(); got != want {
		t.Error("AskOperator's menu is not the real picker menu")
	}
}

// CanAsk reports what the probe says, so a caller decides on the probe's answer
// rather than on whether a probe was wired at all.
func TestAskingCanAskFollowsTheProbe(t *testing.T) {
	for _, interactive := range []bool{true, false} {
		ask := Asking{Interactive: func() bool { return interactive }}
		if got := ask.CanAsk(); got != interactive {
			t.Errorf("CanAsk with a probe saying %v = %v", interactive, got)
		}
	}
}
