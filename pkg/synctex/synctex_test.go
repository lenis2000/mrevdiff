package synctex

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fixturePath = "../../testdata/sample.synctex.gz"

func TestParseFixture_HeaderAndFiles(t *testing.T) {
	idx, err := Open(fixturePath)
	require.NoError(t, err)
	require.NotEmpty(t, idx.Files)

	p, ok := idx.File(1)
	require.True(t, ok, "tag 1 should correspond to the main source")
	require.True(t, strings.HasSuffix(p, "sample.tex"),
		"expected tag 1 to point at sample.tex, got %q", p)

	tag, ok := idx.TagFor("sample.tex")
	require.True(t, ok)
	require.Equal(t, 1, tag)
}

func TestParseFixture_RegionForLines_Theorem(t *testing.T) {
	idx, err := Open(fixturePath)
	require.NoError(t, err)

	// Lines 25-32 are the theorem body in testdata/sample.tex.
	reg := idx.RegionForLines("sample.tex", 25, 32)
	require.NotNil(t, reg, "expected a region for the theorem")
	assert.Equal(t, 1, reg.Page)
	assert.Greater(t, reg.W, 0.0)
	assert.Greater(t, reg.H, 0.0)
	// Page content starts at least ~72bp (1in margin); sanity-check bounds
	// sit inside a letter-sized page (612 x 792 bp) with slack.
	assert.Greater(t, reg.X, 50.0)
	assert.Less(t, reg.X+reg.W, 612.0)
	assert.Greater(t, reg.Y, 50.0)
	assert.Less(t, reg.Y+reg.H, 792.0)
}

func TestParseFixture_RegionForLines_Unknown(t *testing.T) {
	idx, err := Open(fixturePath)
	require.NoError(t, err)

	assert.Nil(t, idx.RegionForLines("sample.tex", 9000, 9001),
		"out-of-range lines must return nil")
	assert.Nil(t, idx.RegionForLines("nope.tex", 1, 10),
		"unknown file must return nil")
}

func TestParseMinimalStream(t *testing.T) {
	body := "SyncTeX Version:1\n" +
		"Input:1:/path/to/foo.tex\n" +
		"Output:pdf\n" +
		"Magnification:1000\n" +
		"Unit:1\n" +
		"X Offset:0\n" +
		"Y Offset:0\n" +
		"Content:\n" +
		"!100\n" +
		"{1\n" +
		"[1,5:1000000,2000000:500000,400000,100000\n" +
		"(1,7:3000000,4000000:600000,300000,50000\n" +
		"g1,7:3100000,4000000\n" +
		")\n" +
		"]\n" +
		"}1\n" +
		"Postamble:\n" +
		"Count:1\n"
	idx, err := Parse(strings.NewReader(body))
	require.NoError(t, err)

	p, ok := idx.Files[1]
	require.True(t, ok)
	require.Equal(t, filepath.Clean("/path/to/foo.tex"), p)

	reg := idx.RegionForLines("/path/to/foo.tex", 5, 5)
	require.NotNil(t, reg)
	assert.Equal(t, 1, reg.Page)

	// 1000000 sp * 1 unit / 65536 sp-per-pt * (72/72.27) -> PDF bp
	expectedX := 1000000.0 / 65536.0 * (72.0 / 72.27)
	assert.InDelta(t, expectedX, reg.X, 0.01)

	// Union across lines 5-7 on the same page.
	u := idx.RegionForLines("foo.tex", 5, 7)
	require.NotNil(t, u)
	assert.Equal(t, 1, u.Page)
	assert.GreaterOrEqual(t, u.W, reg.W)
}

func TestPagesAndEmpty(t *testing.T) {
	idx, err := Parse(strings.NewReader("SyncTeX Version:1\nContent:\n"))
	require.NoError(t, err)
	assert.Empty(t, idx.Pages())
	assert.Nil(t, idx.RegionForLines("anything", 0, 1000))
}

func TestParseRecord(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		wantOK bool
		tag    int
		line   int
		hasDim bool
	}{
		{"hbox-begin with dims", "[1,63:4736286,46693416:27189412,41957130,0", true, 1, 63, true},
		{"vbox-begin with dims", "(1,14:8332738,10386472:23592960,449650,0", true, 1, 14, true},
		{"glue no dims", "g1,14:16635076,10386472", true, 1, 14, false},
		{"kern with one scalar not full dims", "k1,63:0,4736286:4736286", true, 1, 63, false},
		{"x-point no dims", "x1,14:17204872,10386472", true, 1, 14, false},
		{"closing bracket is payload-less", "]", false, 0, 0, false},
		{"closing paren is payload-less", ")", false, 0, 0, false},
		{"short garbage", "hi", false, 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, ok := parseRecord(tc.in)
			assert.Equal(t, tc.wantOK, ok)
			if !ok {
				return
			}
			assert.Equal(t, tc.tag, r.tag)
			assert.Equal(t, tc.line, r.line)
			assert.Equal(t, tc.hasDim, r.hasDim)
		})
	}
}

func TestToRegion_BBoxMath(t *testing.T) {
	idx := &Index{unit: 1, mag: 1000}
	// baseline at v=2000000 sp, height=400000, depth=100000 -> top=v-height.
	r := idx.toRegion(1, rawRec{tag: 1, line: 1, h: 1000000, v: 2000000,
		w: 500000, hh: 400000, d: 100000, hasDim: true})
	toBP := func(sp int64) float64 {
		return float64(sp) / 65536.0 * (72.0 / 72.27)
	}
	assert.InDelta(t, toBP(1000000), r.X, 1e-6)
	assert.InDelta(t, toBP(2000000-400000), r.Y, 1e-6)
	assert.InDelta(t, toBP(500000), r.W, 1e-6)
	assert.InDelta(t, toBP(400000+100000), r.H, 1e-6)
}

func TestParseBasenameFallback(t *testing.T) {
	body := "SyncTeX Version:1\n" +
		"Input:1:/private/tmp/foo/sample.tex\n" +
		"Magnification:1000\nUnit:1\nX Offset:0\nY Offset:0\n" +
		"Content:\n{1\n" +
		"[1,10:1000000,2000000:500000,400000,100000\n" +
		"]\n}1\n"
	idx, err := Parse(strings.NewReader(body))
	require.NoError(t, err)
	// exact match
	require.NotNil(t, idx.RegionForLines("/private/tmp/foo/sample.tex", 10, 10))
	// symlink-equivalent / different-path same-basename — fall through.
	require.NotNil(t, idx.RegionForLines("/tmp/foo/sample.tex", 10, 10))
	// basename only.
	require.NotNil(t, idx.RegionForLines("sample.tex", 10, 10))
}

// TestSuffixMatch_LongestWins exercises the H1 fix: when two distinct
// files share a basename (chapters/intro.tex and appendix/intro.tex),
// the lookup must prefer the entry with the longest matching suffix
// instead of returning whichever the runtime's randomised map iteration
// hands back. A request for chapters/intro.tex should land on chapters.
func TestSuffixMatch_LongestWins(t *testing.T) {
	body := "SyncTeX Version:1\n" +
		"Input:1:/proj/chapters/intro.tex\n" +
		"Input:2:/proj/appendix/intro.tex\n" +
		"Magnification:1000\nUnit:1\nX Offset:0\nY Offset:0\n" +
		"Content:\n{1\n" +
		"[1,5:1000000,2000000:500000,400000,100000\n" +
		"]\n}1\n{2\n" +
		"[2,5:9000000,2000000:500000,400000,100000\n" +
		"]\n}2\n"
	idx, err := Parse(strings.NewReader(body))
	require.NoError(t, err)

	// Run the lookup many times: with random map iteration the buggy
	// fallback would hit both pages; the longest-suffix tiebreak must
	// always pick chapters/intro.tex (page 1).
	for i := 0; i < 20; i++ {
		reg := idx.RegionForLines("chapters/intro.tex", 5, 5)
		require.NotNil(t, reg)
		assert.Equal(t, 1, reg.Page, "request for chapters/intro.tex must hit page 1")
	}

	// A bare "intro.tex" matches both with suffix length 1: it's a tie,
	// so the lookup must refuse rather than guess.
	assert.Nil(t, idx.RegionForLines("intro.tex", 5, 5),
		"basename-only collision must not be resolved arbitrarily")
}

// TestParseErrors_HeaderAndRecord asserts that malformed Magnification
// values do NOT clobber the safe default (mag=1000) and that the
// parseErrors counter increments instead.
func TestParseErrors_HeaderAndRecord(t *testing.T) {
	body := "SyncTeX Version:1\n" +
		"Input:1:/proj/foo.tex\n" +
		"Magnification:not-a-number\n" + // malformed
		"Unit:1\nX Offset:0\nY Offset:0\n" +
		"Content:\n{1\n" +
		"[1,7:1000000,2000000:500000,400000,100000\n" +
		"junk-record\n" + // unparseable record
		"]\n}1\n"
	idx, err := Parse(strings.NewReader(body))
	require.NoError(t, err)

	assert.GreaterOrEqual(t, idx.ParseErrors(), 2, "expected at least 2 parse errors")

	// mag default of 1000 must survive — toRegion would otherwise
	// collapse coordinates to 0 if mag had been zeroed by the bad header.
	reg := idx.RegionForLines("/proj/foo.tex", 7, 7)
	require.NotNil(t, reg)
	assert.Greater(t, reg.X, 0.0, "mag default must keep coords non-zero")
}
