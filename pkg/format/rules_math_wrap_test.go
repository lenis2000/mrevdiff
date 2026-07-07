package format

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// mathWrapOpts returns Options that enable only the math.wrap-at-break-op rule
// with wrapping enabled and a given column limit.
func mathWrapOpts(col int) Options {
	return Options{
		Rules: []string{"math.wrap-at-break-op"},
		MathWrap: MathWrapOptions{
			Enabled: true,
			Col:     col,
		},
	}
}

// ---------------------------------------------------------------------------
// Wrap fires only on overflow
// ---------------------------------------------------------------------------

func TestMathWrapFiresOnOverflow(t *testing.T) {
	// Line is 38 chars; use col=30 to force wrapping.
	input := "\\begin{equation}\nf(x) = a + b + c + d + e + f + g + h\n\\end{equation}\n"
	res := Apply([]byte(input), mathWrapOpts(30))
	assert.NotEqual(t, input, string(res.Src), "should wrap long equation")
	assert.True(t, len(res.Hits) > 0, "should have hits")
	// The wrapped result should have more lines.
	inLines := strings.Count(input, "\n")
	outLines := strings.Count(string(res.Src), "\n")
	assert.Greater(t, outLines, inLines, "should have more lines after wrapping")
}

// ---------------------------------------------------------------------------
// Short equation no-op
// ---------------------------------------------------------------------------

func TestMathWrapShortEquationNoop(t *testing.T) {
	input := "\\begin{equation}\nf(x) = a + b\n\\end{equation}\n"
	res := Apply([]byte(input), mathWrapOpts(80))
	assert.Equal(t, input, string(res.Src), "short equation should not change")
	assert.Empty(t, res.Hits)
}

// ---------------------------------------------------------------------------
// Disabled by default (opt-in)
// ---------------------------------------------------------------------------

func TestMathWrapDisabledByDefault(t *testing.T) {
	// Very long equation but MathWrap.Enabled is false (default).
	input := "\\begin{equation}\nf(x) = a + b + c + d + e + f + g + h + i + j + k + l + m + n + o + p\n\\end{equation}\n"
	res := Apply([]byte(input), Options{
		Rules: []string{"math.wrap-at-break-op"},
		MathWrap: MathWrapOptions{
			Enabled: false,
			Col:     40,
		},
	})
	assert.Equal(t, input, string(res.Src), "should not change when disabled")
	assert.Empty(t, res.Hits)
}

// ---------------------------------------------------------------------------
// Respects break-op set
// ---------------------------------------------------------------------------

func TestMathWrapBreakOps(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		col    int
		wantOp string // the operator that should start the continuation line
	}{
		{
			name:   "wrap at +",
			input:  "\\begin{equation}\nf(x) = aaaa + bbbb + cccc + dddd\n\\end{equation}\n",
			col:    30,
			wantOp: "+",
		},
		{
			name:   "wrap at -",
			input:  "\\begin{equation}\nf(x) = aaaa - bbbb - cccc - dddd\n\\end{equation}\n",
			col:    30,
			wantOp: "-",
		},
		{
			name:   "wrap at \\pm",
			input:  "\\begin{equation}\nf(x) = aaaa \\pm bbbb \\pm cccc \\pm dddd\n\\end{equation}\n",
			col:    35,
			wantOp: "\\pm",
		},
		{
			name:   "wrap at \\times",
			input:  "\\begin{equation}\nf(x) = aaaa \\times bbbb \\times cccc \\times dddd\n\\end{equation}\n",
			col:    40,
			wantOp: "\\times",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := Apply([]byte(tt.input), mathWrapOpts(tt.col))
			assert.NotEqual(t, tt.input, string(res.Src), "should have wrapped")
			lines := strings.Split(string(res.Src), "\n")
			// Find the continuation line (should start with whitespace + operator).
			foundOp := false
			for _, line := range lines {
				trimmed := strings.TrimLeft(line, " \t")
				if strings.HasPrefix(trimmed, tt.wantOp) {
					foundOp = true
					break
				}
			}
			assert.True(t, foundOp, "should find continuation line starting with %s in:\n%s", tt.wantOp, string(res.Src))
		})
	}
}

// ---------------------------------------------------------------------------
// Preserves structure (begin/end not affected)
// ---------------------------------------------------------------------------

func TestMathWrapPreservesStructure(t *testing.T) {
	input := "\\begin{equation}\nf(x) = a + b + c + d + e + f + g + h\n\\end{equation}\n"
	res := Apply([]byte(input), mathWrapOpts(30))
	out := string(res.Src)
	assert.True(t, strings.HasPrefix(out, "\\begin{equation}\n"), "should preserve \\begin")
	assert.True(t, strings.HasSuffix(out, "\\end{equation}\n"), "should preserve \\end")
}

// ---------------------------------------------------------------------------
// Multiple environments
// ---------------------------------------------------------------------------

func TestMathWrapMultipleEnvs(t *testing.T) {
	input := "\\begin{equation}\nf(x) = a + b + c + d + e + f + g + h\n\\end{equation}\n" +
		"Some text.\n" +
		"\\begin{equation*}\ng(x) = p + q + r + s + t + u + v + w\n\\end{equation*}\n"
	res := Apply([]byte(input), mathWrapOpts(30))
	assert.True(t, len(res.Hits) >= 2, "should wrap both environments, got %d hits", len(res.Hits))
}

// ---------------------------------------------------------------------------
// Rows with & are skipped (multi-column)
// ---------------------------------------------------------------------------

func TestMathWrapSkipsMultiColumn(t *testing.T) {
	input := "\\begin{align}\na &= b + c + d + e + f + g + h + i + j + k + l + m + n\n\\end{align}\n"
	res := Apply([]byte(input), mathWrapOpts(30))
	assert.Equal(t, input, string(res.Src), "multi-column rows should not be wrapped")
	assert.Empty(t, res.Hits)
}

// ---------------------------------------------------------------------------
// Protected span (verbatim)
// ---------------------------------------------------------------------------

func TestMathWrapProtectedSpan(t *testing.T) {
	input := "\\begin{verbatim}\n\\begin{equation}\nf(x) = a + b + c + d + e + f + g + h\n\\end{equation}\n\\end{verbatim}\n"
	res := Apply([]byte(input), mathWrapOpts(30))
	assert.Equal(t, input, string(res.Src), "protected span should not be touched")
	assert.Empty(t, res.Hits)
}

func TestMathWrapSkipDirectiveInsideBody(t *testing.T) {
	// A skip directive on an inner line should prevent the entire
	// environment from being rewritten.
	input := "\\begin{equation}\nf(x) = a + b + c + d + e + f + g + h % mrevdiff-fmt: skip\n\\end{equation}\n"
	res := Apply([]byte(input), mathWrapOpts(30))
	assert.Equal(t, input, string(res.Src), "skip directive inside body must suppress wrapping")
	assert.Empty(t, res.Hits)
}

// ---------------------------------------------------------------------------
// Continuation indent alignment
// ---------------------------------------------------------------------------

func TestMathWrapContinuationIndent(t *testing.T) {
	// "f(x) = a + b + c + d + e + f + g + h" — = at col 5, cont at col 6.
	input := "\\begin{equation}\nf(x) = a + b + c + d + e + f + g + h\n\\end{equation}\n"
	res := Apply([]byte(input), mathWrapOpts(30))
	lines := strings.Split(string(res.Src), "\n")
	// Find the first continuation line.
	found := false
	for _, line := range lines[1:] { // skip \begin line
		if strings.HasPrefix(line, "\\end") {
			break
		}
		trimmed := strings.TrimLeft(line, " ")
		if len(line) > len(trimmed) && startsWithBinop(trimmed) {
			// Check that the indent is 6 spaces (= is at col 5, cont at col 6).
			indent := len(line) - len(trimmed)
			assert.Equal(t, 6, indent, "continuation indent should align past = anchor")
			found = true
			break
		}
	}
	assert.True(t, found, "should find a continuation line")
}

// ---------------------------------------------------------------------------
// No break point found (line stays unchanged)
// ---------------------------------------------------------------------------

func TestMathWrapNoBreakPoint(t *testing.T) {
	// A line that's long but has no break operators at depth 0.
	input := "\\begin{equation}\n\\frac{averylongvariablename}{anotherlongvariablename}\n\\end{equation}\n"
	res := Apply([]byte(input), mathWrapOpts(30))
	assert.Equal(t, input, string(res.Src), "no break point should leave unchanged")
	assert.Empty(t, res.Hits)
}

// ---------------------------------------------------------------------------
// Brace depth: operators inside braces are not break points
// ---------------------------------------------------------------------------

func TestMathWrapRespectsBraceDepth(t *testing.T) {
	// The + inside \frac{a+b}{c+d} should NOT be a break point.
	input := "\\begin{equation}\nf(x) = \\frac{a+b}{c+d} + \\frac{e+f}{g+h} + extra\n\\end{equation}\n"
	res := Apply([]byte(input), mathWrapOpts(45))
	if string(res.Src) != input {
		// If it did wrap, make sure it didn't break inside braces.
		lines := strings.Split(string(res.Src), "\n")
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			// No line should start with content from inside a \frac.
			assert.False(t, strings.HasPrefix(trimmed, "b}"), "should not break inside braces")
		}
	}
}

// ---------------------------------------------------------------------------
// Various equation environments
// ---------------------------------------------------------------------------

func TestMathWrapVariousEnvs(t *testing.T) {
	envs := []string{"equation", "equation*", "gather", "gather*", "multline", "multline*"}
	for _, env := range envs {
		t.Run(env, func(t *testing.T) {
			input := "\\begin{" + env + "}\nf(x) = a + b + c + d + e + f + g + h\n\\end{" + env + "}\n"
			res := Apply([]byte(input), mathWrapOpts(30))
			assert.NotEqual(t, input, string(res.Src), "should wrap in %s", env)
			assert.True(t, len(res.Hits) > 0)
		})
	}
}

// ---------------------------------------------------------------------------
// Helper: findRightmostBreakOp
// ---------------------------------------------------------------------------

func TestFindRightmostBreakOp(t *testing.T) {
	tests := []struct {
		name    string
		content string
		budget  int
		wantIdx int
		wantLen int
	}{
		{
			name:    "rightmost + within budget",
			content: "a + b + c + d",
			budget:  10,
			wantIdx: 10, // third + at col 10
			wantLen: 1,
		},
		{
			name:    "no op within budget",
			content: "a + b",
			budget:  1, // only 'a' fits
			wantIdx: -1,
			wantLen: 0,
		},
		{
			name:    "command operator",
			content: "a \\pm b \\pm c",
			budget:  10,
			wantIdx: 8, // second \pm
			wantLen: 3,
		},
		{
			name:    "inside braces ignored",
			content: "a + {b + c} + d",
			budget:  15,
			wantIdx: 12, // the + after }
			wantLen: 1,
		},
		{
			name:    "multi-char := treated as atomic",
			content: "f(x) := a + b",
			budget:  12,
			wantIdx: 10, // the + at col 10
			wantLen: 1,
		},
		{
			name:    ":= not split into : and =",
			content: "f(x) := a",
			budget:  7,
			wantIdx: 5, // the full := at col 5
			wantLen: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx, ln := findRightmostBreakOp(tt.content, tt.budget)
			assert.Equal(t, tt.wantIdx, idx, "index")
			assert.Equal(t, tt.wantLen, ln, "length")
		})
	}
}

// ---------------------------------------------------------------------------
// Helper: computeWrapIndent
// ---------------------------------------------------------------------------

func TestComputeWrapIndent(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		leadingWS string
		wantLen   int
	}{
		{
			name:      "with = anchor",
			content:   "f(x) = a + b",
			leadingWS: "",
			wantLen:   6, // = at col 5, so 5+1=6 spaces
		},
		{
			name:      "with leading whitespace and = anchor",
			content:   "  f(x) = a + b",
			leadingWS: "  ",
			wantLen:   8, // ws(2) + anchor(5) + 1 = 8
		},
		{
			name:      "no anchor fallback",
			content:   "a + b + c",
			leadingWS: "  ",
			wantLen:   4, // ws(2) + 2 fallback
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := computeWrapIndent(tt.content, tt.leadingWS)
			assert.Equal(t, tt.wantLen, len(result), "indent length")
		})
	}
}
