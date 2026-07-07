package format

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// mathContOpts returns Options that enable only the math.continuation-indent rule.
func mathContOpts() Options {
	return Options{
		Rules: []string{"math.continuation-indent"},
	}
}

// ---------------------------------------------------------------------------
// Basic continuation indent
// ---------------------------------------------------------------------------

func TestMathContBasic(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		want     string
		wantHits int
	}{
		{
			name:  "simple equation with continuation +",
			input: "\\begin{equation}\nf(x) = a\n+ b\n\\end{equation}\n",
			// anchor at col 5 (the = sign), so binop should be at col 6
			want:     "\\begin{equation}\nf(x) = a\n      + b\n\\end{equation}\n",
			wantHits: 1,
		},
		{
			name:     "equation* with continuation -",
			input:    "\\begin{equation*}\nf(x) = a\n- b\n\\end{equation*}\n",
			want:     "\\begin{equation*}\nf(x) = a\n      - b\n\\end{equation*}\n",
			wantHits: 1,
		},
		{
			name:  "gather with \\equiv anchor",
			input: "\\begin{gather}\nA \\equiv B\n+ C\n\\end{gather}\n",
			// anchor at col 2 (\equiv), binop at col 3
			want:     "\\begin{gather}\nA \\equiv B\n   + C\n\\end{gather}\n",
			wantHits: 1,
		},
		{
			name:  "multline with := anchor",
			input: "\\begin{multline}\nf(x) := a\n+ b\n+ c\n\\end{multline}\n",
			// := at col 5, binop at col 6
			want:     "\\begin{multline}\nf(x) := a\n      + b\n      + c\n\\end{multline}\n",
			wantHits: 1,
		},
		{
			name:     "already correctly indented (no-op)",
			input:    "\\begin{equation}\nf(x) = a\n      + b\n\\end{equation}\n",
			want:     "\\begin{equation}\nf(x) = a\n      + b\n\\end{equation}\n",
			wantHits: 0,
		},
		{
			name:  "\\le anchor",
			input: "\\begin{equation}\nx \\le y\n+ z\n\\end{equation}\n",
			// \\le at col 2, binop at col 3
			want:     "\\begin{equation}\nx \\le y\n   + z\n\\end{equation}\n",
			wantHits: 1,
		},
		{
			name:     "\\ge anchor",
			input:    "\\begin{equation}\nx \\ge y\n+ z\n\\end{equation}\n",
			want:     "\\begin{equation}\nx \\ge y\n   + z\n\\end{equation}\n",
			wantHits: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := Apply([]byte(tt.input), mathContOpts())
			assert.Equal(t, tt.want, string(res.Src), "source mismatch")
			assert.Equal(t, tt.wantHits, len(res.Hits), "hit count")
		})
	}
}

// ---------------------------------------------------------------------------
// No anchor found (skip)
// ---------------------------------------------------------------------------

func TestMathContNoAnchor(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "no relation operator",
			input: "\\begin{equation}\nf(x) + g(x)\n+ h(x)\n\\end{equation}\n",
		},
		{
			name:  "single row",
			input: "\\begin{equation}\nf(x) = a + b\n\\end{equation}\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := Apply([]byte(tt.input), mathContOpts())
			assert.Equal(t, tt.input, string(res.Src), "should not change")
			assert.Empty(t, res.Hits)
		})
	}
}

// ---------------------------------------------------------------------------
// Various binops
// ---------------------------------------------------------------------------

func TestMathContVariousBinops(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "\\pm continuation",
			input: "\\begin{equation}\nx = a\n\\pm b\n\\end{equation}\n",
			want:  "\\begin{equation}\nx = a\n   \\pm b\n\\end{equation}\n",
		},
		{
			name:  "\\cdot continuation",
			input: "\\begin{equation}\nx = a\n\\cdot b\n\\end{equation}\n",
			want:  "\\begin{equation}\nx = a\n   \\cdot b\n\\end{equation}\n",
		},
		{
			name:  "\\times continuation",
			input: "\\begin{equation}\nx = a\n\\times b\n\\end{equation}\n",
			want:  "\\begin{equation}\nx = a\n   \\times b\n\\end{equation}\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := Apply([]byte(tt.input), mathContOpts())
			assert.Equal(t, tt.want, string(res.Src), "source mismatch")
			assert.Equal(t, 1, len(res.Hits), "hit count")
		})
	}
}

// ---------------------------------------------------------------------------
// Interaction with align-columns: rows with & are skipped
// ---------------------------------------------------------------------------

func TestMathContSkipsColumnar(t *testing.T) {
	// align env with & columns — continuation-indent should skip.
	input := "\\begin{align*}\na &= b \\\\\n+ c &= d\n\\end{align*}\n"
	res := Apply([]byte(input), mathContOpts())
	assert.Equal(t, input, string(res.Src), "should not change env with & columns")
	assert.Empty(t, res.Hits)
}

// ---------------------------------------------------------------------------
// Protected span / skip directives
// ---------------------------------------------------------------------------

func TestMathContProtected(t *testing.T) {
	input := "\\begin{verbatim}\n\\begin{equation}\nf(x) = a\n+ b\n\\end{equation}\n\\end{verbatim}\n"
	res := Apply([]byte(input), mathContOpts())
	assert.Equal(t, input, string(res.Src), "verbatim env should not be touched")
	assert.Empty(t, res.Hits)
}

func TestMathContSkipDirectiveInsideBody(t *testing.T) {
	// A skip directive on an inner line should prevent the entire
	// environment from being rewritten.
	input := "\\begin{equation}\nf(x) = a\n+ b % mrevdiff-fmt: skip\n\\end{equation}\n"
	res := Apply([]byte(input), mathContOpts())
	assert.Equal(t, input, string(res.Src), "skip directive inside body must suppress cont-indent")
	assert.Empty(t, res.Hits)
}

// ---------------------------------------------------------------------------
// Non-binop continuation rows are left alone
// ---------------------------------------------------------------------------

func TestMathContNonBinopRows(t *testing.T) {
	// Second row does not start with a binop, so it should be left alone.
	input := "\\begin{equation}\nf(x) = a\ng(x)\n\\end{equation}\n"
	res := Apply([]byte(input), mathContOpts())
	assert.Equal(t, input, string(res.Src), "non-binop rows should not change")
	assert.Empty(t, res.Hits)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func TestFindRelationAnchor(t *testing.T) {
	tests := []struct {
		row  string
		want int
	}{
		{"f(x) = a", 5},
		{"A \\equiv B", 2},
		{"f(x) := a", 5},
		{"x \\le y", 2},
		{"x \\ge y", 2},
		{"no relation here", -1},
		{"{a = b}", -1}, // inside braces
	}

	for _, tt := range tests {
		t.Run(tt.row, func(t *testing.T) {
			got := findRelationAnchor(tt.row)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestContainsAmpAtDepth0(t *testing.T) {
	assert.True(t, containsAmpAtDepth0("a & b"))
	assert.False(t, containsAmpAtDepth0("a b"))
	assert.False(t, containsAmpAtDepth0("{a & b}")) // inside braces
}

func TestStartsWithBinop(t *testing.T) {
	assert.True(t, startsWithBinop("+ b"))
	assert.True(t, startsWithBinop("- b"))
	assert.True(t, startsWithBinop("\\pm b"))
	assert.True(t, startsWithBinop("\\cdot b"))
	assert.False(t, startsWithBinop("a + b"))
	assert.False(t, startsWithBinop("f(x)"))
}
