package diffui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lenis2000/mrevdiff/pkg/diffreview"
)

func TestSearchHistoryRecall(t *testing.T) {
	m := New(fixtureReview(), Options{})
	m.Width = 120
	m.Height = 30

	submit := func(query string) {
		m = pressKey(t, m, "/")
		for _, r := range query {
			m = pressKey(t, m, string(r))
		}
		m = pressSpecial(t, m, tea.KeyEnter)
	}
	submit("alpha")
	submit("beta")

	m = pressKey(t, m, "/")
	m = pressSpecial(t, m, tea.KeyUp)
	if m.Search.Input != "beta" {
		t.Fatalf("first Up should recall the newest query, got %q", m.Search.Input)
	}
	m = pressSpecial(t, m, tea.KeyUp)
	if m.Search.Input != "alpha" {
		t.Fatalf("second Up should recall the older query, got %q", m.Search.Input)
	}
	m = pressSpecial(t, m, tea.KeyDown)
	m = pressSpecial(t, m, tea.KeyDown)
	if m.Search.Input != "" {
		t.Fatalf("Down past the newest entry should restore the empty draft, got %q", m.Search.Input)
	}
}

func TestAnnotationJumpWraps(t *testing.T) {
	m := New(fixtureReview(), Options{})
	m.Width = 120
	m.Height = 30
	var annotated []int
	for i := range m.Review.Pairs {
		switch m.Review.Pairs[i].ID {
		case "changed", "added":
			m.Annotations[m.Review.Pairs[i].ID] = "note"
			annotated = append(annotated, i)
		}
	}
	if len(annotated) != 2 {
		t.Fatalf("fixture pairs not found")
	}

	m.Cursor = 0
	m = pressKey(t, m, ")")
	if m.Cursor != annotated[0] {
		t.Fatalf(") should jump to the first annotated pair %d, got %d", annotated[0], m.Cursor)
	}
	m = pressKey(t, m, ")")
	if m.Cursor != annotated[1] {
		t.Fatalf("second ) should jump to %d, got %d", annotated[1], m.Cursor)
	}
	m = pressKey(t, m, ")")
	if m.Cursor != annotated[0] {
		t.Fatalf(") at the last annotation should wrap to %d, got %d", annotated[0], m.Cursor)
	}
	m = pressKey(t, m, "(")
	if m.Cursor != annotated[1] {
		t.Fatalf("( should wrap backwards to %d, got %d", annotated[1], m.Cursor)
	}
}

func TestSidecarFlushWritesWithoutQuitting(t *testing.T) {
	m := New(fixtureReview(), Options{})
	m.Width = 120
	m.Height = 30
	m.SidecarPath = filepath.Join(t.TempDir(), "review.sidecar.json")
	for i := range m.Review.Pairs {
		if m.Review.Pairs[i].ID == "changed" {
			m.Cursor = i
		}
	}
	m = pressKey(t, m, "a")
	m.Popup.TA.SetValue("flush me")
	m = pressSpecial(t, m, tea.KeyEnter)

	m = pressKey(t, m, "O")
	if !strings.Contains(m.Status, "annotations written") {
		t.Fatalf("flush status = %q", m.Status)
	}
	side, err := diffreview.LoadSidecar(m.SidecarPath)
	if err != nil {
		t.Fatalf("load flushed sidecar: %v", err)
	}
	if notes := side.AnnotationNotes(); notes["changed"] != "flush me" {
		t.Fatalf("flushed sidecar missing annotation: %#v", notes)
	}
	if st, err := os.Stat(m.SidecarPath); err != nil || !m.SidecarMTime.Equal(st.ModTime()) {
		t.Fatalf("flush should refresh SidecarMTime")
	}
	// The base snapshot now reflects the flushed state, so the final
	// quit-save's three-way merge treats it as already persisted.
	if base := m.SidecarBase.AnnotationNotes(); base["changed"] != "flush me" {
		t.Fatalf("flush should advance SidecarBase: %#v", base)
	}
}

// TestDecorateSearchParts pins the pure decoration: term occurrences are
// split out with Search=true, case-insensitively, without changing the
// concatenated text (wrapping and the click mapping depend on that).
func TestDecorateSearchParts(t *testing.T) {
	parts := []sourcePart{
		{Text: "Alpha and ", Kind: sourcePartEqual},
		{Text: "new Beta", Kind: sourcePartAdd},
	}
	out := decorateSearchParts(parts, "beta")
	var text string
	found := false
	for _, p := range out {
		text += p.Text
		if p.Search {
			found = true
			if !strings.EqualFold(p.Text, "beta") {
				t.Fatalf("search part text = %q", p.Text)
			}
			if p.Kind != sourcePartAdd {
				t.Fatalf("search part must keep its diff kind, got %v", p.Kind)
			}
		}
	}
	if !found {
		t.Fatalf("no search part produced: %#v", out)
	}
	if text != "Alpha and new Beta" {
		t.Fatalf("decoration changed the text: %q", text)
	}
	if got := decorateSearchParts(parts, ""); &got[0] != &parts[0] {
		t.Fatalf("empty term should return parts unchanged")
	}
}

// TestActiveSearchTermFollowsSearchState pins the View plumbing: the term
// is live while typing and cleared when the search is cancelled.
func TestActiveSearchTermFollowsSearchState(t *testing.T) {
	m := New(fixtureReview(), Options{})
	m.Width = 160
	m.Height = 30

	m = pressKey(t, m, "/")
	for _, r := range "Beta" {
		m = pressKey(t, m, string(r))
	}
	if got := m.activeSearchTerm(); got != "beta" {
		t.Fatalf("typing term = %q, want beta", got)
	}
	m = pressSpecial(t, m, tea.KeyEsc)
	if got := m.activeSearchTerm(); got != "" {
		t.Fatalf("cancelled search should clear the term, got %q", got)
	}
}

func TestOverlayScrollThumb(t *testing.T) {
	pane := strings.Join([]string{
		"┌───┐",
		"│t  │",
		"│a  │",
		"│b  │",
		"│c  │",
		"└───┘",
	}, "\n")
	// 12 total rows, 4 visible starting at 0, body rows begin at line 1.
	out := overlayScrollThumb(pane, 12, 4, 0, 1)
	lines := strings.Split(out, "\n")
	if !strings.HasSuffix(lines[1], "┃") {
		t.Fatalf("thumb should start on the first body row:\n%s", out)
	}
	if strings.HasSuffix(lines[4], "┃") {
		t.Fatalf("thumb at offset 0 must not reach the last body row:\n%s", out)
	}
	// Scrolled to the bottom, the thumb hugs the last body row.
	out = overlayScrollThumb(pane, 12, 4, 8, 1)
	lines = strings.Split(out, "\n")
	if !strings.HasSuffix(lines[4], "┃") {
		t.Fatalf("thumb at max offset should reach the last body row:\n%s", out)
	}
	if strings.HasSuffix(lines[5], "┃") {
		t.Fatalf("thumb must never touch the bottom border:\n%s", out)
	}
	// Everything visible: untouched.
	if got := overlayScrollThumb(pane, 4, 4, 0, 1); got != pane {
		t.Fatalf("no thumb when content fits")
	}
}
