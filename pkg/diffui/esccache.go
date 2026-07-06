package diffui

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sync"

	"mrevdiff/pkg/parser"
	"mrevdiff/pkg/pdf"
)

// pdfEscCacheMax bounds the rendered-frame cache. Each entry is either a
// full base64 escape (inline mode, up to a few MB) or a ~200-byte t=f
// escape plus a PNG on disk; 48 covers a long review's worth of block
// revisits without unbounded growth.
const pdfEscCacheMax = 48

// pdfEscEntry is one cached PDF-pane frame: the ready-to-paint kitty
// escape and the image id it transmits under.
type pdfEscEntry struct {
	image string
	id    uint32
}

// pdfEscCache memoises rendered PDF-pane frames keyed by block + geometry +
// reload generation, so revisiting a block (or arriving via prefetch) skips
// the crop + PNG encode + escape build entirely. Shared by the render tick
// goroutine and the neighbor-prefetch goroutines, hence the mutex. It also
// owns the t=f transfer files: a frame's PNG lives exactly as long as its
// cache entry.
type pdfEscCache struct {
	mu       sync.Mutex
	entries  map[string]pdfEscEntry
	order    []string // FIFO eviction order
	inflight map[string]bool
	xferDir  string // non-empty → t=f file transmission
}

func newPDFEscCache(xferDir string) *pdfEscCache {
	return &pdfEscCache{
		entries:  make(map[string]pdfEscEntry),
		inflight: make(map[string]bool),
		xferDir:  xferDir,
	}
}

func (c *pdfEscCache) get(key string) (pdfEscEntry, bool) {
	if c == nil {
		return pdfEscEntry{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	return e, ok
}

func (c *pdfEscCache) put(key string, e pdfEscEntry) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[key]; !exists {
		c.order = append(c.order, key)
	}
	c.entries[key] = e
	for len(c.order) > pdfEscCacheMax {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
		if c.xferDir != "" {
			_ = os.Remove(c.framePath(oldest))
		}
	}
}

// tryClaim marks key as being rendered; returns false when another
// goroutine already owns it (or the result is already cached).
func (c *pdfEscCache) tryClaim(key string) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, done := c.entries[key]; done {
		return false
	}
	if c.inflight[key] {
		return false
	}
	c.inflight[key] = true
	return true
}

func (c *pdfEscCache) release(key string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.inflight, key)
}

// framePath names the t=f PNG for a cache key. Content-addressed by the
// key hash so distinct frames never overwrite each other while cached.
func (c *pdfEscCache) framePath(key string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return filepath.Join(c.xferDir, fmt.Sprintf("crop-%016x.png", h.Sum64()))
}

// clear drops every entry and removes their transfer files. Called on PDF
// reload: the new document invalidates every cached frame wholesale.
func (c *pdfEscCache) clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.xferDir != "" {
		for key := range c.entries {
			_ = os.Remove(c.framePath(key))
		}
	}
	c.entries = make(map[string]pdfEscEntry)
	c.order = nil
}

// diffPDFRenderKey builds the cache key for one frame. The reload
// generation invalidates everything when the PDF is rebuilt; geometry and
// cell pixel size cover pane resizes and monitor/font changes.
func diffPDFRenderKey(block *parser.Block, reloadGen, wCells, hCells int, cellW, cellH float64) string {
	id := ""
	if block != nil {
		id = block.ID
	}
	return fmt.Sprintf("%s|g%d|%dx%d|%.2fx%.2f", id, reloadGen, wCells, hCells, cellW, cellH)
}

// renderDiffPDFFrame renders (or fetches from cache) the frame for
// in.Block and returns the escape plus its image id. Safe for concurrent
// use — this is the shared path for the on-demand render tick and the
// neighbor prefetch goroutines.
func renderDiffPDFFrame(in diffPDFRenderInputs, key string) (string, uint32, string) {
	if e, ok := in.Cache.get(key); ok {
		return e.image, e.id, ""
	}
	if in.Block == nil || in.Block.StartLine < 1 {
		return "", 0, pdf.NoRegionPlaceholder
	}
	file := in.Block.File
	if file == "" && in.Doc != nil {
		file = in.Doc.File
	}
	region := in.Index.RegionForLines(file, in.Block.StartLine, in.Block.EndLine)
	if region == nil || !pdf.HasExtent(*region) {
		return "", 0, pdf.NoRegionPlaceholder
	}
	paneWPx := int(float64(in.WidthCells) * in.CellWidthPx)
	paneHPx := int(float64(in.HeightCells) * in.CellHeightPx)
	png, err := pdf.CropFitted(in.PDF, *region, pdf.FitOptions{
		PaneWidthPx:  paneWPx,
		PaneHeightPx: paneHPx,
	})
	if err != nil {
		return "", 0, fmt.Sprintf("pdf: %v", err)
	}
	id := pdf.NextKittyImageID()
	transferPath := ""
	if in.Cache != nil && in.Cache.xferDir != "" {
		transferPath = in.Cache.framePath(key)
	}
	esc, err := pdf.RenderKittyFrame(png, in.WidthCells, in.HeightCells, id, transferPath)
	if err != nil {
		return "", 0, fmt.Sprintf("pdf: %v", err)
	}
	in.Cache.put(key, pdfEscEntry{image: esc, id: id})
	return esc, id, ""
}

// warmNeighborFrames pre-renders the frames for the blocks adjacent to the
// current pair so j/k navigation hits the cache instead of the crop
// pipeline. Runs on its own goroutine; go-fitz serialises page renders
// internally, so concurrent warms are safe (merely queued). A panic in a
// warm (e.g. a document closed mid-reload) must never take the TUI down.
func warmNeighborFrames(in diffPDFRenderInputs, neighbors []*parser.Block) {
	defer func() { _ = recover() }()
	for _, b := range neighbors {
		if b == nil {
			continue
		}
		nin := in
		nin.Block = b
		key := diffPDFRenderKey(b, in.ReloadGen, in.WidthCells, in.HeightCells, in.CellWidthPx, in.CellHeightPx)
		if !nin.Cache.tryClaim(key) {
			continue
		}
		renderDiffPDFFrame(nin, key)
		nin.Cache.release(key)
	}
}
