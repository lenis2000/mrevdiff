package diffui

import (
	"sort"
	"sync"

	"github.com/lenis2000/mrevdiff/pkg/parser"
	"github.com/lenis2000/mrevdiff/pkg/pdf"
	"github.com/lenis2000/mrevdiff/pkg/synctex"
)

// pageLayoutCache memoizes per-page column detection. Without it, every
// crop of a two-column paper spans the full page width, pulling in the
// neighboring column and its figures. The decision is
// computed once per page (per PDF mtime) by taking the median width of
// all SyncTeX-mapped block regions on that page; if the median spans
// less than ~55% of the page, the page is treated as multi-column.
//
// The cache lives on the model because it needs both the parsed doc
// (for block iteration) and the open PDF (for page bounds). Concurrent
// reads happen from PDF render and prefetch goroutines, so the mutex is
// load-bearing.
type pageLayoutCache struct {
	mu      sync.Mutex
	entries map[pageLayoutKey]bool
	// widths holds each doc's per-page region widths, snapshotted on the UI
	// goroutine by SetDocRegions. The render and prefetch goroutines read
	// these instead of walking parser.Block.PDFRegion, which the UI goroutine
	// rewrites wholesale on every PDF reload — reading the blocks directly
	// from a goroutine was a genuine data race on the region pointers.
	widths map[*parser.Document]map[int][]float64
}

type pageLayoutKey struct {
	doc   *parser.Document
	mtime int64
	page  int
}

func newPageLayoutCache() *pageLayoutCache {
	return &pageLayoutCache{
		entries: map[pageLayoutKey]bool{},
		widths:  map[*parser.Document]map[int][]float64{},
	}
}

// SetDocRegions snapshots doc's mapped region widths per page. Call it on the
// UI goroutine immediately after (re)populating Block.PDFRegion.
func (c *pageLayoutCache) SetDocRegions(doc *parser.Document) {
	if c == nil || doc == nil {
		return
	}
	byPage := map[int][]float64{}
	for _, b := range doc.Blocks {
		if b == nil || b.PDFRegion == nil || b.PDFRegion.W <= 0 {
			continue
		}
		byPage[b.PDFRegion.Page] = append(byPage[b.PDFRegion.Page], b.PDFRegion.W)
	}
	c.mu.Lock()
	if c.widths == nil {
		c.widths = map[*parser.Document]map[int][]float64{}
	}
	c.widths[doc] = byPage
	c.entries = map[pageLayoutKey]bool{}
	c.mu.Unlock()
}

// IsMultiColumn returns true when the page's block widths suggest a
// multi-column layout. Per-region width detection trips on a single-
// column paper containing a narrow inline equation; per-page detection
// looks at the page's overall structure instead.
//
// Returns false when there's not enough data (< 3 mapped blocks on the
// page) — single-column is the safe default because the column-crop
// horizontal slicing is destructive.
func (c *pageLayoutCache) IsMultiColumn(d *pdf.Doc, doc *parser.Document, page int) bool {
	if c == nil || d == nil || doc == nil || page < 1 {
		return false
	}
	key := pageLayoutKey{doc: doc, mtime: d.Mtime().UnixNano(), page: page}

	// doc is only ever a map key here — never dereferenced — so this stays off
	// the blocks the UI goroutine is free to rewrite underneath us.
	c.mu.Lock()
	if v, ok := c.entries[key]; ok {
		c.mu.Unlock()
		return v
	}
	widths := append([]float64(nil), c.widths[doc][page]...)
	c.mu.Unlock()

	bounds, err := d.Bounds(page - 1)
	if err != nil {
		return false
	}
	pageW := float64(bounds.Dx())
	if pageW < 1 {
		return false
	}

	isMulti := false
	if len(widths) >= 3 {
		sort.Float64s(widths)
		median := widths[len(widths)/2]
		// Median region width spanning under ~55% of the page reads as a
		// column; the 1.1 factor keeps single-column papers with generous
		// margins from flipping.
		isMulti = median*2 < pageW*1.1
	}

	c.mu.Lock()
	c.entries[key] = isMulti
	c.mu.Unlock()
	return isMulti
}

// Invalidate clears the cache; called on PDF reload so a rebuilt PDF
// re-detects layout (the mtime key would already miss, but explicit
// invalidation keeps the map from accumulating stale mtimes).
func (c *pageLayoutCache) Invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.entries = map[pageLayoutKey]bool{}
	c.mu.Unlock()
}

// noRegionScanLines bounds the outward fallback scan when a block's own
// line range has no SyncTeX records.
const noRegionScanLines = 25

// regionForBlockLines resolves the PDF region for a source line range,
// falling back to the nearest mapped line when the range itself has no
// SyncTeX records. Material stored in boxes and emitted elsewhere (PNAS
// \significancestatement, some float contents) often gets no line records
// of its own; anchoring to the nearest surviving neighbor line beats the
// dead "[no region]" placeholder.
func regionForBlockLines(idx *synctex.Index, file string, start, end int) *synctex.Region {
	if idx == nil {
		return nil
	}
	if r := idx.RegionForLines(file, start, end); r != nil && pdf.HasExtent(*r) {
		return r
	}
	for d := 1; d <= noRegionScanLines; d++ {
		if before := start - d; before >= 1 {
			if r := idx.RegionForLines(file, before, before); r != nil && pdf.HasExtent(*r) {
				return r
			}
		}
		if r := idx.RegionForLines(file, end+d, end+d); r != nil && pdf.HasExtent(*r) {
			return r
		}
	}
	return nil
}
