package diffui

import (
	"strings"
	"testing"

	"github.com/lenis2000/mrevdiff/pkg/diffreview"
	"github.com/lenis2000/mrevdiff/pkg/parser"
)

func TestRenderPairSourceSeparatesMultipleChangeHunks(t *testing.T) {
	pair := &diffreview.Pair{
		Status: diffreview.Changed,
		Old:    fixtureBlock("old-hunks", 1, "first old\nunchanged middle\nsecond old"),
		New:    fixtureBlock("new-hunks", 1, "first new\nunchanged middle\nsecond new"),
	}
	view := RenderPairSourceSide(pair, false, 60, 8)
	if !strings.Contains(view, "next change") {
		t.Fatalf("multiple changed groups should be visually separated:\n%s", view)
	}
}

func TestRenderPairSourceWholeBlockTokenDiffSurvivesRewrap(t *testing.T) {
	pair := &diffreview.Pair{
		Status: diffreview.Changed,
		Old:    fixtureBlock("old-rewrap", 1, "We study domino tilings of the Aztec diamond with one-periodic weights."),
		New:    fixtureBlock("new-rewrap", 1, "We study domino tilings of the Aztec diamond\nwith one-periodic weights."),
	}
	view := RenderPairSourceHighlighted(pair, 120, 6, 0, 0)
	if strings.Contains(view, "\x1b[") {
		t.Fatalf("pure rewrap should not produce content highlights:\n%q", view)
	}
}

func TestRenderPairSourceTransparentWrapperKeepsInnerWordUnchanged(t *testing.T) {
	pair := &diffreview.Pair{
		Status: diffreview.Changed,
		Old:    fixtureBlock("old-wrap", 1, "hybrid behavior"),
		New:    fixtureBlock("new-wrap", 1, "\\emph{hybrid} behavior"),
	}
	view := RenderPairSourceSideHighlighted(pair, false, 80, 6, 0, 0)
	if !strings.Contains(view, "hybrid") || !strings.Contains(view, "\\emph") {
		t.Fatalf("wrapper rendering missing expected text:\n%q", view)
	}
}

func TestRenderPairSourceShowsAnchorCursor(t *testing.T) {
	pair := &diffreview.Pair{
		Status: diffreview.Changed,
		Old:    fixtureBlock("old-cursor", 1, "first old\nunchanged middle\nsecond old"),
		New:    fixtureBlock("new-cursor", 1, "first new\nunchanged middle\nsecond new"),
	}
	view := RenderPairSourceSideHighlighted(pair, false, 80, 8, 0, 3)
	if !strings.Contains(view, "~>   3") {
		t.Fatalf("anchored changed line should show cursor in source pane:\n%q", view)
	}
}

func TestRenderPairSourceSideCompactsOppositeOnlyRuns(t *testing.T) {
	pair := &diffreview.Pair{
		Status: diffreview.Changed,
		Old:    fixtureBlock("old-insert", 10, "before\nafter"),
		New:    fixtureBlock("new-insert", 10, "before\ninsert one\ninsert two\nafter"),
	}
	oldSide := RenderPairSourceSide(pair, true, 80, 8)
	if !strings.Contains(oldSide, "before") || !strings.Contains(oldSide, "after") {
		t.Fatalf("old side lost context:\n%s", oldSide)
	}
	if !strings.Contains(oldSide, "(2 lines added in new)") {
		t.Fatalf("old side should summarize inserted run, not show a visual gap:\n%s", oldSide)
	}
	if strings.Contains(oldSide, "insert one") || strings.Contains(oldSide, "insert two") {
		t.Fatalf("old side should not render new-only lines:\n%s", oldSide)
	}
}

func TestRenderPairSourceHighlightsChangedLines(t *testing.T) {
	review := fixtureReview()
	highlighted := RenderPairSourceSideHighlighted(pairByID(t, review, "changed"), false, 80, 8, 0, 0)
	if !strings.Contains(highlighted, "new beta") || !strings.Contains(highlighted, "~") {
		t.Fatalf("highlighted source should preserve changed text and marker:\n%q", highlighted)
	}
}

func TestRenderPairSourceAnchorScrollsWithinLongBlock(t *testing.T) {
	pair := &diffreview.Pair{
		Status: diffreview.Changed,
		Old:    fixtureBlock("old-many", 10, "old one\nold two\nold three\nold four\nold five"),
		New:    fixtureBlock("new-many", 10, "new one\nnew two\nnew three\nnew four\nnew five"),
	}
	view := RenderPairSourceSideAt(pair, false, 40, 2, 0, 14)
	if !strings.Contains(view, "new five") || strings.Contains(view, "new one") {
		t.Fatalf("anchored rendering should scroll toward requested line:\n%s", view)
	}
}

func TestRenderPairSourceWrapsLongLines(t *testing.T) {
	pair := &diffreview.Pair{
		Status: diffreview.Changed,
		Old:    fixtureBlock("old-long", 1, "old prefix alpha beta gamma delta epsilon tail"),
		New:    fixtureBlock("new-long", 1, "new prefix alpha beta gamma delta epsilon tail"),
	}

	side := RenderPairSourceSide(pair, false, 24, 6)
	if !strings.Contains(side, "tail") {
		t.Fatalf("wrapped side source should expose the tail of a long line:\n%s", side)
	}
	for _, line := range strings.Split(side, "\n") {
		if len([]rune(line)) > 24 {
			t.Fatalf("wrapped line exceeds pane width: %q", line)
		}
	}

	combined := RenderPairSource(pair, 52, 8)
	if !strings.Contains(combined, "tail") {
		t.Fatalf("wrapped combined source should expose the tail of a long line:\n%s", combined)
	}
}

func TestWrapSourceCellStyledExpandsTabsBeforeWrapping(t *testing.T) {
	const source = "\tWe prove that in this setting, colored interlacing triangles are in"
	const width = 53
	lines := wrapSourceCellStyled(" ", 115, source,
		[]sourcePart{{Text: source, Kind: sourcePartEqual}}, width, true, false)

	for _, line := range lines {
		if strings.ContainsRune(line, '\t') {
			t.Fatalf("wrapped source row contains a terminal-dependent tab: %q", line)
		}
		if got := ansiVisibleWidth(line); got != width {
			t.Fatalf("wrapped source row width = %d, want %d: %q", got, width, line)
		}
	}

	var content strings.Builder
	const prefixWidth = 7
	for _, line := range lines {
		runes := []rune(line)
		content.WriteString(string(runes[prefixWidth:]))
	}
	got := strings.TrimRight(content.String(), " ")
	want := strings.ReplaceAll(source, "\t", "    ")
	if got != want {
		t.Fatalf("wrapped source did not preserve its text:\n got %q\nwant %q", got, want)
	}
}

func TestRenderPairSourceForAddedDeletedChangedAndFormatOnly(t *testing.T) {
	review := fixtureReview()

	added := RenderPairSource(pairByID(t, review, "added"), 100, 8)
	if !strings.Contains(added, "(added in new)") || !strings.Contains(added, "+    6 Added line one.") {
		t.Fatalf("added source rendering missing placeholder or added marker:\n%s", added)
	}

	deleted := RenderPairSource(pairByID(t, review, "deleted"), 100, 8)
	if !strings.Contains(deleted, "(deleted from new)") || !strings.Contains(deleted, "-    9 Deleted line one.") {
		t.Fatalf("deleted source rendering missing placeholder or deleted marker:\n%s", deleted)
	}

	changed := RenderPairSource(pairByID(t, review, "changed"), 100, 8)
	if !strings.Contains(changed, "~    4 old beta") || !strings.Contains(changed, "~    4 new beta") {
		t.Fatalf("changed source rendering missing changed line markers:\n%s", changed)
	}

	formatOnly := RenderPairSource(pairByID(t, review, "fmt"), 100, 8)
	if !strings.Contains(formatOnly, "12 A  B") || !strings.Contains(formatOnly, "12 A B") {
		t.Fatalf("format-only source rendering should preserve raw line text:\n%s", formatOnly)
	}
}

func pairByID(t *testing.T, review *diffreview.Review, id string) *diffreview.Pair {
	t.Helper()
	for i := range review.Pairs {
		if review.Pairs[i].ID == id {
			return &review.Pairs[i]
		}
	}
	t.Fatalf("missing pair %q", id)
	return nil
}

func fixtureBlock(id string, startLine int, source string) *parser.Block {
	return &parser.Block{
		ID:        id,
		Kind:      parser.KindParagraph,
		StartLine: startLine,
		EndLine:   startLine + blockLineCount(source) - 1,
		Source:    source,
	}
}
