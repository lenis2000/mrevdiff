package diffui

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"mrevdiff/pkg/parser"
)

func TestDiffPDFRenderKeyDistinguishesGeometryAndGeneration(t *testing.T) {
	b := &parser.Block{ID: "blk"}
	base := diffPDFRenderKey(b, 1, 80, 40, 9.0, 18.0)
	cases := map[string]string{
		"reload generation": diffPDFRenderKey(b, 2, 80, 40, 9.0, 18.0),
		"pane width":        diffPDFRenderKey(b, 1, 81, 40, 9.0, 18.0),
		"cell size":         diffPDFRenderKey(b, 1, 80, 40, 10.0, 18.0),
		"block":             diffPDFRenderKey(&parser.Block{ID: "other"}, 1, 80, 40, 9.0, 18.0),
	}
	for name, got := range cases {
		if got == base {
			t.Fatalf("key must change with %s", name)
		}
	}
	if again := diffPDFRenderKey(b, 1, 80, 40, 9.0, 18.0); again != base {
		t.Fatalf("key must be deterministic: %q vs %q", again, base)
	}
}

func TestPDFEscCachePutGetAndFIFOEviction(t *testing.T) {
	c := newPDFEscCache("")
	for i := 0; i < pdfEscCacheMax+5; i++ {
		c.put(fmt.Sprintf("k%03d", i), pdfEscEntry{image: fmt.Sprintf("esc%d", i), id: uint32(i + 1)})
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
	c.put(key, pdfEscEntry{image: "esc", id: 1})
	for i := 0; i < pdfEscCacheMax; i++ {
		c.put(fmt.Sprintf("filler%03d", i), pdfEscEntry{image: "x", id: uint32(i + 2)})
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
	c.put(key, pdfEscEntry{image: "esc", id: 1})
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
	c.put("k", pdfEscEntry{image: "esc", id: 1})
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
	c.put(key, pdfEscEntry{image: "cached-escape", id: 11})
	in := diffPDFRenderInputs{Cache: c} // Block/PDF/Index all nil on purpose
	img, id, status := renderDiffPDFFrame(in, key)
	if img != "cached-escape" || id != 11 || status != "" {
		t.Fatalf("cache hit must return the stored frame, got (%q, %d, %q)", img, id, status)
	}
}

func TestNilPDFEscCacheIsSafe(t *testing.T) {
	var c *pdfEscCache
	if _, ok := c.get("k"); ok {
		t.Fatalf("nil cache must miss")
	}
	c.put("k", pdfEscEntry{})
	if c.tryClaim("k") {
		t.Fatalf("nil cache must refuse claims")
	}
	c.release("k")
	c.clear()
}
