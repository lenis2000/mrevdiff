package format

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// runTildeRule runs prose.tilde-refs in isolation (with PDFFix enabled so
// the Tier-2 rule fires even without an explicit --rule filter).
func runTildeRule(t *testing.T, src string) string {
	t.Helper()
	res := Apply([]byte(src), Options{
		Rules: []string{"prose.tilde-refs"},
	})
	return string(res.Src)
}

func runTildeRuleWithHits(t *testing.T, src string) (string, []Hit) {
	t.Helper()
	res := Apply([]byte(src), Options{
		Rules: []string{"prose.tilde-refs"},
	})
	return string(res.Src), res.Hits
}

// --- Insertion cases --------------------------------------------------------

func TestTildeRefs_BasicCite(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "word space cite",
			in:   "see \\cite{foo}\n",
			want: "see~\\cite{foo}\n",
		},
		{
			name: "word space citep",
			in:   "result \\citep{bar}\n",
			want: "result~\\citep{bar}\n",
		},
		{
			name: "word space citet",
			in:   "proven by \\citet{baz}\n",
			want: "proven by~\\citet{baz}\n",
		},
		{
			name: "Theorem 1.2 cite",
			in:   "Theorem 1.2 \\cite{thm12}\n",
			want: "Theorem 1.2~\\cite{thm12}\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runTildeRule(t, tc.in)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestTildeRefs_BasicRef(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "see ref",
			in:   "see \\ref{fig:1}\n",
			want: "see~\\ref{fig:1}\n",
		},
		{
			name: "see eqref",
			in:   "see \\eqref{eq:main}\n",
			want: "see~\\eqref{eq:main}\n",
		},
		{
			name: "see cref",
			in:   "in \\cref{sec:intro}\n",
			want: "in~\\cref{sec:intro}\n",
		},
		{
			name: "see Cref",
			in:   "In \\Cref{sec:intro}\n",
			want: "In~\\Cref{sec:intro}\n",
		},
		{
			name: "see autoref",
			in:   "see \\autoref{tab:1}\n",
			want: "see~\\autoref{tab:1}\n",
		},
		{
			name: "see nameref",
			in:   "see \\nameref{sec:main}\n",
			want: "see~\\nameref{sec:main}\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runTildeRule(t, tc.in)
			assert.Equal(t, tc.want, got)
		})
	}
}

// --- Skip cases -------------------------------------------------------------

func TestTildeRefs_SkipCases(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "opening paren before cite",
			in:   "(\\cite{foo})\n",
			want: "(\\cite{foo})\n", // no change: '(' precedes \cite (space is absent anyway)
		},
		{
			name: "opening paren space cite",
			in:   "( \\cite{foo})\n",
			want: "( \\cite{foo})\n", // no change: '(' precedes the space
		},
		{
			name: "opening bracket space ref",
			in:   "[ \\ref{fig:1}]\n",
			want: "[ \\ref{fig:1}]\n", // no change: '[' precedes
		},
		{
			name: "already tilde",
			in:   "see~\\cite{foo}\n",
			want: "see~\\cite{foo}\n", // already correct
		},
		{
			name: "start of line cite",
			in:   "\\cite{foo} says\n",
			want: "\\cite{foo} says\n", // no space before \ at start
		},
		{
			name: "indented start of line",
			in:   "  \\cite{foo} says\n",
			want: "  \\cite{foo} says\n", // only whitespace before \ on the line
		},
		{
			name: "period before space cite",
			in:   "foo. \\cite{bar}\n",
			want: "foo. \\cite{bar}\n", // punctuation: skip
		},
		{
			name: "comma before space cite",
			in:   "foo, \\cite{bar}\n",
			want: "foo, \\cite{bar}\n", // punctuation: skip
		},
		{
			name: "colon before space ref",
			in:   "as: \\ref{fig:1}\n",
			want: "as: \\ref{fig:1}\n", // punctuation: skip
		},
		{
			name: "semicolon before space cite",
			in:   "see; \\cite{bar}\n",
			want: "see; \\cite{bar}\n", // punctuation: skip
		},
		{
			name: "backslash escape before space",
			in:   "\\ \\cite{foo}\n",
			want: "\\ \\cite{foo}\n", // backslash: skip
		},
		{
			name: "non-ref command not touched",
			in:   "see \\textbf{foo}\n",
			want: "see \\textbf{foo}\n", // \textbf not in the ref set
		},
		{
			name: "non-ref command label not touched",
			in:   "see \\label{foo}\n",
			want: "see \\label{foo}\n", // \label not in the default ref set
		},
		{
			name: "closing brace before space cite",
			in:   "} \\cite{bar}\n",
			want: "} \\cite{bar}\n", // '}' is skipped
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runTildeRule(t, tc.in)
			assert.Equal(t, tc.want, got)
		})
	}
}

// --- Inline math exclusion --------------------------------------------------

func TestTildeRefs_InsideMathNoChange(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{
			name: "dollar math",
			in:   "In $x \\ref{eq}$ text\n",
		},
		{
			name: "paren math",
			in:   "In \\(x \\ref{eq}\\) text\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runTildeRule(t, tc.in)
			assert.Equal(t, tc.in, got)
		})
	}
}

// --- Protected span no-rewrite -----------------------------------------------

func TestTildeRefs_ProtectedSpanNoRewrite(t *testing.T) {
	// \verb|...| creates a protected span; content inside should not be modified.
	in := "\\verb|see \\cite{foo}| text\n"
	got := runTildeRule(t, in)
	assert.Equal(t, in, got)
}

// --- Multiple replacements in one line ---------------------------------------

func TestTildeRefs_MultipleInOneLine(t *testing.T) {
	in := "see \\cite{a} and \\ref{b} together\n"
	want := "see~\\cite{a} and~\\ref{b} together\n"
	got := runTildeRule(t, in)
	assert.Equal(t, want, got)
}

// --- Idempotency -------------------------------------------------------------

func TestTildeRefs_Idempotent(t *testing.T) {
	src := "see \\cite{foo} and \\ref{bar}\n"
	once := runTildeRule(t, src)
	twice := runTildeRule(t, once)
	assert.Equal(t, once, twice, "second pass must produce no further changes")
}

// --- Hit metadata -----------------------------------------------------------

func TestTildeRefs_HitsCarryExpectedDiffSourceLines(t *testing.T) {
	src := "see \\cite{foo}\nand \\ref{bar}\n"
	_, hits := runTildeRuleWithHits(t, src)
	assert.Len(t, hits, 2)
	for _, h := range hits {
		assert.Equal(t, "prose.tilde-refs", h.RuleID)
		assert.NotEmpty(t, h.ExpectedDiffSourceLines)
	}
}

// --- Skip directive ----------------------------------------------------------

func TestTildeRefs_SkipDirective(t *testing.T) {
	// skip only masks its own line; use off/on to mask a block
	in := "% mrevdiff-fmt: off\nsee \\cite{foo}\n% mrevdiff-fmt: on\n"
	got := runTildeRule(t, in)
	assert.Equal(t, in, got, "lines inside off/on block should not be modified")
}

// --- Custom TildeRefs config -------------------------------------------------

func TestTildeRefs_CustomRefSet(t *testing.T) {
	// Only treat "ref" as a tilde command.
	src := "see \\cite{a} and \\ref{b}\n"
	res := Apply([]byte(src), Options{
		Rules: []string{"prose.tilde-refs"},
		Tilde: TildeOptions{Refs: []string{"ref"}},
	})
	got := string(res.Src)
	want := "see \\cite{a} and~\\ref{b}\n"
	assert.Equal(t, want, got, "only \\ref should get tilde with custom config")
}

// --- Multi-line source -------------------------------------------------------

func TestTildeRefs_MultiLine(t *testing.T) {
	in := "First \\cite{a}.\nSecond \\ref{b}.\nThird \\eqref{c}.\n"
	want := "First~\\cite{a}.\nSecond~\\ref{b}.\nThird~\\eqref{c}.\n"
	got := runTildeRule(t, in)
	assert.Equal(t, want, got)
}

// --- Control-word terminator (regression) -----------------------------------

// The space following a TeX control word like `\bf` is gobbled as a token
// separator — it is NOT typeset. Replacing it with `~` emits a visible
// non-breaking space, which is a rendering bug. Hit on PNAS preamble:
//
//	`{\bf \Cref{...}}` → `{\bf~\Cref{...}}` introduced a bold space.
func TestTildeRefs_ControlWordTerminator(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "{\\bf \\Cref}",
			in:   "see {\\bf \\Cref{conj:foo}} for context\n",
			want: "see {\\bf \\Cref{conj:foo}} for context\n",
		},
		{
			name: "\\bf at start of group",
			in:   "{\\bf \\cite{x}}\n",
			want: "{\\bf \\cite{x}}\n",
		},
		{
			name: "\\itshape \\ref",
			in:   "{\\itshape \\ref{sec}}\n",
			want: "{\\itshape \\ref{sec}}\n",
		},
		{
			name: "\\Large \\Cref",
			in:   "{\\Large \\Cref{thm}}\n",
			want: "{\\Large \\Cref{thm}}\n",
		},
		// A literal `\\` (line break) is followed by text `name` — the space
		// after `name` IS a regular inter-word space; tilde rule should fire.
		{
			name: "literal \\\\ then word, then \\cite",
			in:   "first line \\\\name \\cite{a}\n",
			want: "first line \\\\name~\\cite{a}\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := runTildeRule(t, c.in)
			assert.Equal(t, c.want, got)
		})
	}
}
