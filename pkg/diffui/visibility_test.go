package diffui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/lenis2000/mrevdiff/pkg/diffreview"
	"github.com/lenis2000/mrevdiff/pkg/parser"
)

// longBlockReview builds a review whose single pair holds an n-line block, so
// the source pane has to scroll.
func longBlockReview(n int) *diffreview.Review {
	var sb strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&sb, "line %02d of the block\n", i)
	}
	blk := &parser.Block{
		Kind:      parser.KindParagraph,
		Source:    strings.TrimRight(sb.String(), "\n"),
		StartLine: 1,
		EndLine:   n,
	}
	return &diffreview.Review{
		Pairs: []diffreview.Pair{{ID: "p1", Status: diffreview.Changed, Old: blk, New: blk}},
	}
}

// TestSourceCursorRowIsPainted pins the pane-height invariant: the renderer is
// sized with sourceBodyRows, so the row the cursor sits on is always among the
// rows renderPaneRaw paints. Sizing the renderer one row taller than the pane
// clipped the bottom row — and at the end of a block that row is the cursor.
func TestSourceCursorRowIsPainted(t *testing.T) {
	const lines = 40
	m := New(longBlockReview(lines), Options{})
	m.Width, m.Height = 120, 40
	m.Cursor = 0

	for _, paneH := range []int{8, 12, 20, 30} {
		body := RenderPairSourceSideHighlighted(
			m.CurrentDisplayPair(), false, 60, sourceBodyRows(paneH), 0, lines)
		pane := m.renderPaneRaw("New source", body, 62, paneH, true)
		want := fmt.Sprintf("line %02d of the block", lines)
		if !strings.Contains(pane, want) {
			t.Errorf("paneH=%d: cursor is on the last line but %q is not painted:\n%s", paneH, want, pane)
		}
	}
}

// TestSearchHighlightPaints pins that an active / query is actually painted in
// the source panes: wrapPartsHard used to drop sourcePart.Search, so
// searchMatchStyle was unreachable and nothing was ever highlighted.
func TestSearchHighlightPaints(t *testing.T) {
	parts := decorateSearchParts(
		[]sourcePart{{Text: "the beta value", Kind: sourcePartEqual}}, "beta")
	var flagged int
	for _, p := range parts {
		if p.Search {
			flagged++
		}
	}
	if flagged == 0 {
		t.Fatal("decorateSearchParts flagged nothing — fixture is wrong")
	}
	var survived int
	for _, chunk := range wrapPartsHard(parts, 80) {
		for _, p := range chunk {
			if p.Search {
				survived++
			}
		}
	}
	if survived != flagged {
		t.Errorf("wrapPartsHard kept %d of %d Search-flagged parts; the highlight never reaches styleSourceParts", survived, flagged)
	}

	// End to end: the rendered frame must carry searchMatchStyle's background.
	// Under `go test` there is no TTY, so lipgloss would otherwise strip every
	// escape and the assertion would pass vacuously.
	restore := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(restore)

	m := New(fixtureReview(), Options{})
	m.Width, m.Height = 160, 40
	m.Search = &searchState{Typing: true, Input: "beta"}
	if view := m.View(); !strings.Contains(view, "48;5;220") {
		t.Error("rendered view with an active search carries no searchMatchStyle — nothing is highlighted")
	}
}

// TestScrollThumbVisibleAtBottom pins that the scroll thumb still shows when
// the pane is scrolled to the end. Sizing the thumb with the renderer's window
// (one row taller than the painted body) pushed its last row onto the bottom
// border, where overlayScrollThumb drops it — so on a long block the thumb
// vanished exactly when it mattered.
func TestScrollThumbVisibleAtBottom(t *testing.T) {
	const paneH = 30
	lines := make([]string, paneH)
	for i := range lines {
		lines[i] = "│" + strings.Repeat(" ", 20) + "│"
	}
	pane := strings.Join(lines, "\n")

	visible := sourceBodyRows(paneH)
	for _, total := range []int{100, 400, 2000} {
		out := overlayScrollThumb(pane, total, visible, total-visible, 2)
		if n := strings.Count(out, "┃"); n == 0 {
			t.Errorf("total=%d: scrolled to the bottom and the thumb is not drawn at all", total)
		}
	}
}

// TestMouseIgnoredWhileModalOpen pins that clicks and wheel events do not fall
// through a modal into the panes underneath. The full-screen overlays do not
// even draw the panes, so a click was being mapped through a layout that was
// not on screen — silently moving the review cursor or folding a group.
func TestMouseIgnoredWhileModalOpen(t *testing.T) {
	overlays := []struct {
		name string
		set  func(*Model)
	}{
		{"help", func(m *Model) { m.ShowHelp = true }},
		{"info", func(m *Model) { m.ShowInfo = true }},
		{"palette", func(m *Model) { m.Palette = &paletteState{} }},
		{"annlist", func(m *Model) { m.AnnList = &annListState{} }},
		{"search", func(m *Model) { m.Search = &searchState{Typing: true, Input: "a"} }},
	}
	buttons := []struct {
		name string
		btn  tea.MouseButton
	}{
		{"click", tea.MouseButtonLeft},
		{"wheel", tea.MouseButtonWheelDown},
	}

	for _, ov := range overlays {
		for _, b := range buttons {
			t.Run(ov.name+"/"+b.name, func(t *testing.T) {
				m := New(fixtureReview(), Options{})
				m.Width, m.Height = 160, 40
				m.Cursor = 0
				ov.set(&m)

				next, _ := m.Update(tea.MouseMsg{
					X: 1, Y: 5,
					Action: tea.MouseActionPress,
					Button: b.btn,
				})
				nm := next.(Model)
				if nm.Cursor != 0 {
					t.Errorf("%s reached the panes behind the %s overlay: cursor 0 -> %d", b.name, ov.name, nm.Cursor)
				}
				if len(nm.Collapsed) != 0 {
					t.Errorf("%s folded an outline group behind the %s overlay", b.name, ov.name)
				}
			})
		}
	}
}

// TestHelpListsNewBindings pins that the ? overlay documents the bindings that
// exist. ) ( and O shipped bound but undocumented, so they were undiscoverable.
func TestHelpListsNewBindings(t *testing.T) {
	m := New(fixtureReview(), Options{})
	m.Width, m.Height = 160, 60
	m.ShowHelp = true
	help := m.View()

	for _, want := range []string{"annotated pair", "sidecar"} {
		if !strings.Contains(help, want) {
			t.Errorf("help overlay does not document %q", want)
		}
	}
}

// TestHelpIsModal pins that the ? help claims the keyboard: it is a full-screen
// overlay that hides the panes, so nothing may fire behind it. Esc and ? close
// it, q closes it WITHOUT quitting the review (q only quits from the main view),
// and every other key is swallowed.
func TestHelpIsModal(t *testing.T) {
	open := func() Model {
		m := New(fixtureReview(), Options{})
		m.Width, m.Height = 160, 40
		m.ShowHelp = true
		return m
	}
	press := func(m Model, k string) Model {
		var msg tea.KeyMsg
		if k == "esc" {
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		} else {
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		next, _ := m.Update(msg)
		return next.(Model)
	}

	for _, k := range []string{"esc", "?", "q"} {
		if m := press(open(), k); m.ShowHelp {
			t.Errorf("%q did not close the help overlay", k)
		}
	}
	// q must close the help, never quit the review from under it.
	if m := press(open(), "q"); m.quitting {
		t.Error("q quit the review from inside the help overlay; it must only quit from the main view")
	}
	// Other keys are swallowed: nothing moves behind the overlay.
	m := open()
	before := m.Cursor
	after := press(m, "j")
	if !after.ShowHelp {
		t.Error("j closed the help overlay")
	}
	if after.Cursor != before {
		t.Errorf("j moved the cursor behind the help overlay: %d -> %d", before, after.Cursor)
	}
}
