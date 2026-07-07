package diffui

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/lenis2000/mrevdiff/pkg/parser"
)

func TestDiffPDFRenderKeyDistinguishesGeometryAndGeneration(t *testing.T) {
	b := &parser.Block{ID: "blk"}
	base := diffPDFRenderKey("n", b, 1, 80, 40, 9.0, 18.0, 0)
	cases := map[string]string{
		"reload generation": diffPDFRenderKey("n", b, 2, 80, 40, 9.0, 18.0, 0),
		"pane width":        diffPDFRenderKey("n", b, 1, 81, 40, 9.0, 18.0, 0),
		"cell size":         diffPDFRenderKey("n", b, 1, 80, 40, 10.0, 18.0, 0),
		"block":             diffPDFRenderKey("n", &parser.Block{ID: "other"}, 1, 80, 40, 9.0, 18.0, 0),
	}
	for name, got := range cases {
		if got == base {
			t.Fatalf("key must change with %s", name)
		}
	}
	if again := diffPDFRenderKey("n", b, 1, 80, 40, 9.0, 18.0, 0); again != base {
		t.Fatalf("key must be deterministic: %q vs %q", again, base)
	}
}

func TestPDFEscCachePutGetAndFIFOEviction(t *testing.T) {
	c := newPDFEscCache("")
	for i := 0; i < pdfEscCacheMax+5; i++ {
		c.put(fmt.Sprintf("k%03d", i), pdfEscEntry{image: fmt.Sprintf("esc%d", i), id: uint32(i + 1)}, 0)
	}
	if _, ok := c.get("k000"); ok {
		t.Fatalf("oldest entry must be evicted at capacity")
	}
	e, ok := c.get(fmt.Sprintf("k%03d", pdfEscCacheMax+4))
	if !ok || e.image != fmt.Sprintf("esc%d", pdfEscCacheMax+4) {
		t.Fatalf("newest entry must survive, got %+v ok=%v", e, ok)
	}
	if len(c.entries) != pdfEscCacheMax {
		t.Fatalf("cache must hold exactly %d entries, got %d", pdfEscCacheMax, len(c.entries))
	}
}

func TestPDFEscCacheEvictionRemovesTransferFiles(t *testing.T) {
	dir := t.TempDir()
	c := newPDFEscCache(dir)
	// Create the transfer file for a key, then force it out of the cache.
	key := "victim"
	path := c.framePath(key)
	if err := os.WriteFile(path, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.put(key, pdfEscEntry{image: "esc", id: 1}, 0)
	for i := 0; i < pdfEscCacheMax; i++ {
		c.put(fmt.Sprintf("filler%03d", i), pdfEscEntry{image: "x", id: uint32(i + 2)}, 0)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("evicting a cache entry must remove its transfer file (stat err = %v)", err)
	}
}

func TestPDFEscCacheClearRemovesTransferFiles(t *testing.T) {
	dir := t.TempDir()
	c := newPDFEscCache(dir)
	key := "frame"
	path := c.framePath(key)
	if err := os.WriteFile(path, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.put(key, pdfEscEntry{image: "esc", id: 1}, 0)
	c.clear()
	if _, ok := c.get(key); ok {
		t.Fatalf("clear must drop entries")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("clear must remove transfer files (stat err = %v)", err)
	}
	entries, _ := filepath.Glob(filepath.Join(dir, "*.png"))
	if len(entries) != 0 {
		t.Fatalf("no transfer files may remain after clear, got %v", entries)
	}
}

func TestPDFEscCacheTryClaim(t *testing.T) {
	c := newPDFEscCache("")
	if !c.tryClaim("k") {
		t.Fatalf("first claim must succeed")
	}
	if c.tryClaim("k") {
		t.Fatalf("second claim while in-flight must fail")
	}
	c.release("k")
	if !c.tryClaim("k") {
		t.Fatalf("claim after release must succeed")
	}
	c.release("k")
	c.put("k", pdfEscEntry{image: "esc", id: 1}, 0)
	if c.tryClaim("k") {
		t.Fatalf("claim of an already-cached key must fail")
	}
}

// TestRenderDiffPDFFrameCacheHitSkipsPipeline seeds the cache and calls the
// shared render path with a nil PDF: only a cache hit can return without
// touching the crop pipeline, so a non-empty result proves the memoisation.
func TestRenderDiffPDFFrameCacheHitSkipsPipeline(t *testing.T) {
	c := newPDFEscCache("")
	key := "hit"
	c.put(key, pdfEscEntry{image: "cached-escape", id: 11}, 0)
	in := diffPDFRenderInputs{Cache: c} // Block/PDF/Index all nil on purpose
	img, id, _, status := renderDiffPDFFrame(in, key)
	if img != "cached-escape" || id != 11 || status != "" {
		t.Fatalf("cache hit must return the stored frame, got (%q, %d, %q)", img, id, status)
	}
}

// TestPDFEscCacheGetRefreshesRecency pins the LRU behavior: a get-hit
// entry must not remain the eviction victim, or the currently displayed
// frame's t=f PNG could be unlinked behind the painted escape.
func TestPDFEscCacheGetRefreshesRecency(t *testing.T) {
	c := newPDFEscCache("")
	for i := 0; i < pdfEscCacheMax; i++ {
		c.put(fmt.Sprintf("k%03d", i), pdfEscEntry{image: "x", id: uint32(i + 1)}, 0)
	}
	if _, ok := c.get("k000"); !ok {
		t.Fatalf("k000 should still be cached")
	}
	// One more insert evicts the oldest — which must now be k001, not the
	// freshly touched k000.
	c.put("fresh", pdfEscEntry{image: "x", id: 99}, 0)
	if _, ok := c.get("k000"); !ok {
		t.Fatalf("get must refresh recency; k000 was evicted")
	}
	if _, ok := c.get("k001"); ok {
		t.Fatalf("k001 should have been the eviction victim")
	}
}

// TestPDFEscCachePinnedFileSurvivesClearAndEviction pins the fix for the
// blank-pane bug: the transfer file backing the currently painted frame
// must survive clear() (PDF reload) and FIFO/LRU eviction, because the
// terminal re-reads that path on every repaint of the escape held in
// Model.PDFImage.
func TestPDFEscCachePinnedFileSurvivesClearAndEviction(t *testing.T) {
	dir := t.TempDir()
	c := newPDFEscCache(dir)
	key := "displayed"
	path := c.framePath(key)
	if err := os.WriteFile(path, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.put(key, pdfEscEntry{image: "esc", id: 1}, 0)
	c.pin(path)

	c.clear()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("clear must not unlink the pinned (displayed) frame: %v", err)
	}

	// Refill past capacity at the new epoch; the pinned file must survive
	// even though its entry is long gone.
	epoch := c.currentEpoch()
	for i := 0; i < pdfEscCacheMax+2; i++ {
		c.put(fmt.Sprintf("k%03d", i), pdfEscEntry{image: "x", id: uint32(i + 2)}, epoch)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("eviction must not unlink the pinned frame: %v", err)
	}

	// Re-pinning to a new path releases the orphaned old file.
	newPath := c.framePath("next-displayed")
	if err := os.WriteFile(newPath, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.pin(newPath)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("re-pinning must remove the orphaned previous file (stat err = %v)", err)
	}
}

// TestPDFEscCacheStaleEpochPutIsDropped pins the reload race fix: a render
// that started before clear() must not insert its pre-reload frame, and
// its just-written transfer file must be cleaned up.
func TestPDFEscCacheStaleEpochPutIsDropped(t *testing.T) {
	dir := t.TempDir()
	c := newPDFEscCache(dir)
	staleEpoch := c.currentEpoch()
	c.clear() // epoch advances

	key := "stale-frame"
	path := c.framePath(key)
	if err := os.WriteFile(path, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.put(key, pdfEscEntry{image: "esc", id: 1}, staleEpoch)
	if _, ok := c.get(key); ok {
		t.Fatalf("stale-epoch put must not insert")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stale-epoch put must remove its transfer file (stat err = %v)", err)
	}
}

// TestPDFEscCacheByteBudget pins the inline-mode memory bound: cumulative
// escape bytes above the budget evict early, always keeping the newest.
func TestPDFEscCacheByteBudget(t *testing.T) {
	c := newPDFEscCache("")
	big := string(make([]byte, pdfEscCacheMaxBytes/2+1))
	c.put("a", pdfEscEntry{image: big, id: 1}, 0)
	c.put("b", pdfEscEntry{image: big, id: 2}, 0)
	c.put("c", pdfEscEntry{image: big, id: 3}, 0)
	if _, ok := c.get("a"); ok {
		t.Fatalf("byte budget must evict the oldest oversized entries")
	}
	if _, ok := c.get("c"); !ok {
		t.Fatalf("newest entry must always survive the byte budget")
	}
}

func TestNilPDFEscCacheIsSafe(t *testing.T) {
	var c *pdfEscCache
	if _, ok := c.get("k"); ok {
		t.Fatalf("nil cache must miss")
	}
	c.put("k", pdfEscEntry{}, 0)
	if c.tryClaim("k") {
		t.Fatalf("nil cache must refuse claims")
	}
	c.release("k")
	c.clear()
}
