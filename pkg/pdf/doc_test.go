package pdf

import (
	"bytes"
	"image"
	"image/png"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lenis2000/mrevdiff/pkg/synctex"
)

// fixturePath locates testdata/sample.pdf relative to the repo root.
func fixturePath(t *testing.T, name string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", "testdata", name))
	require.NoError(t, err)
	return abs
}

func TestOpenCloseSamplePDF(t *testing.T) {
	d, err := Open(fixturePath(t, "sample.pdf"))
	require.NoError(t, err)
	defer func() { _ = d.Close() }()
	assert.NotEmpty(t, d.Path())
	assert.Greater(t, d.NumPage(), 0)

	// Close is idempotent.
	require.NoError(t, d.Close())
	require.NoError(t, d.Close())
	assert.Equal(t, 0, d.NumPage(), "num page after close")
}

func TestPageRendersAndCaches(t *testing.T) {
	d, err := Open(fixturePath(t, "sample.pdf"))
	require.NoError(t, err)
	defer func() { _ = d.Close() }()

	img1, err := d.Page(0, 100.0)
	require.NoError(t, err)
	require.NotNil(t, img1)
	assert.Greater(t, img1.Bounds().Dx(), 0)
	cacheBefore := d.CacheLen()

	img2, err := d.Page(0, 100.0)
	require.NoError(t, err)
	assert.Same(t, img1, img2, "cache hit returns same pixmap")
	assert.Equal(t, cacheBefore, d.CacheLen(), "cache size unchanged on hit")
}

func TestPageOutOfRange(t *testing.T) {
	d, err := Open(fixturePath(t, "sample.pdf"))
	require.NoError(t, err)
	defer func() { require.NoError(t, d.Close()) }()
	_, err = d.Page(9999, 100.0)
	assert.Error(t, err)
	_, err = d.Page(-1, 100.0)
	assert.Error(t, err)
}

func TestLRUEviction(t *testing.T) {
	lru := newLRU(2)
	assert.Equal(t, 0, lru.ll.Len())
	k1 := pageKey{page: 1, dpi: 72}
	k2 := pageKey{page: 2, dpi: 72}
	k3 := pageKey{page: 3, dpi: 72}
	lru.put(k1, nil)
	lru.put(k2, nil)
	lru.put(k3, nil)
	assert.Equal(t, 2, lru.ll.Len(), "cache bounded")
	_, ok := lru.get(k1)
	assert.False(t, ok, "oldest evicted")
	_, ok = lru.get(k2)
	assert.True(t, ok)
	_, ok = lru.get(k3)
	assert.True(t, ok)
}

func TestCropReturnsDecodablePNG(t *testing.T) {
	d, err := Open(fixturePath(t, "sample.pdf"))
	require.NoError(t, err)
	defer func() { require.NoError(t, d.Close()) }()

	// Bounds of page 1 in 72-dpi space.
	b, err := d.Bounds(0)
	require.NoError(t, err)
	// Crop the centre quarter of the page; it's guaranteed to intersect
	// text or figures for any synthesised paper.
	r := synctex.Region{
		Page: 1,
		X:    float64(b.Dx()) * 0.25,
		Y:    float64(b.Dy()) * 0.25,
		W:    float64(b.Dx()) * 0.5,
		H:    float64(b.Dy()) * 0.5,
	}
	png, err := CropAtDPI(d, r, 0, 100.0)
	require.NoError(t, err)
	require.NotEmpty(t, png)
	img, err := decodePNG(png)
	require.NoError(t, err)
	assert.Greater(t, img.Bounds().Dx(), 0)
	assert.Greater(t, img.Bounds().Dy(), 0)
}

func TestCropRejectsZeroExtent(t *testing.T) {
	d, err := Open(fixturePath(t, "sample.pdf"))
	require.NoError(t, err)
	defer func() { require.NoError(t, d.Close()) }()
	_, err = Crop(d, synctex.Region{Page: 1}, 0)
	assert.Error(t, err)
}

func TestHasExtent(t *testing.T) {
	assert.False(t, HasExtent(synctex.Region{}))
	assert.False(t, HasExtent(synctex.Region{Page: 1}))
	assert.False(t, HasExtent(synctex.Region{Page: 1, W: 10}))
	assert.True(t, HasExtent(synctex.Region{Page: 1, W: 10, H: 10}))
}

func TestRenderKittyValidatesInputs(t *testing.T) {
	_, err := RenderKitty(nil, 10, 10)
	assert.Error(t, err)
	_, err = RenderKitty([]byte{1, 2, 3}, 0, 10)
	assert.Error(t, err)
	_, err = RenderKitty([]byte{1, 2, 3}, 10, 0)
	assert.Error(t, err)
}

func TestRenderKittyEmitsEscape(t *testing.T) {
	d, err := Open(fixturePath(t, "sample.pdf"))
	require.NoError(t, err)
	defer func() { require.NoError(t, d.Close()) }()
	b, err := d.Bounds(0)
	require.NoError(t, err)
	r := synctex.Region{
		Page: 1,
		X:    float64(b.Dx()) * 0.25,
		Y:    float64(b.Dy()) * 0.25,
		W:    float64(b.Dx()) * 0.5,
		H:    float64(b.Dy()) * 0.5,
	}
	png, err := CropAtDPI(d, r, 0, 100.0)
	require.NoError(t, err)
	esc, err := RenderKitty(png, 20, 20)
	require.NoError(t, err)
	// Kitty graphics escape: ESC _G ... ESC \
	assert.Contains(t, esc, "\x1b_G", "kitty graphics APC prefix")
}

// decodePNG is a tiny helper that Go's image.Decode would cover but we don't
// want to depend on the blank-import side effect beyond doc_test scope.
func decodePNG(b []byte) (img interface {
	Bounds() image.Rectangle
}, err error) {
	return png.Decode(bytes.NewReader(b))
}
