package diffui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func clickAt(t *testing.T, m Model, x, y int) Model {
	t.Helper()
	next, _ := m.Update(tea.MouseMsg{
		X: x, Y: y,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	nm, ok := next.(Model)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}
	return nm
}

// TestOutlineClickJumpsToRow pins click-to-jump: clicking a pair row in
// the outline moves the cursor to that pair, exactly like j/k would.
func TestOutlineClickJumpsToRow(t *testing.T) {
	m := New(fixtureReview(), Options{})
	m.Width = 160
	m.Height = 40

	rows := m.outlineRows()
	target := -1
	for i, row := range rows {
		if !row.Group && row.PairIndex >= 0 && row.PairIndex != m.Cursor {
			target = i
			break
		}
	}
	if target < 0 {
		t.Fatalf("fixture outline has no clickable pair row away from the cursor")
	}
	// With the small fixture every row is visible, so the viewport starts
	// at row 0 and row i is drawn at screen y = 3 + i.
	m = clickAt(t, m, 1, 3+target)
	if m.Cursor != rows[target].PairIndex {
		t.Fatalf("click on outline row %d moved cursor to %d, want pair %d",
			target, m.Cursor, rows[target].PairIndex)
	}
}

// TestOutlineClickTogglesGroupFold pins that clicking a section header
// folds it, and clicking again unfolds.
func TestOutlineClickTogglesGroupFold(t *testing.T) {
	m := New(fixtureReview(), Options{})
	m.Width = 160
	m.Height = 40

	rows := m.outlineRows()
	target := -1
	for i, row := range rows {
		if row.Group && row.GroupKey != "" {
			target = i
			break
		}
	}
	if target < 0 {
		t.Fatalf("fixture outline has no group row")
	}
	key := rows[target].GroupKey
	m = clickAt(t, m, 1, 3+target)
	if !m.Collapsed[key] {
		t.Fatalf("click on group row should fold it (status %q)", m.Status)
	}
	// The fold may reflow rows; find the group's new position to unfold.
	rows = m.outlineRows()
	target = -1
	for i, row := range rows {
		if row.Group && row.GroupKey == key {
			target = i
			break
		}
	}
	if target < 0 {
		t.Fatalf("folded group disappeared from the outline")
	}
	m = clickAt(t, m, 1, 3+target)
	if m.Collapsed[key] {
		t.Fatalf("second click should unfold the group")
	}
}

// TestSourceClickPlacesLineCursor pins that clicking a line in the new
// source pane moves the source-line cursor onto that line.
func TestSourceClickPlacesLineCursor(t *testing.T) {
	m := New(fixtureReview(), Options{})
	m.Width = 160
	m.Height = 40
	// Select the changed pair (two lines: "Alpha" / "new beta").
	for i := range m.Review.Pairs {
		if m.Review.Pairs[i].ID == "changed" {
			m.Cursor = i
			break
		}
	}
	m.SourceLineCursor = 1

	geo, ok := m.sourcePaneGeometry(PaneNewSource)
	if !ok {
		t.Fatalf("no geometry for the new source pane in the default layout")
	}
	// Click the second rendered row (the pair's second line).
	m = clickAt(t, m, geo.x0+2, 3)
	if m.Focus != PaneNewSource {
		t.Fatalf("click should focus the new source pane, got %v", m.Focus)
	}
	if m.SourceLineCursor != 2 {
		t.Fatalf("click on the second source row should set line cursor 2, got %d (status %q)",
			m.SourceLineCursor, m.Status)
	}
	if !strings.Contains(m.Status, "new") {
		t.Fatalf("status should report the new-side line, got %q", m.Status)
	}
}
