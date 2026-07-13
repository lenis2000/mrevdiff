package pdf

import (
	"bytes"
	"image/png"
	"path/filepath"
	"testing"

	"github.com/lenis2000/mrevdiff/pkg/synctex"
)

func openSamplePDF(t *testing.T) (*Doc, synctex.Region) {
	t.Helper()
	samplePDF, err := filepath.Abs(filepath.Join("..", "..", "testdata", "sample.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := Open(samplePDF)
	if err != nil {
		t.Fatalf("open sample pdf: %v", err)
	}
	t.Cleanup(func() { _ = doc.Close() })
	return doc, synctex.Region{Page: 1, X: 100, Y: 200, W: 300, H: 60}
}

// TestCropFittedMarkRegion pins the marker contract: marking changes the
// output, and — critically — never scribbles on the LRU-cached page
// pixmap that unmarked crops alias.
func TestCropFittedMarkRegion(t *testing.T) {
	doc, region := openSamplePDF(t)
	opts := FitOptions{PaneWidthPx: 600, PaneHeightPx: 400}

	plain1, _, _, err := CropFitted(doc, region, opts)
	if err != nil {
		t.Fatalf("plain crop: %v", err)
	}
	optsMarked := opts
	optsMarked.MarkRegion = true
	marked, _, _, err := CropFitted(doc, region, optsMarked)
	if err != nil {
		t.Fatalf("marked crop: %v", err)
	}
	if bytes.Equal(plain1, marked) {
		t.Fatalf("marking the region must change the crop")
	}
	// The marked render must not have painted into the cached page pixmap:
	// a second unmarked crop must be byte-identical to the first.
	plain2, _, _, err := CropFitted(doc, region, opts)
	if err != nil {
		t.Fatalf("plain crop 2: %v", err)
	}
	if !bytes.Equal(plain1, plain2) {
		t.Fatalf("marker leaked into the cached page pixmap — unmarked crops differ after a marked render")
	}
}

// TestRenderPageFitted pins the full-page path: it renders a valid PNG,
// the marker changes the output, and marking never mutates the cached
// page pixmap.
func TestRenderPageFitted(t *testing.T) {
	doc, region := openSamplePDF(t)
	opts := FitOptions{PaneWidthPx: 600, PaneHeightPx: 800}

	unmarked, _, _, err := RenderPageFitted(doc, region.Page, region, opts)
	if err != nil {
		t.Fatalf("unmarked full page: %v", err)
	}
	if _, decErr := png.Decode(bytes.NewReader(unmarked)); decErr != nil {
		t.Fatalf("full-page output is not a valid PNG: %v", decErr)
	}

	markedOpts := opts
	markedOpts.MarkRegion = true
	marked, _, _, err := RenderPageFitted(doc, region.Page, region, markedOpts)
	if err != nil {
		t.Fatalf("marked full page: %v", err)
	}
	if bytes.Equal(unmarked, marked) {
		t.Fatalf("marking the region must change the full-page render")
	}

	// A full page is wider/taller than a region crop of the same page, so
	// the two render paths must differ.
	crop, _, _, err := CropFitted(doc, region, FitOptions{PaneWidthPx: 600, PaneHeightPx: 800, MarkRegion: true})
	if err != nil {
		t.Fatalf("region crop: %v", err)
	}
	if bytes.Equal(crop, marked) {
		t.Fatalf("full-page render must differ from the region crop")
	}

	// Marking must not have scribbled the cached page pixmap.
	unmarked2, _, _, err := RenderPageFitted(doc, region.Page, region, opts)
	if err != nil {
		t.Fatalf("unmarked full page 2: %v", err)
	}
	if !bytes.Equal(unmarked, unmarked2) {
		t.Fatalf("full-page marker leaked into the cached page pixmap")
	}
}

func TestRenderPageFittedRejectsBadInput(t *testing.T) {
	doc, region := openSamplePDF(t)
	if _, _, _, err := RenderPageFitted(nil, 1, region, FitOptions{}); err == nil {
		t.Fatalf("nil doc must error")
	}
	if _, _, _, err := RenderPageFitted(doc, 0, region, FitOptions{}); err == nil {
		t.Fatalf("non-positive page must error")
	}
}
