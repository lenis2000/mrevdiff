package format

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runWrap(src, mode string, col int) string {
	res := Apply([]byte(src), Options{
		Wrap: WrapOptions{Mode: mode, Col: col},
	})
	return string(res.Src)
}

func TestWrap_OffIsNoop(t *testing.T) {
	// No trailing whitespace so space.trailing doesn't fire.
	src := strings.Repeat("a ", 199) + "a\n"
	got := runWrap(src, "off", 100)
	assert.Equal(t, src, got)
}

func TestWrap_SentenceBoundary(t *testing.T) {
	src := "First sentence. Second sentence. Third sentence.\n"
	got := runWrap(src, "sentence", 100)
	want := "First sentence.\nSecond sentence.\nThird sentence.\n"
	assert.Equal(t, want, got)
}

func TestWrap_PreservesIndent(t *testing.T) {
	// Use spaces, not a tab — space.tabs runs before us and would expand a tab.
	src := "    First sentence. Second sentence.\n"
	got := runWrap(src, "sentence", 100)
	want := "    First sentence.\n    Second sentence.\n"
	assert.Equal(t, want, got)
}

func TestWrap_AbbreviationsKeepSentence(t *testing.T) {
	// "et al.", "e.g.", "i.e." must NOT split.
	src := "Smith et al. showed that e.g. this works. Therefore we proceed.\n"
	got := runWrap(src, "sentence", 200)
	want := "Smith et al. showed that e.g. this works.\nTherefore we proceed.\n"
	assert.Equal(t, want, got)
}

func TestWrap_DoesNotBreakInsideMath(t *testing.T) {
	// The period inside $...$ must not start a new sentence.
	src := `Equation $a.b$. Next sentence.` + "\n"
	got := runWrap(src, "sentence", 200)
	want := "Equation $a.b$.\nNext sentence.\n"
	assert.Equal(t, want, got)
}

func TestWrap_DoesNotBreakInsideRefCommands(t *testing.T) {
	// `\eqref{eq.1}.` followed by space + capital should split AFTER \eqref{}.
	src := `See \eqref{eq:1}. Next sentence.` + "\n"
	got := runWrap(src, "sentence", 200)
	want := "See \\eqref{eq:1}.\nNext sentence.\n"
	assert.Equal(t, want, got)
}

// TestExcludedRanges_EscapedBracket asserts that a literal \] inside a
// ref-like optional argument does not terminate the optional-argument
// scan early. Without the escape arm the scanner would close the [..]
// group at the first bare ], leaving the rest of the construct (including
// the {…} body) eligible for wrapping.
func TestExcludedRanges_EscapedBracket(t *testing.T) {
	// \cite[\] note]{key} — without the fix, the scanner would stop at
	// the literal backslash-bracket pair and miss the {key} body.
	s := `prefix \cite[note with \] mark]{key} suffix`
	ranges := excludedRanges(s)
	require.NotEmpty(t, ranges)

	// The cite-and-args span must cover everything from the leading
	// backslash through the closing brace of {key}.
	citeStart := strings.Index(s, `\cite`)
	citeEnd := strings.Index(s, "}") + 1 // first } closes {key}
	covered := false
	for _, r := range ranges {
		if r[0] == citeStart && r[1] == citeEnd {
			covered = true
			break
		}
	}
	assert.True(t, covered,
		"expected a range covering [%d, %d); got %v", citeStart, citeEnd, ranges)
}

func TestWrap_ColumnGreedy(t *testing.T) {
	// "alpha bravo charlie delta echo foxtrot golf" with col=20 should
	// break at the rightmost space ≤ 20 columns.
	src := "alpha bravo charlie delta echo foxtrot golf\n"
	got := runWrap(src, "column", 20)
	// First piece must end at or before col=20.
	first := strings.SplitN(got, "\n", 2)[0]
	if visualWidth(first) > 20 {
		t.Fatalf("first piece exceeds col=20: %q", first)
	}
}

func TestWrap_SentencePlusColumn(t *testing.T) {
	// Single long sentence — sentence-only would not split it; sentence+column does.
	long := strings.Repeat("word ", 30) + "end."
	src := long + "\n"
	got := runWrap(src, "sentence+column", 60)
	if !strings.Contains(got, "\n") {
		t.Fatalf("sentence+column must wrap a too-long sentence: %q", got)
	}
}

func TestWrap_VerbatimNotTouched(t *testing.T) {
	src := strings.Join([]string{
		"\\begin{verbatim}",
		"This is one long verbatim line that exceeds the column limit easily.",
		"\\end{verbatim}",
		"",
	}, "\n")
	got := runWrap(src, "sentence+column", 30)
	assert.Equal(t, src, got, "verbatim content must not be wrapped")
}

func TestWrap_SkipDirective(t *testing.T) {
	src := "Long sentence that exceeds the limit. Another sentence. % mrevdiff-fmt: skip\n"
	got := runWrap(src, "sentence", 200)
	assert.Equal(t, src, got, "skipped lines must not be wrapped")
}

func TestWrap_TrailingCommentLineLeftAlone(t *testing.T) {
	// Lines with a trailing inline comment are preserved verbatim — the
	// comment is the user's annotation of THIS physical line, so we don't
	// reflow it (would re-anchor the comment to a different sentence).
	src := "First sentence. Second sentence. % side note\n"
	got := runWrap(src, "sentence", 200)
	assert.Equal(t, src, got)
}

func TestWrap_Idempotent(t *testing.T) {
	src := "First sentence. Second sentence. Third sentence.\n"
	once := runWrap(src, "sentence", 100)
	twice := runWrap(once, "sentence", 100)
	assert.Equal(t, once, twice, "wrap must be idempotent")
}

// Regression: input that's already hand-wrapped at column 80 (mid-sentence
// breaks) must reflow to clean sentence-per-line, NOT produce additional
// breaks on top of the existing ones.
func TestWrap_RejoinsAlreadyWrappedParagraph(t *testing.T) {
	src := strings.Join([]string{
		"This is the first",
		"sentence. And here",
		"is the second one.",
		"",
	}, "\n")
	got := runWrap(src, "sentence", 200)
	want := strings.Join([]string{
		"This is the first sentence.",
		"And here is the second one.",
		"",
	}, "\n")
	assert.Equal(t, want, got)
}

// Regression: a paragraph with three lines and three sentences should
// reflow to three lines regardless of where the original breaks were.
func TestWrap_ParagraphReflowSentencePerLine(t *testing.T) {
	src := strings.Join([]string{
		"First. Second sentence here that wraps to two",
		"physical lines. Third one is also long enough to span multiple",
		"input lines comfortably.",
		"",
	}, "\n")
	got := runWrap(src, "sentence", 200)
	want := strings.Join([]string{
		"First.",
		"Second sentence here that wraps to two physical lines.",
		"Third one is also long enough to span multiple input lines comfortably.",
		"",
	}, "\n")
	assert.Equal(t, want, got)
}

// Paragraph reflow is idempotent: running wrap a second time on the
// reflowed output must not add or remove any breaks.
func TestWrap_ParagraphReflowIdempotent(t *testing.T) {
	src := strings.Join([]string{
		"This is the first",
		"sentence. And here",
		"is the second.",
		"",
	}, "\n")
	once := runWrap(src, "sentence", 200)
	twice := runWrap(once, "sentence", 200)
	assert.Equal(t, once, twice)
}

// Structural lines (\begin, \end, \section, \item, blank, \\) terminate
// paragraphs — surrounding prose must not be joined across them.
func TestWrap_StructuralLinesAreParagraphBreaks(t *testing.T) {
	src := strings.Join([]string{
		"Above the env.",
		"\\begin{theorem}",
		"Inside.",
		"\\end{theorem}",
		"Below the env.",
		"",
	}, "\n")
	got := runWrap(src, "sentence", 200)
	// Structure preserved; each body line is its own paragraph (single
	// sentence each), so no reflow occurs.
	assert.Equal(t, src, got)
}

// Command-only paragraph: top-matter \title{} \author{} \affil{} blocks must
// be left exactly as written — never joined with neighbours, never rebroken
// at the column limit, no matter how long.
func TestWrap_CommandOnlyParagraph_TopMatter(t *testing.T) {
	longAffil := "\\affil[c]{Department of Mathematics, University of Virginia, Charlottesville, VA 22904, USA}"
	src := strings.Join([]string{
		"\\begin{document}",
		"",
		"\\title{Computation and sampling for Schubert specializations}",
		"",
		"\\author[a]{David Anderson}",
		"\\author[b]{Greta Panova}",
		"\\author[c,1]{Leonid Petrov}",
		"",
		"\\affil[a]{Department of Mathematics, Ohio State University, Columbus, OH 43210, USA}",
		"\\affil[b]{Department of Mathematics, University of Southern California, Los Angeles, CA 90089, USA}",
		longAffil,
		"",
		"\\maketitle",
		"",
	}, "\n")
	got := runWrap(src, "sentence+column", 80)
	assert.Equal(t, src, got, "top-matter command-only lines must be preserved")
}

// Chained \author{}\address{}\email{} on one line — must stay as one line.
func TestWrap_CommandOnlyParagraph_Chain(t *testing.T) {
	src := "\\author{David Anderson} \\address{Ohio State University} \\email{anderson@osu.edu}\n"
	got := runWrap(src, "sentence+column", 80)
	assert.Equal(t, src, got, "command chain on one line must not be broken")
}

// Multi-line \significancestatement{...} with brace continuing across lines:
// every line in the continuation is structural; reflow must not touch it.
func TestWrap_CommandOnlyParagraph_MultiLineContinuation(t *testing.T) {
	src := strings.Join([]string{
		"\\significancestatement{Schubert specializations encode fundamental",
		"intersection counts in geometry. We disprove the conjecture and",
		"present new bounds.}",
		"",
		"After the statement.",
		"",
	}, "\n")
	got := runWrap(src, "sentence+column", 50)
	// All three lines of the significancestatement stay verbatim; the
	// "After the statement." line is normal prose.
	assert.Equal(t, src, got)
}

// \label{} after \section{} stays on its own line and does NOT merge with
// the following prose paragraph.
func TestWrap_CommandOnlyParagraph_LabelDoesNotMergeWithProse(t *testing.T) {
	src := strings.Join([]string{
		"\\section{Introduction}",
		"\\label{sec:intro}",
		"This is the first sentence. This is the second sentence.",
		"",
	}, "\n")
	got := runWrap(src, "sentence", 200)
	want := strings.Join([]string{
		"\\section{Introduction}",
		"\\label{sec:intro}",
		"This is the first sentence.",
		"This is the second sentence.",
		"",
	}, "\n")
	assert.Equal(t, want, got)
}

// Math-environment exclusion: a line whose content looks like a single
// command (e.g. \frac{a}{b}) but sits inside \begin{align}…\end{align}
// must NOT be classified as command-only — math rules handle it.
func TestWrap_CommandOnlyParagraph_NotInsideMath(t *testing.T) {
	src := strings.Join([]string{
		"\\begin{align}",
		"\\frac{a}{b}",
		"\\end{align}",
		"",
	}, "\n")
	got := runWrap(src, "sentence", 200)
	// The wrap rule should leave this alone (each line is structural by
	// existing rules: \begin/\end), and the new rule must not destabilise
	// that by treating \frac{a}{b} as a cmd-only-paragraph then merging.
	assert.Equal(t, src, got)
}

// \textbf{%…%} pattern is preserved (existing trailing-% heuristic still
// classifies each interior line as struct; the new rule must not break it).
func TestWrap_CommandOnlyParagraph_PercentFencedBoldPreserved(t *testing.T) {
	src := strings.Join([]string{
		"\\textbf{%",
		"%",
		"stuff stuff%",
		"%",
		"}",
		"",
	}, "\n")
	got := runWrap(src, "sentence+column", 40)
	assert.Equal(t, src, got, "%-fenced \\textbf block must be preserved verbatim")
}

// Multi-line \caption{} with hand-laid line breaks: the entire caption is
// command-only continuation, so internal lines are NOT joined and reflowed.
func TestWrap_CommandOnlyParagraph_CaptionPreserved(t *testing.T) {
	src := strings.Join([]string{
		"\\begin{figure}",
		"\\caption{\\textbf{Top:} The six tile types used in bumpless pipe dreams.",
		"\\textbf{Bottom:} A reduced bumpless pipe dream for $n=4$ corresponding",
		"to the permutation $w=2143$.}",
		"\\label{fig:bpd}",
		"\\end{figure}",
		"",
	}, "\n")
	got := runWrap(src, "sentence+column", 80)
	assert.Equal(t, src, got, "multi-line \\caption{} must be preserved")
}

// A bare prose line that happens to start with a command at column 0
// followed by trailing text is NOT command-only — it reflows normally.
func TestWrap_CommandOnlyParagraph_InlineCommandWithTrailingText(t *testing.T) {
	src := strings.Join([]string{
		"\\emph{Important}: this is a long sentence that should be reflowed.",
		"And here is another sentence that follows.",
		"",
	}, "\n")
	got := runWrap(src, "sentence", 200)
	want := strings.Join([]string{
		"\\emph{Important}: this is a long sentence that should be reflowed.",
		"And here is another sentence that follows.",
		"",
	}, "\n")
	assert.Equal(t, want, got)
}
