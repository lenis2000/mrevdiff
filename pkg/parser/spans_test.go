package parser

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProtectedSpans_NoProtectedRegions(t *testing.T) {
	src := []byte("Hello world\nThis is plain text\n")
	spans := ProtectedSpans(src)
	assert.Empty(t, spans)
}

func TestProtectedSpans_CommentLine(t *testing.T) {
	src := []byte("text % a comment\n")
	spans := ProtectedSpans(src)
	require.Len(t, spans, 1)
	assert.Equal(t, "comment-line", spans[0].Kind)
	assert.Equal(t, "% a comment", string(src[spans[0].Start:spans[0].End]))
}

func TestProtectedSpans_CommentLineStartOfLine(t *testing.T) {
	src := []byte("% entire line is comment\n")
	spans := ProtectedSpans(src)
	require.Len(t, spans, 1)
	assert.Equal(t, "comment-line", spans[0].Kind)
	assert.Equal(t, "% entire line is comment", string(src[spans[0].Start:spans[0].End]))
}

func TestProtectedSpans_EscapedPercent(t *testing.T) {
	src := []byte(`10\% discount`)
	spans := ProtectedSpans(src)
	assert.Empty(t, spans, "escaped percent should not create a comment span")
}

func TestProtectedSpans_Verbatim(t *testing.T) {
	src := []byte("before\n\\begin{verbatim}\nhello $$ world\n\\end{verbatim}\nafter\n")
	spans := ProtectedSpans(src)
	// Should have one verbatim span covering \begin{verbatim} through \end{verbatim}
	var verbSpan *ProtectedSpan
	for i := range spans {
		if spans[i].Kind == "verbatim" {
			verbSpan = &spans[i]
			break
		}
	}
	require.NotNil(t, verbSpan, "expected a verbatim span")
	content := string(src[verbSpan.Start:verbSpan.End])
	assert.Contains(t, content, `\begin{verbatim}`)
	assert.Contains(t, content, `\end{verbatim}`)
}

func TestProtectedSpans_VerbatimStar(t *testing.T) {
	src := []byte("\\begin{verbatim*}\ncode here\n\\end{verbatim*}\n")
	spans := ProtectedSpans(src)
	var found bool
	for _, sp := range spans {
		if sp.Kind == "verbatim" {
			found = true
			assert.Contains(t, string(src[sp.Start:sp.End]), `\begin{verbatim*}`)
		}
	}
	assert.True(t, found, "expected a verbatim span for verbatim*")
}

func TestProtectedSpans_Lstlisting(t *testing.T) {
	src := []byte("\\begin{lstlisting}\nint x = 1;\n\\end{lstlisting}\n")
	spans := ProtectedSpans(src)
	var found bool
	for _, sp := range spans {
		if sp.Kind == "lstlisting" {
			found = true
		}
	}
	assert.True(t, found, "expected an lstlisting span")
}

func TestProtectedSpans_CommentEnv(t *testing.T) {
	src := []byte("\\begin{comment}\nthis is hidden\n\\end{comment}\n")
	spans := ProtectedSpans(src)
	var found bool
	for _, sp := range spans {
		if sp.Kind == "comment-env" {
			found = true
		}
	}
	assert.True(t, found, "expected a comment-env span")
}

func TestProtectedSpans_VerbInlinePipe(t *testing.T) {
	src := []byte(`Use \verb|x + y| in text`)
	spans := ProtectedSpans(src)
	require.Len(t, spans, 1)
	assert.Equal(t, "verb-inline", spans[0].Kind)
	assert.Equal(t, `\verb|x + y|`, string(src[spans[0].Start:spans[0].End]))
}

func TestProtectedSpans_VerbInlinePlus(t *testing.T) {
	src := []byte(`Use \verb+code here+ in text`)
	spans := ProtectedSpans(src)
	require.Len(t, spans, 1)
	assert.Equal(t, "verb-inline", spans[0].Kind)
	assert.Equal(t, `\verb+code here+`, string(src[spans[0].Start:spans[0].End]))
}

func TestProtectedSpans_VerbInlineBang(t *testing.T) {
	src := []byte(`Use \verb!foo! in text`)
	spans := ProtectedSpans(src)
	require.Len(t, spans, 1)
	assert.Equal(t, "verb-inline", spans[0].Kind)
	assert.Equal(t, `\verb!foo!`, string(src[spans[0].Start:spans[0].End]))
}

func TestProtectedSpans_VerbStarInline(t *testing.T) {
	src := []byte(`Use \verb*+x y+ in text`)
	spans := ProtectedSpans(src)
	require.Len(t, spans, 1)
	assert.Equal(t, "verb-inline", spans[0].Kind)
	assert.Equal(t, `\verb*+x y+`, string(src[spans[0].Start:spans[0].End]))
}

func TestProtectedSpans_LstinlineBraces(t *testing.T) {
	src := []byte(`Use \lstinline{int x = 1;} here`)
	spans := ProtectedSpans(src)
	require.Len(t, spans, 1)
	assert.Equal(t, "verb-inline", spans[0].Kind)
	assert.Equal(t, `\lstinline{int x = 1;}`, string(src[spans[0].Start:spans[0].End]))
}

func TestProtectedSpans_LstinlineDelimiter(t *testing.T) {
	src := []byte(`Use \lstinline|int x;| here`)
	spans := ProtectedSpans(src)
	require.Len(t, spans, 1)
	assert.Equal(t, "verb-inline", spans[0].Kind)
	assert.Equal(t, `\lstinline|int x;|`, string(src[spans[0].Start:spans[0].End]))
}

func TestProtectedSpans_LstinlineWithOptional(t *testing.T) {
	src := []byte(`Use \lstinline[language=Go]{func main()} here`)
	spans := ProtectedSpans(src)
	require.Len(t, spans, 1)
	assert.Equal(t, "verb-inline", spans[0].Kind)
	assert.Equal(t, `\lstinline[language=Go]{func main()}`, string(src[spans[0].Start:spans[0].End]))
}

func TestProtectedSpans_Mixed(t *testing.T) {
	src := []byte("text \\verb|x| and % comment\nmore \\begin{verbatim}\nhello\n\\end{verbatim}\n")
	spans := ProtectedSpans(src)
	// Expect: verb-inline, comment-line, verbatim (sorted by start)
	require.True(t, len(spans) >= 3, "expected at least 3 spans, got %d", len(spans))

	// Verify sorted order
	for i := 1; i < len(spans); i++ {
		assert.LessOrEqual(t, spans[i-1].Start, spans[i].Start,
			"spans should be sorted by Start")
	}
}

func TestProtectedSpans_MultipleVerbOnOneLine(t *testing.T) {
	src := []byte(`\verb|a| and \verb|b|`)
	spans := ProtectedSpans(src)
	require.Len(t, spans, 2)
	assert.Equal(t, `\verb|a|`, string(src[spans[0].Start:spans[0].End]))
	assert.Equal(t, `\verb|b|`, string(src[spans[1].Start:spans[1].End]))
}

func TestProtectedSpans_CommentedOutBeginVerbatim(t *testing.T) {
	// A commented-out \begin{verbatim} should NOT start a verbatim span.
	src := []byte("% \\begin{verbatim}\nThis should be formatted.\n\\end{verbatim}\n")
	spans := ProtectedSpans(src)
	// The only spans should be the comment-line on line 1.
	// There should be no verbatim span because the \begin is inside a comment.
	for _, sp := range spans {
		if sp.Kind == "verbatim" {
			t.Fatalf("commented-out \\begin{verbatim} should not create a verbatim span, got span [%d,%d)", sp.Start, sp.End)
		}
	}
}

func TestProtectedSpans_NestedVerbatim(t *testing.T) {
	// A verbatim inside verbatim is impossible in LaTeX, but make sure our
	// scanner handles the string \begin{verbatim} inside a verbatim block
	// (it's literal text, not a real \begin).
	src := []byte("\\begin{verbatim}\n\\begin{verbatim}\n\\end{verbatim}\n")
	spans := ProtectedSpans(src)
	var verbSpans []ProtectedSpan
	for _, sp := range spans {
		if sp.Kind == "verbatim" {
			verbSpans = append(verbSpans, sp)
		}
	}
	// The inner \begin{verbatim} is literal text; scanner finds the first
	// \end{verbatim} and closes. So we get one span.
	require.Len(t, verbSpans, 1)
}

func TestProtectedSpans_UnclosedVerbatim(t *testing.T) {
	src := []byte("\\begin{verbatim}\ncode without end\n")
	spans := ProtectedSpans(src)
	var verbSpan *ProtectedSpan
	for i := range spans {
		if spans[i].Kind == "verbatim" {
			verbSpan = &spans[i]
			break
		}
	}
	require.NotNil(t, verbSpan)
	assert.Equal(t, len(src), verbSpan.End, "unclosed verbatim should extend to EOF")
}

// --- LineOffsets tests -------------------------------------------------------

func TestLineOffsets_Empty(t *testing.T) {
	offsets := LineOffsets([]byte{})
	// offsets[0] = 0 (sentinel), offsets[1] = 0 (line 1 starts at byte 0)
	assert.Equal(t, []int{0, 0}, offsets)
}

func TestLineOffsets_SingleLine(t *testing.T) {
	offsets := LineOffsets([]byte("hello"))
	assert.Equal(t, []int{0, 0}, offsets)
}

func TestLineOffsets_SingleLineNewline(t *testing.T) {
	offsets := LineOffsets([]byte("hello\n"))
	assert.Equal(t, []int{0, 0, 6}, offsets)
}

func TestLineOffsets_MultiLine(t *testing.T) {
	src := []byte("ab\ncd\nef\n")
	offsets := LineOffsets(src)
	// line 1 starts at 0, line 2 at 3, line 3 at 6, sentinel at 9
	assert.Equal(t, []int{0, 0, 3, 6, 9}, offsets)
}

func TestLineOffsets_NoTrailingNewline(t *testing.T) {
	src := []byte("ab\ncd")
	offsets := LineOffsets(src)
	assert.Equal(t, []int{0, 0, 3}, offsets)
}

// --- OverlapsProtected tests ------------------------------------------------

func TestOverlapsProtected_NoSpans(t *testing.T) {
	assert.False(t, OverlapsProtected(0, 10, nil))
}

func TestOverlapsProtected_NoOverlap(t *testing.T) {
	spans := []ProtectedSpan{{Start: 20, End: 30, Kind: "verbatim"}}
	assert.False(t, OverlapsProtected(0, 10, spans))
	assert.False(t, OverlapsProtected(30, 40, spans))
}

func TestOverlapsProtected_Overlap(t *testing.T) {
	spans := []ProtectedSpan{{Start: 20, End: 30, Kind: "verbatim"}}
	assert.True(t, OverlapsProtected(15, 25, spans))
	assert.True(t, OverlapsProtected(25, 35, spans))
	assert.True(t, OverlapsProtected(20, 30, spans))
	assert.True(t, OverlapsProtected(22, 28, spans))
}

func TestOverlapsProtected_EdgeCases(t *testing.T) {
	spans := []ProtectedSpan{{Start: 10, End: 20, Kind: "verbatim"}}
	// Half-open: [10, 20) and [20, 30) do not overlap
	assert.False(t, OverlapsProtected(20, 30, spans))
	// But [10, 20) and [19, 30) do
	assert.True(t, OverlapsProtected(19, 30, spans))
}

func TestOverlapsProtected_MultipleSpans(t *testing.T) {
	spans := []ProtectedSpan{
		{Start: 10, End: 20, Kind: "verbatim"},
		{Start: 30, End: 40, Kind: "comment-line"},
	}
	assert.False(t, OverlapsProtected(0, 10, spans))
	assert.True(t, OverlapsProtected(15, 25, spans))
	assert.False(t, OverlapsProtected(20, 30, spans))
	assert.True(t, OverlapsProtected(35, 45, spans))
}

func TestProtectedSpans_CapitalVerbatim(t *testing.T) {
	src := []byte("\\begin{Verbatim}\nfancyvrb body\n\\end{Verbatim}\n")
	spans := ProtectedSpans(src)
	var found bool
	for _, sp := range spans {
		if sp.Kind == "verbatim" {
			found = true
			assert.Contains(t, string(src[sp.Start:sp.End]), `\begin{Verbatim}`)
		}
	}
	assert.True(t, found, "fancyvrb \\begin{Verbatim} must be detected")
}

func TestProtectedSpans_Minted(t *testing.T) {
	src := []byte("\\begin{minted}{python}\nprint('hi')\n\\end{minted}\n")
	spans := ProtectedSpans(src)
	var found bool
	for _, sp := range spans {
		if sp.Kind == "lstlisting" {
			found = true
			assert.Contains(t, string(src[sp.Start:sp.End]), `\begin{minted}`)
		}
	}
	assert.True(t, found, "minted env must be detected as a protected listing")
}

func TestProtectedSpansExtra_CustomEnv(t *testing.T) {
	src := []byte("\\begin{mycode}\nverbatim body  \n\\end{mycode}\n")
	spans := ProtectedSpansExtra(src, []string{"mycode"})
	var found bool
	for _, sp := range spans {
		if sp.Kind == "verbatim" {
			found = true
			assert.Contains(t, string(src[sp.Start:sp.End]), `\begin{mycode}`)
		}
	}
	assert.True(t, found, "user-extended verbatim env must be detected")

	// Without the extra it must NOT be detected.
	plain := ProtectedSpans(src)
	for _, sp := range plain {
		assert.NotContains(t, string(src[sp.Start:sp.End]), `\begin{mycode}`,
			"default ProtectedSpans must not pick up unknown envs")
	}
}
