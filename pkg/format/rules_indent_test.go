package format

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func runIndent(src string, useTab bool, size int) string {
	res := Apply([]byte(src), Options{
		Indent: IndentOptions{Enabled: true, UseTab: useTab, Size: size},
	})
	return string(res.Src)
}

func TestIndent_DisabledIsNoop(t *testing.T) {
	src := "\\begin{document}\nhello\n\\end{document}\n"
	res := Apply([]byte(src), Options{}) // Enabled=false
	assert.Equal(t, src, string(res.Src))
}

func TestIndent_DocumentEnvIsNoIndentEnv(t *testing.T) {
	// Body of `document` must NOT be indented (no_indent_envs).
	src := "\\begin{document}\nhello\n\\end{document}\n"
	got := runIndent(src, true, 1)
	assert.Equal(t, src, got, "document body must remain at depth 0")
}

func TestIndent_TheoremEnvIsIndented(t *testing.T) {
	src := strings.Join([]string{
		"\\begin{document}",
		"\\begin{theorem}",
		"hello",
		"\\end{theorem}",
		"\\end{document}",
		"",
	}, "\n")
	got := runIndent(src, true, 1)
	want := strings.Join([]string{
		"\\begin{document}",
		"\\begin{theorem}",
		"\thello",
		"\\end{theorem}",
		"\\end{document}",
		"",
	}, "\n")
	assert.Equal(t, want, got)
}

func TestIndent_NestedEnvs(t *testing.T) {
	src := strings.Join([]string{
		"\\begin{document}",
		"\\begin{outer}",
		"a",
		"\\begin{inner}",
		"b",
		"\\end{inner}",
		"c",
		"\\end{outer}",
		"\\end{document}",
		"",
	}, "\n")
	got := runIndent(src, true, 1)
	want := strings.Join([]string{
		"\\begin{document}",
		"\\begin{outer}",
		"\ta",
		"\t\\begin{inner}",
		"\t\tb",
		"\t\\end{inner}",
		"\tc",
		"\\end{outer}",
		"\\end{document}",
		"",
	}, "\n")
	assert.Equal(t, want, got)
}

func TestIndent_Idempotent(t *testing.T) {
	src := strings.Join([]string{
		"\\begin{document}",
		"\\begin{theorem}",
		"a",
		"\\begin{proof}",
		"b",
		"\\end{proof}",
		"\\end{theorem}",
		"\\end{document}",
		"",
	}, "\n")
	once := runIndent(src, true, 1)
	twice := runIndent(once, true, 1)
	assert.Equal(t, once, twice, "indent must be idempotent")
}

func TestIndent_SkipDirectivePreservesLine(t *testing.T) {
	src := strings.Join([]string{
		"\\begin{document}",
		"\\begin{theorem}",
		"        hand-indented % mrevdiff-fmt: skip",
		"\\end{theorem}",
		"\\end{document}",
		"",
	}, "\n")
	got := runIndent(src, true, 1)
	assert.Contains(t, got, "        hand-indented % mrevdiff-fmt: skip", "skipped lines must keep their original leading whitespace")
}

func TestIndent_VerbatimContentsLeftAlone(t *testing.T) {
	src := strings.Join([]string{
		"\\begin{document}",
		"\\begin{verbatim}",
		"  preserve  whitespace  ",
		"\\end{verbatim}",
		"\\end{document}",
		"",
	}, "\n")
	got := runIndent(src, true, 1)
	assert.Contains(t, got, "  preserve  whitespace  ")
}

func TestIndent_BlankLinesNotIndented(t *testing.T) {
	src := strings.Join([]string{
		"\\begin{document}",
		"\\begin{theorem}",
		"hello",
		"",
		"world",
		"\\end{theorem}",
		"\\end{document}",
		"",
	}, "\n")
	got := runIndent(src, true, 1)
	// Blank line stays blank.
	assert.Contains(t, got, "\thello\n\n\tworld")
}

func TestIndent_SpacesMode(t *testing.T) {
	src := strings.Join([]string{
		"\\begin{document}",
		"\\begin{theorem}",
		"a",
		"\\end{theorem}",
		"\\end{document}",
		"",
	}, "\n")
	got := runIndent(src, false, 2) // 2 spaces per level
	assert.Contains(t, got, "  a")
	assert.NotContains(t, got, "\t")
}

func TestIndent_TabsModeSuppressesSpaceTabs(t *testing.T) {
	// With UseTab=true, the existing space.tabs rule must NOT eat the tabs
	// that space.indent writes — otherwise we get a tabs↔spaces ping-pong.
	src := strings.Join([]string{
		"\\begin{document}",
		"\\begin{theorem}",
		"a",
		"\\end{theorem}",
		"\\end{document}",
		"",
	}, "\n")
	res := Apply([]byte(src), Options{
		Indent: IndentOptions{Enabled: true, UseTab: true, Size: 1},
	})
	assert.Contains(t, string(res.Src), "\ta", "tabs mode must keep its tabs")
}

func runIndentWithRules(src string, useTab bool, size int, rules map[string]string) string {
	res := Apply([]byte(src), Options{
		Indent: IndentOptions{Enabled: true, UseTab: useTab, Size: size, Rules: rules},
	})
	return string(res.Src)
}

func TestIndent_PerEnvOverride_TwoSpaces(t *testing.T) {
	// tikzpicture uses 2-space indent while global is tab.
	src := strings.Join([]string{
		"\\begin{document}",
		"\\begin{tikzpicture}",
		"a",
		"\\end{tikzpicture}",
		"\\end{document}",
		"",
	}, "\n")
	got := runIndentWithRules(src, true, 1, map[string]string{
		"tikzpicture": "  ",
	})
	want := strings.Join([]string{
		"\\begin{document}",
		"\\begin{tikzpicture}",
		"  a",
		"\\end{tikzpicture}",
		"\\end{document}",
		"",
	}, "\n")
	assert.Equal(t, want, got)
}

func TestIndent_PerEnvOverride_NoIndent(t *testing.T) {
	// tikzcd with empty string = no indent (like document).
	src := strings.Join([]string{
		"\\begin{document}",
		"\\begin{theorem}",
		"\\begin{tikzcd}",
		"a",
		"\\end{tikzcd}",
		"\\end{theorem}",
		"\\end{document}",
		"",
	}, "\n")
	got := runIndentWithRules(src, true, 1, map[string]string{
		"tikzcd": "",
	})
	want := strings.Join([]string{
		"\\begin{document}",
		"\\begin{theorem}",
		"\t\\begin{tikzcd}",
		"\ta",
		"\t\\end{tikzcd}",
		"\\end{theorem}",
		"\\end{document}",
		"",
	}, "\n")
	assert.Equal(t, want, got)
}

func TestIndent_PerEnvOverride_DefaultFallback(t *testing.T) {
	// Only tikzpicture has a rule; theorem falls back to global tab indent.
	src := strings.Join([]string{
		"\\begin{document}",
		"\\begin{theorem}",
		"a",
		"\\end{theorem}",
		"\\end{document}",
		"",
	}, "\n")
	got := runIndentWithRules(src, true, 1, map[string]string{
		"tikzpicture": "  ",
	})
	want := strings.Join([]string{
		"\\begin{document}",
		"\\begin{theorem}",
		"\ta",
		"\\end{theorem}",
		"\\end{document}",
		"",
	}, "\n")
	assert.Equal(t, want, got, "envs not in Rules should fall back to global indent")
}

func TestIndent_PerEnvOverride_NestedMixed(t *testing.T) {
	// theorem (global tab) -> tikzpicture (2-space per rule): nested indent
	// should be tab from theorem + 2-space from tikzpicture.
	src := strings.Join([]string{
		"\\begin{document}",
		"\\begin{theorem}",
		"text",
		"\\begin{tikzpicture}",
		"draw code",
		"\\end{tikzpicture}",
		"more text",
		"\\end{theorem}",
		"\\end{document}",
		"",
	}, "\n")
	got := runIndentWithRules(src, true, 1, map[string]string{
		"tikzpicture": "  ",
	})
	want := strings.Join([]string{
		"\\begin{document}",
		"\\begin{theorem}",
		"\ttext",
		"\t\\begin{tikzpicture}",
		"\t  draw code",
		"\t\\end{tikzpicture}",
		"\tmore text",
		"\\end{theorem}",
		"\\end{document}",
		"",
	}, "\n")
	assert.Equal(t, want, got)
}

func TestIndent_PerEnvOverride_Idempotent(t *testing.T) {
	src := strings.Join([]string{
		"\\begin{document}",
		"\\begin{theorem}",
		"a",
		"\\begin{tikzpicture}",
		"b",
		"\\end{tikzpicture}",
		"\\end{theorem}",
		"\\end{document}",
		"",
	}, "\n")
	rules := map[string]string{"tikzpicture": "  "}
	once := runIndentWithRules(src, true, 1, rules)
	twice := runIndentWithRules(once, true, 1, rules)
	assert.Equal(t, once, twice, "per-env indent must be idempotent")
}

func TestIndent_PerEnvOverride_DeeplyNested(t *testing.T) {
	// Three levels: theorem (tab) -> tikzpicture (2-space) -> scope (tab, default).
	src := strings.Join([]string{
		"\\begin{document}",
		"\\begin{theorem}",
		"\\begin{tikzpicture}",
		"\\begin{scope}",
		"deep",
		"\\end{scope}",
		"\\end{tikzpicture}",
		"\\end{theorem}",
		"\\end{document}",
		"",
	}, "\n")
	got := runIndentWithRules(src, true, 1, map[string]string{
		"tikzpicture": "  ",
	})
	want := strings.Join([]string{
		"\\begin{document}",
		"\\begin{theorem}",
		"\t\\begin{tikzpicture}",
		"\t  \\begin{scope}",
		"\t  \tdeep",
		"\t  \\end{scope}",
		"\t\\end{tikzpicture}",
		"\\end{theorem}",
		"\\end{document}",
		"",
	}, "\n")
	assert.Equal(t, want, got)
}

// ---------------------------------------------------------------------------
// Same-line \begin and \end
// ---------------------------------------------------------------------------

func TestIndent_SameLineBeginEnd(t *testing.T) {
	// \begin{theorem}\end{theorem} on same line should net to zero:
	// the next line must NOT be indented.
	src := strings.Join([]string{
		"\\begin{document}",
		"\\begin{theorem}\\end{theorem}",
		"next line",
		"\\end{document}",
		"",
	}, "\n")
	got := runIndent(src, true, 1)
	want := strings.Join([]string{
		"\\begin{document}",
		"\\begin{theorem}\\end{theorem}",
		"next line",
		"\\end{document}",
		"",
	}, "\n")
	assert.Equal(t, want, got, "same-line begin/end must not leak indent to next line")
}

func TestIndent_SameLineEndThenBegin(t *testing.T) {
	// \end{A}\begin{B} on same line: A closes, B opens. Next line
	// should be indented for B only.
	// Use Rules filter to run ONLY space.indent (other rules may split the line).
	src := strings.Join([]string{
		"\\begin{document}",
		"\\begin{theorem}",
		"body of theorem",
		"\\end{theorem}\\begin{proof}",
		"body of proof",
		"\\end{proof}",
		"\\end{document}",
		"",
	}, "\n")
	res := Apply([]byte(src), Options{
		Rules:  []string{"space.indent"},
		Indent: IndentOptions{Enabled: true, UseTab: true, Size: 1},
	})
	got := string(res.Src)
	want := strings.Join([]string{
		"\\begin{document}",
		"\\begin{theorem}",
		"\tbody of theorem",
		"\\end{theorem}\\begin{proof}",
		"\tbody of proof",
		"\\end{proof}",
		"\\end{document}",
		"",
	}, "\n")
	assert.Equal(t, want, got, "end-then-begin on same line must transition correctly")
}

func TestIndent_SameLineBeginEnd_Idempotent(t *testing.T) {
	src := strings.Join([]string{
		"\\begin{document}",
		"\\begin{theorem}\\end{theorem}",
		"after",
		"\\end{document}",
		"",
	}, "\n")
	once := runIndent(src, true, 1)
	twice := runIndent(once, true, 1)
	assert.Equal(t, once, twice, "same-line begin/end indent must be idempotent")
}
