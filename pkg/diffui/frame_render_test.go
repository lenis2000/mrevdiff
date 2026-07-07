package diffui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mrevdiff/pkg/parser"
	"mrevdiff/pkg/pdf"
	"mrevdiff/pkg/synctex"
)

// openSampleArtifacts loads the repo-level sample PDF + SyncTeX pair and
// finds a source line range that maps to a real region, so the frame
// pipeline can be exercised end to end.
func openSampleArtifacts(t *testing.T) (*pdf.Doc, *synctex.Index, *parser.Block) {
	t.Helper()
	samplePDF, err := filepath.Abs(filepath.Join("..", "..", "testdata", "sample.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	sampleSyncTeX, err := filepath.Abs(filepath.Join("..", "..", "testdata", "sample.synctex.gz"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := pdf.Open(samplePDF)
	if err != nil {
		t.Fatalf("open sample pdf: %v", err)
	}
	t.Cleanup(func() { _ = doc.Close() })
	idx, err := synctex.Open(sampleSyncTeX)
	if err != nil {
		t.Fatalf("open sample synctex: %v", err)
	}
	for _, file := range idx.Files {
		for line := 1; line < 200; line++ {
			r := idx.RegionForLines(file, line, line+2)
			if r != nil && pdf.HasExtent(*r) {
				return doc, idx, &parser.Block{ID: "sample-block", File: file, StartLine: line, EndLine: line + 2}
			}
		}
	}
	t.Fatalf("no synctex region with extent found in sample fixtures")
	return nil, nil, nil
}

// TestRenderDiffPDFFrameEndToEnd drives the full crop → encode → escape
// pipeline in both transmission modes and checks the cache fills.
func TestRenderDiffPDFFrameEndToEnd(t *testing.T) {
	doc, idx, block := openSampleArtifacts(t)

	t.Run("inline base64", func(t *testing.T) {
		cache := newPDFEscCache("")
		in := diffPDFRenderInputs{
			Block: block, PDF: doc, Index: idx,
			WidthCells: 40, HeightCells: 20,
			CellWidthPx: 9, CellHeightPx: 18,
			Cache: cache, ReloadGen: 1,
		}
		key := diffPDFRenderKey("n", block, 1, 40, 20, 9, 18)
		esc, id, _, status := renderDiffPDFFrame(in, key)
		if status != "" || esc == "" || id == 0 {
			t.Fatalf("render failed: esc=%dB id=%d status=%q", len(esc), id, status)
		}
		if strings.HasPrefix(esc, pdf.KittyDeleteAll) {
			t.Fatalf("frame must not start with delete-all")
		}
		esc2, id2, _, _ := renderDiffPDFFrame(in, key)
		if esc2 != esc || id2 != id {
			t.Fatalf("second call must be a cache hit with the identical frame")
		}
	})

	t.Run("t=f file transfer", func(t *testing.T) {
		dir := t.TempDir()
		cache := newPDFEscCache(dir)
		in := diffPDFRenderInputs{
			Block: block, PDF: doc, Index: idx,
			WidthCells: 40, HeightCells: 20,
			CellWidthPx: 9, CellHeightPx: 18,
			Cache: cache, ReloadGen: 1,
		}
		key := diffPDFRenderKey("n", block, 1, 40, 20, 9, 18)
		esc, id, _, status := renderDiffPDFFrame(in, key)
		if status != "" || esc == "" || id == 0 {
			t.Fatalf("render failed: esc=%dB id=%d status=%q", len(esc), id, status)
		}
		if !strings.Contains(esc, "t=f") {
			t.Fatalf("xfer-dir cache must produce a t=f escape")
		}
		if _, err := os.Stat(cache.framePath(key)); err != nil {
			t.Fatalf("transfer PNG must exist while cached: %v", err)
		}
	})

	t.Run("full page", func(t *testing.T) {
		cache := newPDFEscCache("")
		in := diffPDFRenderInputs{
			Block: block, PDF: doc, Index: idx,
			WidthCells: 40, HeightCells: 20,
			CellWidthPx: 9, CellHeightPx: 18,
			Cache: cache, ReloadGen: 1, FullPage: true,
		}
		key := diffPDFRenderKey("nf", block, 1, 40, 20, 9, 18)
		esc, id, _, status := renderDiffPDFFrame(in, key)
		if status != "" || esc == "" || id == 0 {
			t.Fatalf("full-page render failed: esc=%dB id=%d status=%q", len(esc), id, status)
		}
		// A crop of the same block under the same key namespace must produce
		// a different frame, confirming full-page took a distinct path.
		cropCache := newPDFEscCache("")
		cropIn := in
		cropIn.Cache = cropCache
		cropIn.FullPage = false
		cropEsc, _, _, _ := renderDiffPDFFrame(cropIn, diffPDFRenderKey("n", block, 1, 40, 20, 9, 18))
		if cropEsc == esc {
			t.Fatalf("full-page frame must differ from the region crop")
		}
	})
}

// TestFullPageToggle pins the F key: it flips the render mode and the
// pane title reflects it.
func TestFullPageToggle(t *testing.T) {
	m := New(fixtureReview(), Options{})
	m.Width, m.Height = 120, 40
	m.Layout = LayoutThreeCol
	if m.pdfFullPage {
		t.Fatalf("full-page must default off")
	}
	m = pressKey(t, m, "F")
	if !m.pdfFullPage {
		t.Fatalf("F should turn full-page on")
	}
	if !strings.Contains(m.Status, "full page") {
		t.Fatalf("status should announce full page, got %q", m.Status)
	}
	m = pressKey(t, m, "F")
	if m.pdfFullPage {
		t.Fatalf("F should toggle full-page back off")
	}
	if !strings.Contains(m.Status, "region crop") {
		t.Fatalf("status should announce region crop, got %q", m.Status)
	}
}

// TestApplyPDFRenderSwapsImagesByID pins the draw-new-then-delete-old
// ordering that keeps the pane painted through a frame swap.
func TestApplyPDFRenderSwapsImagesByID(t *testing.T) {
	m := Model{pdfGen: 3, lastKittyID: 5}
	next, _ := m.applyPDFRender(diffPDFRenderMsg{Generation: 3, Image: "NEWFRAME", ImageID: 9})
	if !strings.HasPrefix(next.PDFImage, "NEWFRAME") {
		t.Fatalf("new frame must come first, got %q", next.PDFImage)
	}
	if !strings.HasSuffix(next.PDFImage, pdf.KittyDeleteByID(5)) {
		t.Fatalf("previous image must be deleted after the draw, got %q", next.PDFImage)
	}
	if next.lastKittyID != 9 {
		t.Fatalf("lastKittyID must advance to 9, got %d", next.lastKittyID)
	}

	// Stale generation is dropped wholesale.
	stale, _ := next.applyPDFRender(diffPDFRenderMsg{Generation: 2, Image: "OLD", ImageID: 4})
	if stale.PDFImage != next.PDFImage || stale.lastKittyID != 9 {
		t.Fatalf("stale generations must not mutate the frame state")
	}

	// A placeholder result (no image) keeps ids intact for kittyClear.
	empty, _ := next.applyPDFRender(diffPDFRenderMsg{Generation: 3, Image: "", Status: "no region"})
	if empty.lastKittyID != 9 {
		t.Fatalf("empty render must not clobber the last image id")
	}
}
