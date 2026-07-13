package diffui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lenis2000/mrevdiff/pkg/diffreview"
)

func typeSearch(t *testing.T, m Model, query string) Model {
	t.Helper()
	m = pressKey(t, m, "/")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(query)})
	nm, ok := next.(Model)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}
	next, _ = nm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	nm, ok = next.(Model)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}
	return nm
}

// TestSearchJumpsAndSteps pins the / search: matches by pair ID/source,
// jumps the cursor, and n/N wrap through the match list.
func TestSearchJumpsAndSteps(t *testing.T) {
	m := New(fixtureReview(), Options{})
	m.Width, m.Height = 120, 40

	m = typeSearch(t, m, "added")
	if currentID(m) != "added" {
		t.Fatalf("search should jump to the matching pair, cursor at %s", currentID(m))
	}
	if !strings.Contains(m.Status, "match 1/1") {
		t.Fatalf("status should report match position, got %q", m.Status)
	}

	// No match: cursor stays, status explains.
	before := currentID(m)
	m = typeSearch(t, m, "zebra-quagga-nonexistent")
	if currentID(m) != before {
		t.Fatalf("failed search must not move the cursor")
	}
	if !strings.Contains(m.Status, "no match") {
		t.Fatalf("status should report no match, got %q", m.Status)
	}

	// Esc cancels input without side effects.
	m = pressKey(t, m, "/")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.Search != nil && m.Search.Typing {
		t.Fatalf("esc must cancel search input")
	}
}

func TestSearchWrapsWithN(t *testing.T) {
	m := New(fixtureManyChangedReview(6), Options{})
	m.Width, m.Height = 120, 40
	m = typeSearch(t, m, "p0") // matches p00..p05
	if len(m.Search.Matches) < 2 {
		t.Fatalf("expected multiple matches, got %d", len(m.Search.Matches))
	}
	first := currentID(m)
	m = pressKey(t, m, "n")
	if currentID(m) == first {
		t.Fatalf("n must advance to the next match")
	}
	for i := 0; i < len(m.Search.Matches)-1; i++ {
		m = pressKey(t, m, "n")
	}
	if currentID(m) != first {
		t.Fatalf("n must wrap back to the first match, got %s", currentID(m))
	}
	m = pressKey(t, m, "N")
	if currentID(m) == first {
		t.Fatalf("N must step backwards")
	}
}

// TestAnnListJumpAndDelete pins the @ overlay: enter jumps to the pair,
// d deletes the annotation from the sidecar.
func TestAnnListJumpAndDelete(t *testing.T) {
	m := New(fixtureReview(), Options{})
	m.Width, m.Height = 120, 40
	m.ensureSidecar().Annotations = []diffreview.Annotation{
		{PairID: "moved", Note: "check the displaced lemma"},
		{PairID: "added", Note: "new paragraph needs a citation"},
	}

	m = pressKey(t, m, "@")
	if m.AnnList == nil || len(m.AnnList.Entries) != 2 {
		t.Fatalf("@ should open the list with 2 entries, got %+v", m.AnnList)
	}
	view := m.View()
	if !strings.Contains(view, "check the displaced lemma") {
		t.Fatalf("list overlay should show the notes:\n%s", view)
	}

	// Jump to the second entry.
	m = pressKey(t, m, "j")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.AnnList != nil {
		t.Fatalf("enter should close the list")
	}
	if currentID(m) != "added" {
		t.Fatalf("enter should jump to the annotated pair, cursor at %s", currentID(m))
	}

	// Reopen and delete the first entry.
	m = pressKey(t, m, "@")
	m = pressKey(t, m, "d")
	if len(m.ensureSidecar().Annotations) != 1 {
		t.Fatalf("d should delete the annotation from the sidecar")
	}
	if len(m.AnnList.Entries) != 1 {
		t.Fatalf("d should remove the row from the list")
	}
}

// TestInfoOverlayShowsScopeAndDescription pins the i popup.
func TestInfoOverlayShowsScopeAndDescription(t *testing.T) {
	m := New(fixtureReview(), Options{Description: "rewrote Lemma 3.2 per referee 2"})
	m.Width, m.Height = 120, 40
	m = pressKey(t, m, "i")
	if !m.ShowInfo {
		t.Fatalf("i should open the info popup")
	}
	view := m.View()
	for _, needle := range []string{"review scope", "Pairs:", "rewrote Lemma 3.2 per referee 2"} {
		if !strings.Contains(view, needle) {
			t.Fatalf("info overlay missing %q in:\n%s", needle, view)
		}
	}
	m = pressKey(t, m, "j")
	if m.ShowInfo {
		t.Fatalf("any key should close the info popup")
	}
}

// TestIncrementalSearchJumpsWhileTypingAndEscRestores pins the / UX: the
// cursor follows the first match live as the query is typed, the status
// carries a match count, and Esc puts the cursor back where / was pressed.
func TestIncrementalSearchJumpsWhileTypingAndEscRestores(t *testing.T) {
	m := New(fixtureReview(), Options{})
	m.Width = 120
	m.Height = 30
	origin := m.Cursor

	m = pressKey(t, m, "/")
	for _, r := range "Added" {
		m = pressKey(t, m, string(r))
	}
	if m.Review.Pairs[m.Cursor].ID != "added" {
		t.Fatalf("incremental search should jump to the added pair, cursor on %q", m.Review.Pairs[m.Cursor].ID)
	}
	if !strings.Contains(m.Status, "match") {
		t.Fatalf("status should carry a live match count, got %q", m.Status)
	}
	m = pressSpecial(t, m, tea.KeyEsc)
	if m.Cursor != origin {
		t.Fatalf("Esc should restore the search origin cursor %d, got %d", origin, m.Cursor)
	}
}

// TestSearchReportsFilterHiddenMatches pins the widened no-match report:
// a query that only hits pairs hidden by the active filter must say so
// instead of dead-ending on "no match".
func TestSearchReportsFilterHiddenMatches(t *testing.T) {
	m := New(fixtureReview(), Options{})
	m.Width = 120
	m.Height = 30
	m.Filter = FilterChanged // hides the unchanged pair

	m = pressKey(t, m, "/")
	for _, r := range "Same paragraph" {
		m = pressKey(t, m, string(r))
	}
	m = pressSpecial(t, m, tea.KeyEnter)
	if !strings.Contains(m.Status, "other pairs") {
		t.Fatalf("no-match status should mention filter-hidden matches, got %q", m.Status)
	}
}
