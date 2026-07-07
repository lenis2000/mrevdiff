package format

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// mathAlignOpts returns Options that enable only the math.align-columns rule.
func mathAlignOpts() Options {
	return Options{
		Rules:     []string{"math.align-columns"},
		MathAlign: MathAlignOptions{Enabled: true},
	}
}

// ---------------------------------------------------------------------------
// Basic alignment
// ---------------------------------------------------------------------------

func TestMathAlignBasicAlign(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		want     string
		wantHits int
	}{
		{
			name:     "simple align* with two columns",
			input:    "\\begin{align*}\na &= b \\\\\nfoo &= bar\n\\end{align*}\n",
			want:     "\\begin{align*}\na   &= b \\\\\nfoo &= bar\n\\end{align*}\n",
			wantHits: 1,
		},
		{
			name:     "already aligned (no-op)",
			input:    "\\begin{align*}\na   &= b \\\\\nfoo &= bar\n\\end{align*}\n",
			want:     "\\begin{align*}\na   &= b \\\\\nfoo &= bar\n\\end{align*}\n",
			wantHits: 0,
		},
		{
			name:     "three columns",
			input:    "\\begin{align*}\na &= b &+ c \\\\\nfoo &= bar &+ baz\n\\end{align*}\n",
			want:     "\\begin{align*}\na   &= b   &+ c \\\\\nfoo &= bar &+ baz\n\\end{align*}\n",
			wantHits: 1,
		},
		{
			name:     "single row no alignment needed",
			input:    "\\begin{align*}\na &= b\n\\end{align*}\n",
			want:     "\\begin{align*}\na &= b\n\\end{align*}\n",
			wantHits: 0,
		},
		{
			name:     "matrix env",
			input:    "\\begin{pmatrix}\n1 & 2 \\\\\n100 & 200\n\\end{pmatrix}\n",
			want:     "\\begin{pmatrix}\n1   & 2 \\\\\n100 & 200\n\\end{pmatrix}\n",
			wantHits: 1,
		},
		{
			name:     "cases env",
			input:    "\\begin{cases}\nx & \\text{if } y > 0 \\\\\nxy & \\text{otherwise}\n\\end{cases}\n",
			want:     "\\begin{cases}\nx  & \\text{if } y > 0 \\\\\nxy & \\text{otherwise}\n\\end{cases}\n",
			wantHits: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := Apply([]byte(tt.input), mathAlignOpts())
			assert.Equal(t, tt.want, string(res.Src), "source mismatch")
			assert.Equal(t, tt.wantHits, len(res.Hits), "hit count")
		})
	}
}

// ---------------------------------------------------------------------------
// Refusal cases
// ---------------------------------------------------------------------------

func TestMathAlignRefusal(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantSame bool // expect no change
	}{
		{
			name:     "line comment causes refusal",
			input:    "\\begin{align*}\na &= b \\\\ % comment\nfoo &= bar\n\\end{align*}\n",
			wantSame: true,
		},
		{
			name:     "nested aligned env causes refusal",
			input:    "\\begin{align*}\n\\begin{aligned}\na &= b\n\\end{aligned} &= c \\\\\nfoo &= bar\n\\end{align*}\n",
			wantSame: true,
		},
		{
			name:     "unequal cell counts causes refusal",
			input:    "\\begin{align*}\na &= b &= c \\\\\nfoo &= bar\n\\end{align*}\n",
			wantSame: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := Apply([]byte(tt.input), mathAlignOpts())
			if tt.wantSame {
				assert.Equal(t, tt.input, string(res.Src), "source should be unchanged")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Protected spans and skip directives
// ---------------------------------------------------------------------------

func TestMathAlignProtected(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantSame bool
	}{
		{
			name:     "inside verbatim (no rewrite)",
			input:    "\\begin{verbatim}\n\\begin{align*}\na &= b \\\\\nfoo &= bar\n\\end{align*}\n\\end{verbatim}\n",
			wantSame: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := Apply([]byte(tt.input), mathAlignOpts())
			if tt.wantSame {
				assert.Equal(t, tt.input, string(res.Src), "source should be unchanged")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Disabled by config
// ---------------------------------------------------------------------------

func TestMathAlignDisabled(t *testing.T) {
	input := "\\begin{align*}\na &= b \\\\\nfoo &= bar\n\\end{align*}\n"
	opts := Options{
		Rules:     []string{"math.align-columns"},
		MathAlign: MathAlignOptions{Enabled: false},
	}
	res := Apply([]byte(input), opts)
	assert.Equal(t, input, string(res.Src), "disabled rule should not change source")
	assert.Empty(t, res.Hits)
}

// ---------------------------------------------------------------------------
// Env set and skip config
// ---------------------------------------------------------------------------

func TestMathAlignCustomEnvs(t *testing.T) {
	input := "\\begin{myenv}\na &= b \\\\\nfoo &= bar\n\\end{myenv}\n"
	opts := Options{
		Rules: []string{"math.align-columns"},
		MathAlign: MathAlignOptions{
			Enabled: true,
			Envs:    []string{"myenv"},
		},
	}
	res := Apply([]byte(input), opts)
	assert.NotEqual(t, input, string(res.Src), "custom env should be aligned")
	assert.Equal(t, 1, len(res.Hits))
}

func TestMathAlignSkipEnv(t *testing.T) {
	input := "\\begin{align*}\na &= b \\\\\nfoo &= bar\n\\end{align*}\n"
	opts := Options{
		Rules: []string{"math.align-columns"},
		MathAlign: MathAlignOptions{
			Enabled: true,
			Skip:    []string{"align*"},
		},
	}
	res := Apply([]byte(input), opts)
	assert.Equal(t, input, string(res.Src), "skipped env should not be aligned")
	assert.Empty(t, res.Hits)
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func TestSplitRows(t *testing.T) {
	body := []byte("a &= b \\\\\nfoo &= bar")
	rows := splitRows(body)
	assert.Equal(t, 2, len(rows))
	assert.Equal(t, "a &= b", rows[0].content)
	assert.Contains(t, rows[0].suffix, "\\\\")
	assert.Equal(t, "foo &= bar", rows[1].content)
}

func TestSplitCells(t *testing.T) {
	cells := splitCells("a &= b &+ c")
	assert.Equal(t, []string{"a ", "= b ", "+ c"}, cells)
}

func TestHasLineComment(t *testing.T) {
	assert.True(t, hasLineComment("a &= b % comment"))
	assert.False(t, hasLineComment("a &= b"))
	assert.False(t, hasLineComment("a &= b \\% escaped"))
}

func TestContainsNestedAlignedEnv(t *testing.T) {
	envSet := map[string]bool{"aligned": true, "align": true}
	assert.True(t, containsNestedAlignedEnv(
		[]byte("\\begin{aligned}\na &= b\n\\end{aligned}"), envSet))
	assert.False(t, containsNestedAlignedEnv(
		[]byte("a &= b"), envSet))
}

func TestVisualWidth(t *testing.T) {
	assert.Equal(t, 5, visualWidth("hello"))
	assert.Equal(t, 3, visualWidth("abc"))
	assert.Equal(t, 0, visualWidth(""))
}

// ---------------------------------------------------------------------------
// Row suffix with optional skip
// ---------------------------------------------------------------------------

func TestAlignWithRowSkip(t *testing.T) {
	input := "\\begin{align*}\na &= b \\\\[2pt]\nfoo &= bar\n\\end{align*}\n"
	res := Apply([]byte(input), mathAlignOpts())
	assert.Contains(t, string(res.Src), "\\\\[2pt]", "row skip should be preserved")
}

// ---------------------------------------------------------------------------
// Tabular environment
// ---------------------------------------------------------------------------

func TestMathAlignTabular(t *testing.T) {
	input := "\\begin{tabular}{ll}\na & b \\\\\nfoo & bar\n\\end{tabular}\n"
	want := "\\begin{tabular}{ll}\na   & b \\\\\nfoo & bar\n\\end{tabular}\n"
	res := Apply([]byte(input), mathAlignOpts())
	assert.Equal(t, want, string(res.Src))
	assert.Equal(t, 1, len(res.Hits))
}

// ---------------------------------------------------------------------------
// Skip directives inside math env body
// ---------------------------------------------------------------------------

func TestMathAlignSkipDirectiveInsideBody(t *testing.T) {
	// A skip directive on an inner line should prevent the entire
	// environment from being rewritten.
	input := "\\begin{align*}\na &= b \\\\ % mrevdiff-fmt: skip\nfoo &= bar\n\\end{align*}\n"
	res := Apply([]byte(input), mathAlignOpts())
	assert.Equal(t, input, string(res.Src), "skip directive inside body must suppress alignment")
	assert.Empty(t, res.Hits)
}

func TestMathAlignOffOnDirectiveInsideBody(t *testing.T) {
	// An off/on block inside the env body should prevent alignment.
	input := "\\begin{align*}\na &= b \\\\\n% mrevdiff-fmt: off\nfoo &= bar\n% mrevdiff-fmt: on\n\\end{align*}\n"
	res := Apply([]byte(input), mathAlignOpts())
	assert.Equal(t, input, string(res.Src), "off/on block inside body must suppress alignment")
	assert.Empty(t, res.Hits)
}
