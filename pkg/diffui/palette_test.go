package diffui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// paletteHas reports whether the filtered palette currently lists the action.
func paletteHas(p *paletteState, a Action) bool {
	if p == nil {
		return false
	}
	for _, it := range p.Items {
		if it.Action == a {
			return true
		}
	}
	return false
}

func TestPaletteActionsAreCommandsNotMotions(t *testing.T) {
	visible := map[Action]bool{}
	for _, meta := range paletteActions() {
		if meta.Palette == "" {
			t.Fatalf("paletteActions returned %s with an empty label", meta.Action)
		}
		visible[meta.Action] = true
	}
	wantIn := []Action{ActionReload, ActionRegimeToggle, ActionSkim, ActionCompare, ActionPreview, ActionSearch}
	for _, a := range wantIn {
		if !visible[a] {
			t.Errorf("palette is missing command %s", a)
		}
	}
	wantOut := []Action{
		ActionNext, ActionPrev, ActionJumpDown, ActionJumpUp, ActionFirst, ActionLast,
		ActionSectionNext, ActionFocusNext, ActionSourceLineNext, ActionSearchNext,
		ActionResizeGrow, ActionResizeShrink, ActionHelp, ActionQuit, ActionDiscard, ActionPalette,
	}
	for _, a := range wantOut {
		if visible[a] {
			t.Errorf("palette should not list motion/keyboard-only action %s", a)
		}
	}
}

func TestPaletteOpensViaColonKey(t *testing.T) {
	m := New(fixtureReview(), Options{})
	m = pressKey(t, m, ":")
	if m.Palette == nil {
		t.Fatalf(": did not open the command palette")
	}
	if len(m.Palette.Items) != len(paletteActions()) {
		t.Fatalf("fresh palette shows %d items, want %d", len(m.Palette.Items), len(paletteActions()))
	}
}

func TestPaletteFilterMatchesRecompile(t *testing.T) {
	m := New(fixtureReview(), Options{})
	m = pressKey(t, m, ":")
	m = pressRunes(t, m, "recompile")
	if m.Palette == nil {
		t.Fatalf("palette closed while typing")
	}
	if len(m.Palette.Items) != 1 {
		t.Fatalf("query %q matched %d items, want 1: %+v", "recompile", len(m.Palette.Items), m.Palette.Items)
	}
	if got := m.Palette.Items[0].Action; got != ActionReload {
		t.Fatalf("recompile matched %s, want %s", got, ActionReload)
	}
}

func TestPaletteEnterRunsSelectedCommand(t *testing.T) {
	m := New(fixtureReview(), Options{})
	if m.Filter != FilterChanged {
		t.Fatalf("fixture starts on filter %s, want changed", m.Filter)
	}
	m = pressKey(t, m, ":")
	m = pressRunes(t, m, "outline filter") // uniquely matches "Cycle outline filter"
	if !paletteHas(m.Palette, ActionFilterCycle) || len(m.Palette.Items) != 1 {
		t.Fatalf("expected only filter-cycle to match, got %+v", m.Palette.Items)
	}
	m = pressSpecial(t, m, tea.KeyEnter)
	if m.Palette != nil {
		t.Fatalf("palette stayed open after enter")
	}
	if m.Filter == FilterChanged {
		t.Fatalf("filter-cycle command did not run (filter still %s)", m.Filter)
	}
}

func TestPaletteEnterClosesBeforeOpeningNextOverlay(t *testing.T) {
	m := New(fixtureReview(), Options{})
	m = pressKey(t, m, ":")
	m = pressRunes(t, m, "search pairs")
	if !paletteHas(m.Palette, ActionSearch) {
		t.Fatalf("expected search command to match, got %+v", m.Palette.Items)
	}
	m = pressSpecial(t, m, tea.KeyEnter)
	if m.Palette != nil {
		t.Fatalf("palette not closed before the search overlay opened")
	}
	if m.Search == nil {
		t.Fatalf("running the search command did not open the / search prompt")
	}
}

func TestPaletteNoMatchThenEnterIsNoop(t *testing.T) {
	m := New(fixtureReview(), Options{})
	m = pressKey(t, m, ":")
	m = pressRunes(t, m, "zzzznope")
	if len(m.Palette.Items) != 0 {
		t.Fatalf("query %q unexpectedly matched %+v", "zzzznope", m.Palette.Items)
	}
	m = pressSpecial(t, m, tea.KeyEnter)
	if m.Palette != nil {
		t.Fatalf("empty-match enter should still close the palette")
	}
	if m.Status != "no matching command" {
		t.Fatalf("status = %q, want %q", m.Status, "no matching command")
	}
}

func TestPaletteEscCloses(t *testing.T) {
	m := New(fixtureReview(), Options{})
	m = pressKey(t, m, ":")
	m = pressSpecial(t, m, tea.KeyEsc)
	if m.Palette != nil {
		t.Fatalf("esc did not close the palette")
	}
}

func TestPaletteCursorClamps(t *testing.T) {
	m := New(fixtureReview(), Options{})
	m = pressKey(t, m, ":")
	n := len(m.Palette.Items)
	if n < 3 {
		t.Fatalf("need at least 3 commands to test clamping, have %d", n)
	}
	m = pressSpecial(t, m, tea.KeyUp) // already at top
	if m.Palette.Cursor != 0 {
		t.Fatalf("up at top moved cursor to %d, want 0", m.Palette.Cursor)
	}
	for i := 0; i < n+5; i++ {
		m = pressSpecial(t, m, tea.KeyDown)
	}
	if m.Palette.Cursor != n-1 {
		t.Fatalf("down past end left cursor at %d, want %d", m.Palette.Cursor, n-1)
	}
}

func TestPaletteBackspaceEditsQuery(t *testing.T) {
	m := New(fixtureReview(), Options{})
	m = pressKey(t, m, ":")
	m = pressRunes(t, m, "recompilez") // no match
	if len(m.Palette.Items) != 0 {
		t.Fatalf("expected no match for %q, got %+v", "recompilez", m.Palette.Items)
	}
	m = pressSpecial(t, m, tea.KeyBackspace) // -> "recompile", matches reload
	if !paletteHas(m.Palette, ActionReload) || len(m.Palette.Items) != 1 {
		t.Fatalf("backspace did not restore the recompile match: %+v", m.Palette.Items)
	}
}
