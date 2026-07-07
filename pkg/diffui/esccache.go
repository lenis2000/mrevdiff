package diffui

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/lenis2000/mrevdiff/pkg/parser"
	"github.com/lenis2000/mrevdiff/pkg/pdf"
)

// pdfEscCacheMax bounds the rendered-frame cache by entry count, and
// pdfEscCacheMaxBytes by cumulative escape size. In t=f mode entries are
// ~200-byte path escapes and only the count bound matters; in inline mode
// a supersampled crop's base64 escape can reach a few hundred KB to ~1 MB,
// so the byte budget caps worst-case retention at ~32 MB.
const (
	pdfEscCacheMax      = 48
	pdfEscCacheMaxBytes = 32 << 20
)

// pdfEscEntry is one cached PDF-pane frame: the ready-to-paint kitty
// escape and the image id it transmits under.
type pdfEscEntry struct {
	image string
	id    uint32
}

// pdfEscCache memoises rendered PDF-pane frames keyed by block + geometry +
// reload generation, so revisiting a block (or arriving via prefetch) skips
// the crop + PNG encode + escape build entirely. Recency is LRU: get moves
// a hit to the back of the eviction order, so the frame being displayed is
// never the eviction victim. Shared by the render tick goroutine and the
// neighbor-prefetch goroutines, hence the mutex.
//
// The cache also owns the t=f transfer files, with one crucial exception:
// the file backing the currently *painted* frame (the escape held in
// Model.PDFImage) is pinned via pin() and never unlinked by clear() or
// eviction — the terminal re-reads that path on every repaint of the pane,
// so unlinking it would blank the pane until the next successful render.
type pdfEscCache struct {
	mu       sync.Mutex
	entries  map[string]pdfEscEntry
	order    []string // LRU order, oldest first
	inflight map[string]bool
	xferDir  string // non-empty → t=f file transmission
	// pinned is the transfer file backing the currently displayed frame.
	pinned string
	// epoch increments on clear(); renders snapshot it before their work
	// so a slow render straddling a PDF reload cannot re-insert a frame
	// of the pre-reload document.
	epoch int
	// totalBytes tracks cumulative escape sizes for the byte budget.
	totalBytes int
	// wg counts in-flight render/prefetch work so the process can drain
	// it before removing xferDir (Bubble Tea abandons Cmd goroutines on
	// quit). WaitGroup carries its own synchronisation — not under mu.
	wg sync.WaitGroup
}

func newPDFEscCache(xferDir string) *pdfEscCache {
	return &pdfEscCache{
		entries:  make(map[string]pdfEscEntry),
		inflight: make(map[string]bool),
		xferDir:  xferDir,
	}
}

// get returns the cached frame for key and refreshes its LRU recency.
func (c *pdfEscCache) get(key string) (pdfEscEntry, bool) {
	if c == nil {
		return pdfEscEntry{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if ok {
		c.touchLocked(key)
	}
	return e, ok
}

// touchLocked moves key to the back of the eviction order. Caller holds mu.
func (c *pdfEscCache) touchLocked(key string) {
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			c.order = append(c.order, key)
			return
		}
	}
}

// put inserts a frame rendered under the given epoch. A stale epoch means
// clear() ran (PDF reload) while this render was in flight: the frame
// depicts the pre-reload document, so it is dropped and its transfer file
// removed instead of poisoning a cache slot with an unreachable key.
func (c *pdfEscCache) put(key string, e pdfEscEntry, epoch int) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if epoch != c.epoch {
		c.removeFileLocked(key)
		return
	}
	if old, exists := c.entries[key]; exists {
		c.totalBytes -= len(old.image)
		c.touchLocked(key)
	} else {
		c.order = append(c.order, key)
	}
	c.entries[key] = e
	c.totalBytes += len(e.image)
	for len(c.order) > pdfEscCacheMax || (c.totalBytes > pdfEscCacheMaxBytes && len(c.order) > 1) {
		oldest := c.order[0]
		c.order = c.order[1:]
		c.totalBytes -= len(c.entries[oldest].image)
		delete(c.entries, oldest)
		c.removeFileLocked(oldest)
	}
}

// removeFileLocked unlinks key's transfer file unless it is the pinned
// (currently displayed) frame. Caller holds mu.
func (c *pdfEscCache) removeFileLocked(key string) {
	if c.xferDir == "" {
		return
	}
	path := c.framePath(key)
	if path == c.pinned {
		return
	}
	_ = os.Remove(path)
}

// pin marks path as the transfer file behind the currently painted frame.
// The previously pinned file is unlinked if no live cache entry owns it
// (its entry may have been evicted or cleared while it was pinned).
func (c *pdfEscCache) pin(path string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	prev := c.pinned
	c.pinned = path
	if prev == "" || prev == path || c.xferDir == "" {
		return
	}
	for key := range c.entries {
		if c.framePath(key) == prev {
			return // still cached; eviction will handle it later
		}
	}
	_ = os.Remove(prev)
}

// currentEpoch snapshots the cache epoch before a render starts.
func (c *pdfEscCache) currentEpoch() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.epoch
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

// renderStarted/renderDone bracket in-flight render work for drainRenders.
func (c *pdfEscCache) renderStarted() {
	if c == nil {
		return
	}
	c.wg.Add(1)
}

func (c *pdfEscCache) renderDone() {
	if c == nil {
		return
	}
	c.wg.Done()
}

// drainRenders waits until all in-flight render/prefetch work finished or
// the timeout elapsed. Returns true when fully drained.
func (c *pdfEscCache) drainRenders(timeout time.Duration) bool {
	if c == nil {
		return true
	}
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// framePath names the t=f PNG for a cache key. Content-addressed by the
// key hash so distinct frames never overwrite each other while cached.
func (c *pdfEscCache) framePath(key string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return filepath.Join(c.xferDir, fmt.Sprintf("crop-%016x.png", h.Sum64()))
}

// clear drops every entry and removes their transfer files — except the
// pinned file backing the frame still painted on screen (Model.PDFImage
// deliberately outlives the reload so the pane never blanks; the terminal
// re-reads that path on repaints). Called on PDF reload; bumps the epoch
// so in-flight renders of the old document discard themselves.
func (c *pdfEscCache) clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.epoch++
	if c.xferDir != "" {
		for key := range c.entries {
			path := c.framePath(key)
			if path != c.pinned {
				_ = os.Remove(path)
			}
		}
	}
	c.entries = make(map[string]pdfEscEntry)
	c.order = nil
	c.totalBytes = 0
}

// diffPDFRenderKey builds the cache key for one frame. The side prefix
// separates old/new frames of the same block; the reload generation
// invalidates everything when the PDF is rebuilt; geometry and cell pixel
// size cover pane resizes and monitor/font changes.
func diffPDFRenderKey(side string, block *parser.Block, reloadGen, wCells, hCells int, cellW, cellH float64) string {
	id := ""
	if block != nil {
		id = block.ID
	}
	return fmt.Sprintf("%s|%s|g%d|%dx%d|%.2fx%.2f", side, id, reloadGen, wCells, hCells, cellW, cellH)
}

// renderDiffPDFFrame renders (or fetches from cache) the frame for
// in.Block and returns the escape, its image id, and — in t=f mode — the
// transfer file the escape references (for pinning). Safe for concurrent
// use: this is the shared path for the on-demand render tick and the
// neighbor prefetch goroutines.
func renderDiffPDFFrame(in diffPDFRenderInputs, key string) (string, uint32, string, string) {
	transferPath := ""
	if in.Cache != nil && in.Cache.xferDir != "" {
		transferPath = in.Cache.framePath(key)
	}
	if e, ok := in.Cache.get(key); ok {
		return e.image, e.id, transferPath, ""
	}
	if in.Block == nil || in.Block.StartLine < 1 {
		return "", 0, "", pdf.NoRegionPlaceholder
	}
	file := in.Block.File
	if file == "" && in.Doc != nil {
		file = in.Doc.File
	}
	region := regionForBlockLines(in.Index, file, in.Block.StartLine, in.Block.EndLine)
	if region == nil {
		return "", 0, "", pdf.NoRegionPlaceholder
	}
	epoch := in.Cache.currentEpoch()
	paneWPx := int(float64(in.WidthCells) * in.CellWidthPx)
	paneHPx := int(float64(in.HeightCells) * in.CellHeightPx)
	var png []byte
	var err error
	if in.FullPage {
		// Whole page with the region marked — floats, margins, and page
		// context visible.
		png, err = pdf.RenderPageFitted(in.PDF, region.Page, *region, pdf.FitOptions{
			PaneWidthPx:  paneWPx,
			PaneHeightPx: paneHPx,
			MarkRegion:   true,
		})
	} else {
		// Column-aware cropping: on a two-column page a full-width crop
		// drags in the neighboring column and its figures; slice to the
		// region's column instead. Page-level detection avoids misfiring on
		// narrow inline equations in single-column papers.
		multi := in.PageLayout.IsMultiColumn(in.PDF, in.Doc, region.Page)
		png, err = pdf.CropFitted(in.PDF, *region, pdf.FitOptions{
			PaneWidthPx:  paneWPx,
			PaneHeightPx: paneHPx,
			MultiColumn:  multi,
			MarkRegion:   true,
		})
	}
	if err != nil {
		return "", 0, "", fmt.Sprintf("pdf: %v", err)
	}
	id := pdf.NextKittyImageID()
	esc, err := pdf.RenderKittyFrame(png, in.WidthCells, in.HeightCells, id, transferPath)
	if err != nil {
		return "", 0, "", fmt.Sprintf("pdf: %v", err)
	}
	in.Cache.put(key, pdfEscEntry{image: esc, id: id}, epoch)
	return esc, id, transferPath, ""
}

// warmNeighborFrames pre-renders the frames for the blocks adjacent to the
// current pair so j/k navigation hits the cache instead of the crop
// pipeline. Runs on its own goroutine; go-fitz serialises page renders
// internally, so concurrent warms are safe (merely queued). A panic in a
// warm (e.g. a document closed mid-reload) must never take the TUI down.
func warmNeighborFrames(in diffPDFRenderInputs, neighbors []*parser.Block) {
	defer in.Cache.renderDone()
	defer func() { _ = recover() }()
	for _, b := range neighbors {
		if b == nil {
			continue
		}
		nin := in
		nin.Block = b
		key := diffPDFRenderKey(in.SideKey, b, in.ReloadGen, in.WidthCells, in.HeightCells, in.CellWidthPx, in.CellHeightPx)
		if !nin.Cache.tryClaim(key) {
			continue
		}
		renderDiffPDFFrame(nin, key)
		nin.Cache.release(key)
	}
}
