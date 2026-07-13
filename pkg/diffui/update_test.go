package diffui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lenis2000/mrevdiff/pkg/diffreview"
	"github.com/lenis2000/mrevdiff/pkg/pdf"
	"github.com/lenis2000/mrevdiff/pkg/synctex"
)

func TestCursorMovementIsPairBased(t *testing.T) {
	m := New(fixtureReview(), Options{})
	if currentID(m) != "changed" {
		t.Fatalf("default cursor = %s, want changed", currentID(m))
	}

	m = pressKey(t, m, "j")
	if currentID(m) != "added" {
		t.Fatalf("j moved cursor to %s, want added", currentID(m))
	}

	m = pressKey(t, m, "J")
	if currentID(m) != "moved" {
		t.Fatalf("J moved cursor to %s, want moved", currentID(m))
	}

	m = pressKey(t, m, "K")
	if currentID(m) != "changed" {
		t.Fatalf("K moved cursor to %s, want changed", currentID(m))
	}

	m = pressKey(t, m, "G")
	if currentID(m) != "moved" {
		t.Fatalf("G moved cursor to %s, want moved", currentID(m))
	}

	m = pressKey(t, m, "g")
	m = pressKey(t, m, "g")
	if currentID(m) != "changed" {
		t.Fatalf("gg moved cursor to %s, want changed", currentID(m))
	}
}

func TestCursorMovementAcceptsVimCountPrefixes(t *testing.T) {
	m := New(fixtureManyChangedReview(20), Options{})
	if currentID(m) != "p00" {
		t.Fatalf("default cursor = %s, want p00", currentID(m))
	}

	m = pressKey(t, m, "1")
	m = pressKey(t, m, "0")
	m = pressKey(t, m, "j")
	if currentID(m) != "p10" {
		t.Fatalf("10j moved cursor to %s, want p10", currentID(m))
	}
	if m.CountBuf != "" {
		t.Fatalf("count buffer after motion = %q, want empty", m.CountBuf)
	}

	m = pressKey(t, m, "5")
	m = pressKey(t, m, "k")
	if currentID(m) != "p05" {
		t.Fatalf("5k moved cursor to %s, want p05", currentID(m))
	}
}

func TestUppercaseJKJumpTenDownAndFiveUp(t *testing.T) {
	m := New(fixtureManyChangedReview(20), Options{})

	m = pressKey(t, m, "J")
	if currentID(m) != "p10" {
		t.Fatalf("J moved cursor to %s, want p10", currentID(m))
	}

	m = pressKey(t, m, "K")
	if currentID(m) != "p05" {
		t.Fatalf("K moved cursor to %s, want p05", currentID(m))
	}
}

func TestSectionNavigationUsesPairSectionPaths(t *testing.T) {
	m := New(fixtureReview(), Options{})
	if currentID(m) != "changed" {
		t.Fatalf("default cursor = %s, want changed", currentID(m))
	}

	m = pressKey(t, m, "}")
	if currentID(m) != "deleted" {
		t.Fatalf("} moved cursor to %s, want first pair in Methods", currentID(m))
	}

	m = pressKey(t, m, "{")
	if currentID(m) != "added" {
		t.Fatalf("{ moved cursor to %s, want last pair in Intro", currentID(m))
	}
}

func TestOutlineFoldToggleHidesAndRestoresCurrentGroup(t *testing.T) {
	m := New(fixtureReview(), Options{})
	if currentID(m) != "changed" {
		t.Fatalf("default cursor = %s, want changed", currentID(m))
	}

	m = pressKey(t, m, "z")
	if !m.Collapsed[outlinePathKey([]string{"Intro"})] {
		t.Fatalf("Intro group was not collapsed: %#v", m.Collapsed)
	}
	if currentID(m) != "changed" {
		t.Fatalf("fold should keep source cursor on current pair, got %s", currentID(m))
	}
	if got := strings.Join(visibleIDs(m), ","); got != "changed,deleted,fmt,moved" {
		t.Fatalf("visible ids after fold = %s", got)
	}
	outline := m.renderOutline(120, 10)
	if !strings.Contains(outline, "▸ Intro") || strings.Contains(outline, "Alpha") {
		t.Fatalf("folded outline should show collapsed Intro and hide child rows:\n%s", outline)
	}

	m = pressKey(t, m, "z")
	if m.Collapsed[outlinePathKey([]string{"Intro"})] {
		t.Fatalf("Intro group was not unfolded: %#v", m.Collapsed)
	}
	if got := strings.Join(visibleIDs(m), ","); got != "changed,added,deleted,fmt,moved" {
		t.Fatalf("visible ids after unfold = %s", got)
	}
}

func TestFoldedCursorNavigationDoesNotSkipNextVisiblePair(t *testing.T) {
	m := New(fixtureReview(), Options{})
	m = pressKey(t, m, "z")
	m = pressKey(t, m, "j")
	if currentID(m) != "deleted" {
		t.Fatalf("j from folded Intro moved to %s, want deleted", currentID(m))
	}
	m = pressKey(t, m, "k")
	if currentID(m) != "changed" {
		t.Fatalf("k should move back to folded Intro group; got %s", currentID(m))
	}
	outline := m.renderOutline(120, 10)
	if !strings.Contains(outline, "> ▸ Intro") {
		t.Fatalf("folded group should be selectable after moving up:\n%s", outline)
	}
}

func TestHelpIncludesDiffSpecificKeys(t *testing.T) {
	// Narrow width forces the single-column path so substrings stay on
	// one physical line (the two-column path pads between the columns).
	m := New(fixtureReview(), Options{})
	help := m.RenderHelpBody(80)
	for _, needle := range []string{
		"inline single-line edit",
		"$EDITOR at current line",
		"rerun with --allow-modifications",
		"semantic / coalesced diff regime",
		"fold/unfold outline group",
		"edit annotation",
		"delete annotation",
		"Skim forward-search at line",
		"external compare",
		"undo / redo in-place edits",
		"source line (PDF anchor)",
		"PDF-only zoom",
		"discard annotations/marks",
	} {
		if !strings.Contains(help, needle) {
			t.Fatalf("help missing %q in:\n%s", needle, help)
		}
	}
	// The help must show the live bound keys — default 'x' for the blink
	// comparator here.
	if !strings.Contains(help, "x") {
		t.Fatalf("help should show the blink key")
	}
	// The two-column render must include the same sections.
	mAllow := New(fixtureReview(), Options{AllowModifications: true})
	wide := mAllow.RenderHelpBody(140)
	for _, needle := range []string{"NAVIGATE", "REVIEW", "EDIT (new file)", "PDF", "LAYOUT", "QUIT"} {
		if !strings.Contains(wide, needle) {
			t.Fatalf("wide help missing section %q in:\n%s", needle, wide)
		}
	}
}

// TestHelpReflectsRemappedKeys pins that the overlay shows the user's
// bindings, not the defaults.
func TestHelpReflectsRemappedKeys(t *testing.T) {
	km := NewKeymap()
	km.ApplyFile("unmap x\nmap ctrl+b pdf-blink")
	m := New(fixtureReview(), Options{Keymap: km})
	help := m.RenderHelpBody(80)
	if !strings.Contains(help, "ctrl+b") {
		t.Fatalf("help should show the remapped blink key ctrl+b:\n%s", help)
	}
}

func TestPairNavigationIgnoresInternalDiffHunks(t *testing.T) {
	review := &diffreview.Review{Pairs: []diffreview.Pair{
		{
			ID:       "multi",
			Status:   diffreview.Changed,
			Old:      fixtureBlock("old-multi", 1, "first old\nunchanged middle\nsecond old"),
			New:      fixtureBlock("new-multi", 1, "first new\nunchanged middle\nsecond new"),
			OldIndex: 0,
			NewIndex: 0,
		},
		{
			ID:       "next",
			Status:   diffreview.Added,
			New:      fixtureBlock("new-next", 10, "next pair"),
			OldIndex: -1,
			NewIndex: 1,
		},
	}}
	m := New(review, Options{})
	if currentID(m) != "multi" {
		t.Fatalf("initial cursor = %s", currentID(m))
	}
	m = pressKey(t, m, "j")
	if currentID(m) != "next" {
		t.Fatalf("j should advance to next pair, ignoring internal hunks; got %s line=%d", currentID(m), m.SourceLineCursor)
	}
}

func TestToggleDiffRegimeKeepsCurrentSourceLine(t *testing.T) {
	review := &diffreview.Review{Pairs: []diffreview.Pair{
		{
			ID:             "added-rewrite",
			Status:         diffreview.Added,
			New:            fixtureBlock("new-added-rewrite", 10, "The new coherent replacement paragraph."),
			OldIndex:       -1,
			NewIndex:       0,
			SectionPathNew: []string{"Intro"},
		},
		{
			ID:             "deleted-rewrite-1",
			Status:         diffreview.Deleted,
			Old:            fixtureBlock("old-deleted-rewrite-1", 20, "Old paragraph one."),
			OldIndex:       0,
			NewIndex:       -1,
			SectionPathOld: []string{"Intro"},
		},
		{
			ID:             "deleted-rewrite-2",
			Status:         diffreview.Deleted,
			Old:            fixtureBlock("old-deleted-rewrite-2", 21, "Old paragraph two."),
			OldIndex:       1,
			NewIndex:       -1,
			SectionPathOld: []string{"Intro"},
		},
	}}
	m := New(review, Options{})
	m.Cursor = 1
	m.Focus = PaneOldSource
	m.SourceLineCursor = 1
	oldLineBefore, _ := m.sourceAnchorLines()
	if oldLineBefore != 20 {
		t.Fatalf("old anchor before toggle = %d, want 20", oldLineBefore)
	}

	m = pressKey(t, m, "m")
	if m.DiffRegime != DiffRegimeCoalesced {
		t.Fatalf("diff regime = %s, want coalesced", m.DiffRegime)
	}
	if m.Cursor != 1 {
		t.Fatalf("cursor moved to %d, want same hidden member pair 1", m.Cursor)
	}
	oldLineAfter, _ := m.sourceAnchorLines()
	if oldLineAfter != oldLineBefore {
		t.Fatalf("old anchor after toggle = %d, want %d", oldLineAfter, oldLineBefore)
	}

	m.SourceLineCursor = 2
	oldLineBefore, _ = m.sourceAnchorLines()
	if oldLineBefore != 21 {
		t.Fatalf("old anchor before toggling back = %d, want 21", oldLineBefore)
	}
	m = pressKey(t, m, "m")
	if m.DiffRegime != DiffRegimeSemantic {
		t.Fatalf("diff regime = %s, want semantic", m.DiffRegime)
	}
	if m.Cursor != 2 {
		t.Fatalf("cursor after toggling back = %d, want member pair 2", m.Cursor)
	}
	oldLineAfter, _ = m.sourceAnchorLines()
	if oldLineAfter != oldLineBefore {
		t.Fatalf("old anchor after toggling back = %d, want %d", oldLineAfter, oldLineBefore)
	}
}

func TestToggleReviewedAutoAdvancesChangedAndUnreviewedFilters(t *testing.T) {
	m := New(fixtureReview(), Options{})
	if currentID(m) != "changed" {
		t.Fatalf("default cursor = %s, want changed", currentID(m))
	}

	m = pressKey(t, m, " ")
	if !m.Reviewed["changed"] {
		t.Fatalf("changed pair was not marked reviewed")
	}
	if currentID(m) != "added" {
		t.Fatalf("space under changed filter moved to %s, want added", currentID(m))
	}
	if got := m.Sidecar.ReviewedSet(); !got["changed"] {
		t.Fatalf("sidecar reviewed set was not updated")
	}

	m.Filter = FilterUnreviewed
	m = pressKey(t, m, " ")
	if !m.Reviewed["added"] {
		t.Fatalf("added pair was not marked reviewed")
	}
	if currentID(m) != "deleted" {
		t.Fatalf("space under unreviewed filter moved to %s, want deleted", currentID(m))
	}
}

func TestAnnotationAddEditAndDelete(t *testing.T) {
	m := New(fixtureReview(), Options{})

	m = pressKey(t, m, "a")
	if m.Popup == nil || m.Popup.PairID != "changed" {
		t.Fatalf("expected annotation popup for changed pair")
	}
	m = pressRunes(t, m, "first note")
	m = pressSpecial(t, m, tea.KeyEnter)
	if got := m.Annotations["changed"]; got != "first note" {
		t.Fatalf("annotation note = %q, want first note", got)
	}
	if notes := m.Sidecar.AnnotationNotes(); notes["changed"] != "first note" {
		t.Fatalf("sidecar annotation was not updated: %#v", notes)
	}

	m = pressSpecial(t, m, tea.KeyCtrlA)
	m.Popup.TA.SetValue("updated note")
	m = pressSpecial(t, m, tea.KeyEnter)
	if got := m.Annotations["changed"]; got != "updated note" {
		t.Fatalf("annotation note = %q, want updated note", got)
	}

	m = pressKey(t, m, "d")
	if m.Pending == nil {
		t.Fatalf("expected pending delete confirmation")
	}
	m = pressKey(t, m, "y")
	if _, ok := m.Annotations["changed"]; ok {
		t.Fatalf("annotation was not removed from map")
	}
	if notes := m.Sidecar.AnnotationNotes(); notes["changed"] != "" {
		t.Fatalf("annotation was not removed from sidecar: %#v", notes)
	}
}

func TestLayoutToggleAndPaneResize(t *testing.T) {
	m := New(fixtureReview(), Options{})
	m.Width = 140
	m.Height = 30
	m.Layout = LayoutNoPDF

	m = pressKey(t, m, "\\")
	if m.Layout != LayoutThreeCol {
		t.Fatalf("first \\ should show side PDF layout")
	}
	view := m.View()
	if !strings.Contains(view, "Old source") || !strings.Contains(view, "New source") || !strings.Contains(view, "PDF") {
		t.Fatalf("side-PDF view should retain old/new panes and PDF pane:\n%s", view)
	}

	oldSplit := m.SourceSplitFrac
	m = pressKey(t, m, "right") // outline -> old
	if m.Focus != PaneOldSource {
		t.Fatalf("focus after right = %s, want old", m.Focus)
	}
	m = pressKey(t, m, "left") // old -> outline
	if m.Focus != PaneOutline {
		t.Fatalf("focus after left = %s, want outline", m.Focus)
	}
	m = pressKey(t, m, "l") // outline -> old
	if m.Focus != PaneOldSource {
		t.Fatalf("focus after l = %s, want old", m.Focus)
	}
	m = pressKey(t, m, ">")
	if m.SourceSplitFrac <= oldSplit {
		t.Fatalf("> on old source should grow old side split: before %.2f after %.2f", oldSplit, m.SourceSplitFrac)
	}

	m = pressKey(t, m, "\\")
	if m.Layout != LayoutStacked {
		t.Fatalf("second \\ should switch to stacked layout")
	}
	view = m.View()
	if !strings.Contains(view, "Old source") || !strings.Contains(view, "New source") || !strings.Contains(view, "PDF") {
		t.Fatalf("stacked view should retain old/new top panes and PDF pane:\n%s", view)
	}

	m.Focus = PanePDF
	oldTop := m.StackedTopFrac
	m = pressKey(t, m, ">")
	if m.StackedTopFrac >= oldTop {
		t.Fatalf("> on stacked PDF should grow bottom PDF by shrinking top: before %.2f after %.2f", oldTop, m.StackedTopFrac)
	}

	m = pressKey(t, m, "\\")
	if m.Layout != LayoutSourcesPDF {
		t.Fatalf("third \\ should drop the outline (sources + PDF), got %v", m.Layout)
	}
	view = m.View()
	if strings.Contains(view, "Outline") || !strings.Contains(view, "Old source") || !strings.Contains(view, "PDF") {
		t.Fatalf("sources+PDF view should omit the outline and keep sources + PDF:\n%s", view)
	}

	m = pressKey(t, m, "\\")
	if m.Layout != LayoutNewPDF {
		t.Fatalf("fourth \\ should switch to new + PDF, got %v", m.Layout)
	}
	if m.Focus == PaneOldSource || m.Focus == PaneOutline {
		t.Fatalf("new+PDF layout should move focus off hidden panes, got %v", m.Focus)
	}
	view = m.View()
	if strings.Contains(view, "Old source") || !strings.Contains(view, "New source") || !strings.Contains(view, "PDF") {
		t.Fatalf("new+PDF view should show only new source and PDF:\n%s", view)
	}

	m = pressKey(t, m, "\\")
	if m.Layout != LayoutNoPDF {
		t.Fatalf("fifth \\ should hide PDF, got %v", m.Layout)
	}
	if m.Focus == PanePDF {
		t.Fatalf("hidden PDF layout should move focus off PDF")
	}
	m.Status = ""
	view = m.View()
	if strings.Contains(view, "PDF") || !strings.Contains(view, "Old source") || !strings.Contains(view, "New source") {
		t.Fatalf("hidden-PDF view should keep source panes and omit PDF pane:\n%s", view)
	}
	m = pressKey(t, m, "right")
	if m.Focus == PanePDF {
		t.Fatalf("focus traversal should skip hidden PDF pane")
	}
	m = pressKey(t, m, "\\")
	if m.Layout != LayoutThreeCol {
		t.Fatalf("sixth \\ should return to side-by-side layout")
	}
}

// TestPDFOnlyZoomToggle pins the | zoom: it interrupts any layout, forces
// PDF focus, and a second | (or \) restores the interrupted layout.
func TestPDFOnlyZoomToggle(t *testing.T) {
	m := New(fixtureReview(), Options{})
	m.Width, m.Height = 120, 40
	m.Layout = LayoutStacked
	m.Focus = PaneOldSource

	m = pressKey(t, m, "|")
	if m.Layout != LayoutPDFOnly {
		t.Fatalf("| should zoom to PDF only, got %v", m.Layout)
	}
	if m.Focus != PanePDF {
		t.Fatalf("PDF zoom should focus the PDF pane, got %v", m.Focus)
	}
	view := m.View()
	if strings.Contains(view, "Old source") || strings.Contains(view, "Outline") {
		t.Fatalf("PDF-only view must not render other panes:\n%s", view)
	}

	m = pressKey(t, m, "|")
	if m.Layout != LayoutStacked {
		t.Fatalf("second | should restore the interrupted layout, got %v", m.Layout)
	}
	if m.Focus != PaneOldSource {
		t.Fatalf("second | should restore the interrupted focus, got %v", m.Focus)
	}

	// \ also exits the zoom, back to the interrupted layout and focus.
	m.Focus = PaneNewSource
	m = pressKey(t, m, "|")
	m = pressKey(t, m, "\\")
	if m.Layout != LayoutStacked {
		t.Fatalf("\\ inside PDF zoom should restore the interrupted layout, got %v", m.Layout)
	}
	if m.Focus != PaneNewSource {
		t.Fatalf("\\ inside PDF zoom should restore the interrupted focus, got %v", m.Focus)
	}
}

func TestCopySelectedChunkUsesFocusedSide(t *testing.T) {
	saved := writeDiffClipboard
	var copied string
	writeDiffClipboard = func(text string) error {
		copied = text
		return nil
	}
	t.Cleanup(func() { writeDiffClipboard = saved })

	m := New(fixtureReview(), Options{})
	m.Cursor = pairIndexByID(m.Review, "changed")
	m.Focus = PaneNewSource
	m = pressKey(t, m, "y")
	if copied != "Alpha\nnew beta" {
		t.Fatalf("new-focused y copied %q", copied)
	}
	if !strings.Contains(m.Status, "copied new chunk") {
		t.Fatalf("copy status = %q", m.Status)
	}

	m.Focus = PaneOldSource
	m = pressKey(t, m, "y")
	if copied != "Alpha\nold beta" {
		t.Fatalf("old-focused y copied %q", copied)
	}

	m.Cursor = pairIndexByID(m.Review, "deleted")
	m.Focus = PaneNewSource
	m = pressKey(t, m, "y")
	if copied != "Deleted line one.\nDeleted line two." {
		t.Fatalf("deleted row should fall back to old source, copied %q", copied)
	}
	if !strings.Contains(m.Status, "copied old chunk") {
		t.Fatalf("fallback copy status = %q", m.Status)
	}
}

func TestMouseWheelScrollsPanesUnderPointer(t *testing.T) {
	m := New(fixtureReview(), Options{})
	m.Width = 140
	m.Height = 30
	m.Layout = LayoutThreeCol

	m = mouseWheel(t, m, 1, 3, tea.MouseButtonWheelDown)
	if currentID(m) != "added" {
		t.Fatalf("outline wheel down moved to %s, want added", currentID(m))
	}
	if m.Focus != PaneOutline {
		t.Fatalf("outline wheel focus = %s", m.Focus)
	}

	m.Cursor = pairIndexByID(m.Review, "changed")
	m.SourceLineCursor = 1
	m = mouseWheel(t, m, 45, 3, tea.MouseButtonWheelDown)
	if currentID(m) != "changed" {
		t.Fatalf("source wheel should stay on current pair, got %s", currentID(m))
	}
	if m.Focus != PaneOldSource {
		t.Fatalf("source wheel focus = %s, want old source", m.Focus)
	}
	if m.SourceLineCursor != 2 {
		t.Fatalf("source wheel should scroll source line to 2, got %d", m.SourceLineCursor)
	}
	m = mouseWheel(t, m, 45, 3, tea.MouseButtonWheelDown)
	if currentID(m) != "changed" || m.SourceLineCursor != 2 {
		t.Fatalf("source wheel at block end must clamp, not jump changes; pair=%s line=%d", currentID(m), m.SourceLineCursor)
	}
	m = mouseWheel(t, m, 45, 3, tea.MouseButtonWheelUp)
	m = mouseWheel(t, m, 45, 3, tea.MouseButtonWheelUp)
	if currentID(m) != "changed" || m.SourceLineCursor != 1 {
		t.Fatalf("source wheel at block start must clamp, not jump changes; pair=%s line=%d", currentID(m), m.SourceLineCursor)
	}

	m = mouseWheel(t, m, 120, 3, tea.MouseButtonWheelDown)
	if currentID(m) != "added" {
		t.Fatalf("PDF wheel should move pair, got %s", currentID(m))
	}
}

func TestMouseWheelAtEdgeArmsDropFilterAndSkipsPDF(t *testing.T) {
	m := New(fixtureReview(), Options{})
	m.Width = 140
	m.Height = 30
	m.Layout = LayoutThreeCol
	m.PDF = &pdf.Doc{}
	m.Synctex = &synctex.Index{}
	m.KittyAvailable = true
	m.moveToLast()
	msg := tea.MouseMsg{X: 120, Y: 3, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown}

	next, cmd := m.Update(msg)
	if cmd != nil {
		t.Fatalf("edge mouse wheel returned PDF command")
	}
	nm, ok := next.(Model)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}
	if nm.pdfGen != 0 {
		t.Fatalf("edge mouse wheel bumped pdfGen to %d", nm.pdfGen)
	}
	if !nm.ShouldDropMouseWheel(msg) {
		t.Fatalf("repeated edge wheel should be dropped by the program filter")
	}
	up := tea.MouseMsg{X: 120, Y: 3, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp}
	if nm.ShouldDropMouseWheel(up) {
		t.Fatalf("opposite-direction wheel must not be dropped")
	}
}

func TestMouseWheelMovementDoesNotArmDropFilter(t *testing.T) {
	m := New(fixtureReview(), Options{})
	m.Width = 140
	m.Height = 30
	m.Layout = LayoutThreeCol
	msg := tea.MouseMsg{X: 120, Y: 3, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown}

	next, _ := m.Update(msg)
	nm, ok := next.(Model)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}
	if currentID(nm) != "added" {
		t.Fatalf("wheel should move to added pair, got %s", currentID(nm))
	}
	if nm.ShouldDropMouseWheel(msg) {
		t.Fatalf("successful wheel motion should not arm the drop filter")
	}
}

func TestFocusedSourcePaneScrollsWithinChunk(t *testing.T) {
	m := New(fixtureReview(), Options{})
	m.Cursor = pairIndexByID(m.Review, "changed")
	m.Focus = PaneNewSource
	m = pressKey(t, m, "j")
	if got := m.SourceLineCursor; got != 2 {
		t.Fatalf("j with source focus should scroll source line, got cursor %d", got)
	}
	if currentID(m) != "changed" {
		t.Fatalf("source scroll should not move semantic pair, got %s", currentID(m))
	}
	m = pressKey(t, m, "j")
	if currentID(m) != "changed" || m.SourceLineCursor != 2 {
		t.Fatalf("source j at block end must clamp, not jump changes; pair=%s line=%d", currentID(m), m.SourceLineCursor)
	}
	m = pressKey(t, m, "k")
	m = pressKey(t, m, "k")
	if currentID(m) != "changed" || m.SourceLineCursor != 1 {
		t.Fatalf("source k at block start must clamp, not jump changes; pair=%s line=%d", currentID(m), m.SourceLineCursor)
	}
}

func TestSourceLineSelectionDrivesInlineEditLine(t *testing.T) {
	m := New(fixtureReview(), Options{AllowModifications: true, RequestedAllowMods: true})
	m.Cursor = pairIndexByID(m.Review, "changed")
	if got := m.currentNewLine(); got != 4 {
		t.Fatalf("initial selected diff line = %d, want 4", got)
	}
	m = pressKey(t, m, "[")
	if got := m.currentNewLine(); got != 3 {
		t.Fatalf("after [ selected line = %d, want 3", got)
	}
	if !strings.Contains(m.Status, "3") {
		t.Fatalf("source-line status = %q, want line 3", m.Status)
	}
	m = pressKey(t, m, "j")
	if got := m.SourceLineCursor; got != 1 {
		t.Fatalf("pair navigation should land on next chunk cursor 1, got %d", got)
	}
	if currentID(m) != "added" {
		t.Fatalf("pair navigation should advance to added, got %s", currentID(m))
	}
}

func mouseWheel(t *testing.T, m Model, x, y int, button tea.MouseButton) Model {
	t.Helper()
	next, _ := m.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: button})
	nm, ok := next.(Model)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}
	return nm
}

func pressKey(t *testing.T, m Model, key string) Model {
	t.Helper()
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	if len([]rune(key)) == 1 && key[0] < 32 {
		msg = tea.KeyMsg{Type: tea.KeyType(key[0])}
	}
	next, _ := m.Update(msg)
	nm, ok := next.(Model)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}
	return nm
}

func pressRunes(t *testing.T, m Model, value string) Model {
	t.Helper()
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)})
	nm, ok := next.(Model)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}
	return nm
}

func pressSpecial(t *testing.T, m Model, typ tea.KeyType) Model {
	t.Helper()
	next, _ := m.Update(tea.KeyMsg{Type: typ})
	nm, ok := next.(Model)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}
	return nm
}

func currentID(m Model) string {
	pair := m.CurrentPair()
	if pair == nil {
		return ""
	}
	return pair.ID
}

func fixtureManyChangedReview(n int) *diffreview.Review {
	pairs := make([]diffreview.Pair, n)
	for i := range pairs {
		id := fmt.Sprintf("p%02d", i)
		pairs[i] = diffreview.Pair{
			ID:       id,
			Status:   diffreview.Changed,
			Old:      fixtureBlock("old-"+id, i+1, fmt.Sprintf("old %d", i)),
			New:      fixtureBlock("new-"+id, i+1, fmt.Sprintf("new %d", i)),
			OldIndex: i,
			NewIndex: i,
		}
	}
	return &diffreview.Review{Pairs: pairs}
}
