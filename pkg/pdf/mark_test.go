package pdf

import (
	"bytes"
	"path/filepath"
	"testing"

	"mrevdiff/pkg/synctex"
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

	plain1, err := CropFitted(doc, region, opts)
	if err != nil {
		t.Fatalf("plain crop: %v", err)
	}
	optsMarked := opts
	optsMarked.MarkRegion = true
	marked, err := CropFitted(doc, region, optsMarked)
	if err != nil {
		t.Fatalf("marked crop: %v", err)
	}
	if bytes.Equal(plain1, marked) {
		t.Fatalf("marking the region must change the crop")
	}
	// The marked render must not have painted into the cached page pixmap:
	// a second unmarked crop must be byte-identical to the first.
	plain2, err := CropFitted(doc, region, opts)
	if err != nil {
		t.Fatalf("plain crop 2: %v", err)
	}
	if !bytes.Equal(plain1, plain2) {
		t.Fatalf("marker leaked into the cached page pixmap — unmarked crops differ after a marked render")
	}
}
