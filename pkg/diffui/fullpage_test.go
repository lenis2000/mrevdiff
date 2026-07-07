package diffui

import (
	"strings"
	"testing"
)

func TestStepPage(t *testing.T) {
	cases := []struct{ cur, delta, total, want int }{
		{1, 1, 5, 2},  // forward
		{5, 1, 5, 5},  // clamp at top
		{1, -1, 5, 1}, // clamp at bottom
		{3, -1, 5, 2}, // backward
		{0, 1, 5, 2},  // uninitialised -> page 1 then +1
		{0, -1, 5, 1}, // uninitialised -> page 1, clamped
		{3, 1, 0, 4},  // open upper bound (total unknown)
		{7, 1, 5, 5},  // stale index above total clamps down
	}
	for _, c := range cases {
		if got := stepPage(c.cur, c.delta, c.total); got != c.want {
			t.Fatalf("stepPage(%d,%d,%d) = %d, want %d", c.cur, c.delta, c.total, got, c.want)
		}
	}
}

// TestFullPageArrowsFlipPages pins the routing: in full-page mode with the
// PDF pane focused, arrows flip pages; a focused source pane or non-full-
// page mode leaves the arrows to their normal focus/nav roles.
func TestFullPageArrowsFlipPages(t *testing.T) {
	doc, _, _ := openSampleArtifacts(t) // sample.pdf is a single page

	base := New(fixtureReview(), Options{})
	base.Width, base.Height = 120, 40
	base.PDF = doc

	t.Run("full-page + PDF focus flips", func(t *testing.T) {
		m := base
		m.pdfFullPage = true
		m.Focus = PanePDF
		m = pressKey(t, m, "right")
		if !strings.Contains(m.Status, "PDF page 1/1") {
			t.Fatalf("arrow should flip pages, got status %q", m.Status)
		}
		m = pressKey(t, m, "left")
		if !strings.Contains(m.Status, "PDF page 1/1") {
			t.Fatalf("left arrow should stay clamped at page 1, got %q", m.Status)
		}
	})

	t.Run("source pane focus does not flip", func(t *testing.T) {
		m := base
		m.pdfFullPage = true
		m.Focus = PaneNewSource
		m = pressKey(t, m, "right")
		if strings.Contains(m.Status, "PDF page") {
			t.Fatalf("arrow with a source pane focused must not flip pages, got %q", m.Status)
		}
	})

	t.Run("region-crop mode does not flip", func(t *testing.T) {
		m := base
		m.pdfFullPage = false
		m.Focus = PanePDF
		m = pressKey(t, m, "right")
		if strings.Contains(m.Status, "PDF page") {
			t.Fatalf("arrow outside full-page must not flip pages, got %q", m.Status)
		}
	})
}

func TestFlipPDFPageNoDoc(t *testing.T) {
	m := New(fixtureReview(), Options{})
	m.pdfFullPage = true
	m.Focus = PanePDF // m.PDF is nil
	m = pressKey(t, m, "left")
	if !strings.Contains(m.Status, "no PDF loaded") {
		t.Fatalf("flip with no PDF should report it, got %q", m.Status)
	}
}

// TestFullPageRespectsPageOverride pins that the render honours an explicit
// page and that the cache key incorporates it (so flipped pages of the same
// block never collide).
func TestFullPageRespectsPageOverride(t *testing.T) {
	doc, idx, block := openSampleArtifacts(t)
	cache := newPDFEscCache("")
	in := diffPDFRenderInputs{
		Block: block, PDF: doc, Index: idx,
		WidthCells: 40, HeightCells: 20, CellWidthPx: 9, CellHeightPx: 18,
		Cache: cache, ReloadGen: 1, FullPage: true, PageOverride: 1,
	}
	key := diffPDFRenderKey("nf", block, 1, 40, 20, 9, 18, 1)
	esc, _, _, status := renderDiffPDFFrame(in, key)
	if status != "" || esc == "" {
		t.Fatalf("full-page render with PageOverride=1 should succeed, status=%q esc=%dB", status, len(esc))
	}
	if diffPDFRenderKey("nf", block, 1, 40, 20, 9, 18, 1) == diffPDFRenderKey("nf", block, 1, 40, 20, 9, 18, 2) {
		t.Fatalf("full-page keys must differ by page")
	}
	if diffPDFRenderKey("n", block, 1, 40, 20, 9, 18, 0) == diffPDFRenderKey("n", block, 1, 40, 20, 9, 18, 1) {
		t.Fatalf("page 0 and page 1 keys must differ")
	}
}

// TestFullPageEntersPDFFocus pins that toggling full-page on focuses the
// PDF pane (when visible) so the arrows flip pages immediately.
func TestFullPageEntersPDFFocus(t *testing.T) {
	m := New(fixtureReview(), Options{})
	m.Width, m.Height = 120, 40
	m.Layout = LayoutThreeCol
	m.Focus = PaneOutline
	m = pressKey(t, m, "F")
	if m.Focus != PanePDF {
		t.Fatalf("F should focus the PDF pane so arrows flip pages, got %v", m.Focus)
	}
	// Turning it back off is fine; focus need not change back.
	m = pressKey(t, m, "F")
	if m.pdfFullPage {
		t.Fatalf("second F should leave full-page mode")
	}
}
