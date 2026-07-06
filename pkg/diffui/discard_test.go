package diffui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestQDiscardRequiresTwoPresses pins the discard-quit contract: the first
// Q only arms the discard (with a status warning), any other key disarms
// it, and a second consecutive Q quits with Discarded set so the caller
// skips the sidecar save and the stdout emit.
func TestQDiscardRequiresTwoPresses(t *testing.T) {
	m := New(fixtureReview(), Options{})

	m = pressKey(t, m, "Q")
	if m.Discarded {
		t.Fatalf("single Q must not discard")
	}
	if m.Status == "" {
		t.Fatalf("first Q should explain the second-press requirement in the status line")
	}

	// Any other key disarms the pending discard.
	m = pressKey(t, m, "j")
	m = pressKey(t, m, "Q")
	if m.Discarded {
		t.Fatalf("Q after a disarming key must not discard")
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Q")})
	nm, ok := next.(Model)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}
	if !nm.Discarded {
		t.Fatalf("second consecutive Q must set Discarded")
	}
	if cmd == nil {
		t.Fatalf("second Q must quit")
	}
}

// TestMouseDisarmsPendingDiscard pins the fix for the stale-armed-Q bug:
// mouse activity between the two Q presses must disarm the discard, or an
// accidental Q followed by minutes of wheel navigation would let a later
// Q discard the session with no visible warning.
func TestMouseDisarmsPendingDiscard(t *testing.T) {
	m := New(fixtureReview(), Options{})
	m = pressKey(t, m, "Q")

	next, _ := m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	nm, ok := next.(Model)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}

	nm = pressKey(t, nm, "Q")
	if nm.Discarded {
		t.Fatalf("Q after mouse activity must be a fresh first press, not a discard")
	}
}

// TestPlainQuitDoesNotDiscard guards against q ever picking up the
// discard flag: q is the keep-and-emit path.
func TestPlainQuitDoesNotDiscard(t *testing.T) {
	m := New(fixtureReview(), Options{})
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	nm, ok := next.(Model)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}
	if nm.Discarded {
		t.Fatalf("q must not discard")
	}
	if cmd == nil {
		t.Fatalf("q must quit")
	}
}
