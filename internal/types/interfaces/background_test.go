package interfaces

import "testing"

func TestIsTerminalWorkState(t *testing.T) {
	terminal := []string{
		WorkStateSucceeded, WorkStateFailed, WorkStateCancelled, WorkStateLost,
	}
	for _, s := range terminal {
		if !IsTerminalWorkState(s) {
			t.Errorf("IsTerminalWorkState(%q) = false, want true", s)
		}
	}
	nonTerminal := []string{WorkStateRunning, WorkStateUnknown, "queued", "timeout", "", "bogus"}
	for _, s := range nonTerminal {
		if IsTerminalWorkState(s) {
			t.Errorf("IsTerminalWorkState(%q) = true, want false", s)
		}
	}
}
