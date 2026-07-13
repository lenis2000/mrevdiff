package diffui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lenis2000/mrevdiff/pkg/diffreview"
)

// TestSearchEscRestoresSourceLine pins that backing out of a / search puts the
// in-block line cursor back too. snapCursor rewrites SourceLineCursor with the
// pair's hunk anchor, so restoring only the pair silently moved the reader.
func TestSearchEscRestoresSourceLine(t *testing.T) {
	m := New(fixtureReview(), Options{})
	m.Width, m.Height = 160, 40

	originPair := m.Cursor
	m.SourceLineCursor = 1
	originLine := m.SourceLineCursor

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = next.(Model)
	for _, r := range "zzz-no-such-match" {
		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(Model)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)

	if m.Cursor != originPair {
		t.Errorf("Esc left the cursor on pair %d, want %d", m.Cursor, originPair)
	}
	if m.SourceLineCursor != originLine {
		t.Errorf("Esc left the source line on %d, want %d — the / search moved the reader and did not put them back",
			m.SourceLineCursor, originLine)
	}
}

// TestAnnotationJumpSkipsFilterHiddenPairs pins that ) / ( never claim to jump
// to a pair the filter hides: snapCursor would drag the cursor somewhere else
// while the status line named the pair we meant.
func TestAnnotationJumpSkipsFilterHiddenPairs(t *testing.T) {
	m := New(fixtureReview(), Options{})
	m.Width, m.Height = 160, 40
	m.Filter = FilterChanged

	// "same" is Unchanged, so the default changed-filter hides it.
	hidden := pairIndexByID(m.Review, "same")
	if hidden < 0 {
		t.Fatal("fixture has no 'same' pair")
	}
	m.Annotations = map[string]string{"same": "note on a hidden pair"}

	before := m.Cursor
	next, _ := m.jumpAnnotation(+1)
	nm := next.(Model)

	if nm.Cursor != before {
		t.Errorf("jump moved the cursor to %d for an annotation on a filter-hidden pair (was %d)", nm.Cursor, before)
	}
	if !strings.Contains(nm.Status, "hidden by filter") {
		t.Errorf("status %q does not tell the user the annotation is hidden by the filter", nm.Status)
	}
}

// TestQDiscardRollsBackOFlush pins Q's all-or-nothing contract: O writes this
// session's annotations to disk mid-review, so a later Q must put the sidecar
// back as the review found it rather than leave them there.
func TestQDiscardRollsBackOFlush(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "review.sidecar.json")

	origin := &diffreview.Sidecar{}
	if err := diffreview.SaveSidecar(path, origin); err != nil {
		t.Fatalf("seed sidecar: %v", err)
	}
	seeded, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read seeded sidecar: %v", err)
	}

	m := New(fixtureReview(), Options{SidecarPath: path, SidecarBase: origin})
	const note = "a note that must not survive Q"
	pair := m.pairByID("changed")
	if pair == nil {
		t.Fatal("fixture has no 'changed' pair")
	}
	m.ensureSidecar().UpsertAnnotation(diffreview.AnnotationForPair(m.Review, pair, note))
	m.Annotations["changed"] = note

	next, _ := m.flushSidecar()
	m = next.(Model)
	flushed, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read flushed sidecar: %v", err)
	}
	if !strings.Contains(string(flushed), note) {
		t.Fatalf("O did not write the annotation to disk; fixture is wrong:\n%s", flushed)
	}

	if err := m.RestoreSidecarOnDiscard(); err != nil {
		t.Fatalf("restore: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read restored sidecar: %v", err)
	}
	if strings.Contains(string(after), note) {
		t.Errorf("Q-discard left the annotations O had flushed on disk:\n%s", after)
	}
	if string(after) != string(seeded) {
		t.Errorf("Q-discard did not restore the sidecar the review found:\ngot:  %s\nwant: %s", after, seeded)
	}
}

// TestRebuildDuringBuildIsCoalesced pins that mrevdiff never launches a second
// latexmk against its own in-flight one — that collided on its own lmk-guard
// lock and wedged the pane on "(new PDF needs rebuild)".
func TestRebuildDuringBuildIsCoalesced(t *testing.T) {
	review := fixtureReview()
	review.New = diffreview.Endpoint{
		Kind: diffreview.WorkingFile,
		Path: filepath.Join(t.TempDir(), "paper.tex"),
	}
	m := New(review, Options{})
	m.Width, m.Height = 160, 40
	m.buildInFlight = true

	next, cmd := m.startPDFReload(true)
	nm := next

	if cmd != nil {
		t.Error("a rebuild requested while our own build is in flight spawned a second latexmk")
	}
	if !nm.buildQueued {
		t.Error("the rebuild request was dropped instead of queued behind the running build")
	}
	if !strings.Contains(nm.Status, "already running") {
		t.Errorf("status %q does not tell the user the build is already running", nm.Status)
	}
}
