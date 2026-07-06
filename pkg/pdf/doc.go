// Package pdf wraps MuPDF (via go-fitz) for use by the mrevdiff UI.
// It renders pages on demand, crops sub-regions corresponding to SyncTeX
// bounding boxes, and emits kitty-graphics escape sequences (via
// blacktop/go-termimg) so the PDF pane can follow the cursor.
//
// Ownership model:
//   - Doc owns a *fitz.Document and an LRU cache of rendered page pixmaps.
//   - Concurrent calls on the same Doc are serialised via an internal mutex;
//     fitz is not goroutine-safe.
package pdf

import (
	"container/list"
	"fmt"
	"image"
	"os"
	"sync"
	"time"

	"github.com/gen2brain/go-fitz"
)

// DefaultPageCacheSize is the max number of page pixmaps retained in the
// Doc-level LRU. 16 is enough for fluid navigation around a typical
// single-paper document where only a handful of pages are in play.
const DefaultPageCacheSize = 16

// DefaultPageCacheBytes bounds the LRU by cumulative pixmap size. With
// 2× supersampling a letter page can reach ~75 MB RGBA (450 DPI), so a
// pure count bound of 16 would allow >1 GB of retained pixmaps; the byte
// budget evicts early instead while leaving small-DPI behavior unchanged.
const DefaultPageCacheBytes = 256 << 20

// Doc wraps an opened PDF with a bounded pixmap LRU and a Close guard.
type Doc struct {
	path   string
	mtime  time.Time
	doc    *fitz.Document
	cache  *lruPages
	mu     sync.Mutex
	closed bool
}

// Open loads a PDF from disk. The caller must Close the result.
func Open(path string) (*Doc, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("pdf: stat %q: %w", path, err)
	}
	fd, err := fitz.New(path)
	if err != nil {
		return nil, fmt.Errorf("pdf: open %q: %w", path, err)
	}
	return &Doc{
		path:  path,
		mtime: info.ModTime(),
		doc:   fd,
		cache: newLRU(DefaultPageCacheSize),
	}, nil
}

// Close releases the underlying MuPDF handle and drops the cache. Calling
// Close twice is safe and returns nil.
func (d *Doc) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	d.cache = nil
	if d.doc != nil {
		err := d.doc.Close()
		d.doc = nil
		return err
	}
	return nil
}

// Path returns the file path this Doc was opened from.
func (d *Doc) Path() string { return d.path }

// Mtime returns the file mtime observed at Open time; downstream caches can
// key on it to invalidate when the PDF is rebuilt.
func (d *Doc) Mtime() time.Time { return d.mtime }

// NumPage returns the number of pages in the document.
func (d *Doc) NumPage() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.doc == nil {
		return 0
	}
	return d.doc.NumPage()
}

// Bounds returns the page bounding box in PDF big points (72 dpi).
func (d *Doc) Bounds(page int) (image.Rectangle, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.doc == nil {
		return image.Rectangle{}, fmt.Errorf("pdf: document closed")
	}
	return d.doc.Bound(page)
}

// Page returns a rendered pixmap for (pageIdx, dpi). Hits the LRU cache so
// repeated calls during cursor motion amortise the MuPDF render cost.
func (d *Doc) Page(pageIdx int, dpi float64) (*image.RGBA, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.doc == nil {
		return nil, fmt.Errorf("pdf: document closed")
	}
	if pageIdx < 0 || pageIdx >= d.doc.NumPage() {
		return nil, fmt.Errorf("pdf: page %d out of range", pageIdx)
	}
	key := pageKey{page: pageIdx, dpi: dpi}
	if img, ok := d.cache.get(key); ok {
		return img, nil
	}
	img, err := d.doc.ImageDPI(pageIdx, dpi)
	if err != nil {
		return nil, fmt.Errorf("pdf: render page %d: %w", pageIdx, err)
	}
	d.cache.put(key, img)
	return img, nil
}

// CacheLen returns the number of pages currently held in the pixmap cache.
// Primarily used by tests.
func (d *Doc) CacheLen() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cache == nil {
		return 0
	}
	return d.cache.ll.Len()
}

// pageKey is the LRU key for rendered pixmaps.
type pageKey struct {
	page int
	dpi  float64
}

// lruPages is a bounded LRU over rendered page pixmaps: at most max
// entries AND at most maxBytes of cumulative pixel data (always keeping
// at least one entry so the current page stays cached).
type lruPages struct {
	max      int
	maxBytes int
	bytes    int
	ll       *list.List
	index    map[pageKey]*list.Element
}

type lruEntry struct {
	key pageKey
	img *image.RGBA
}

func newLRU(max int) *lruPages {
	if max < 1 {
		max = 1
	}
	return &lruPages{max: max, maxBytes: DefaultPageCacheBytes, ll: list.New(), index: map[pageKey]*list.Element{}}
}

func pixmapBytes(img *image.RGBA) int {
	if img == nil {
		return 0
	}
	return len(img.Pix)
}

func (l *lruPages) get(k pageKey) (*image.RGBA, bool) {
	if e, ok := l.index[k]; ok {
		l.ll.MoveToFront(e)
		return e.Value.(lruEntry).img, true
	}
	return nil, false
}

func (l *lruPages) put(k pageKey, img *image.RGBA) {
	if e, ok := l.index[k]; ok {
		l.bytes -= pixmapBytes(e.Value.(lruEntry).img)
		l.bytes += pixmapBytes(img)
		e.Value = lruEntry{key: k, img: img}
		l.ll.MoveToFront(e)
		l.evict()
		return
	}
	e := l.ll.PushFront(lruEntry{key: k, img: img})
	l.index[k] = e
	l.bytes += pixmapBytes(img)
	l.evict()
}

func (l *lruPages) evict() {
	for l.ll.Len() > l.max || (l.bytes > l.maxBytes && l.ll.Len() > 1) {
		tail := l.ll.Back()
		if tail == nil {
			break
		}
		entry := tail.Value.(lruEntry)
		l.ll.Remove(tail)
		delete(l.index, entry.key)
		l.bytes -= pixmapBytes(entry.img)
	}
}
