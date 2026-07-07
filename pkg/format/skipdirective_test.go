package format

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildSkipMask_Skip(t *testing.T) {
	src := []byte(strings.Join([]string{
		"line 1",
		"line 2  % mrevdiff-fmt: skip",
		"line 3",
	}, "\n"))
	mask := BuildSkipMask(src)
	assert.Len(t, mask, 4) // 1-indexed; 3 lines + sentinel
	assert.False(t, mask[1])
	assert.True(t, mask[2])
	assert.False(t, mask[3])
}

func TestBuildSkipMask_OffOn(t *testing.T) {
	src := []byte(strings.Join([]string{
		"line 1",
		"% mrevdiff-fmt: off",
		"line 3",
		"line 4",
		"% mrevdiff-fmt: on",
		"line 6",
	}, "\n"))
	mask := BuildSkipMask(src)
	assert.False(t, mask[1])
	assert.True(t, mask[2])  // off line itself is masked
	assert.True(t, mask[3])  // body
	assert.True(t, mask[4])  // body
	assert.True(t, mask[5])  // on line itself is masked
	assert.False(t, mask[6]) // back to normal
}

func TestBuildSkipMask_NoDirective(t *testing.T) {
	src := []byte("hello\nworld\n")
	mask := BuildSkipMask(src)
	for i, m := range mask {
		assert.Falsef(t, m, "mask[%d] expected false", i)
	}
}

func TestBuildSkipMask_EscapedPercent(t *testing.T) {
	// `\%` is not a real comment, so the directive must NOT trigger.
	src := []byte(`100\% mrevdiff-fmt: skip` + "\n")
	mask := BuildSkipMask(src)
	assert.False(t, mask[1], "escaped percent must not start a comment")
}

func TestBuildSkipMask_TripleComment(t *testing.T) {
	// %%% mrevdiff-fmt: off should still be recognised.
	src := []byte("%%% mrevdiff-fmt: off\nbody\n%%% mrevdiff-fmt: on\n")
	mask := BuildSkipMask(src)
	assert.True(t, mask[1])
	assert.True(t, mask[2])
	assert.True(t, mask[3])
}

func TestBuildSkipMask_CaseInsensitive(t *testing.T) {
	src := []byte("% MREVDIFF-FMT: SKIP\nbody\n")
	mask := BuildSkipMask(src)
	assert.True(t, mask[1])
	assert.False(t, mask[2])
}

func TestBuildSkipMask_TrailingTextRefuses(t *testing.T) {
	// Directive must end the line; trailing junk should disable it.
	src := []byte("% mrevdiff-fmt: skip and also other stuff\nbody\n")
	mask := BuildSkipMask(src)
	assert.False(t, mask[1])
}

func TestSkipDirective_SilencesTrailingRule(t *testing.T) {
	// Trailing whitespace on a `skip` line must survive.
	src := []byte("clean line\nkeep me   % mrevdiff-fmt: skip\nrewrite me   \n")
	res := Apply(src, Options{})
	got := string(res.Src)
	// Line 2 retains its trailing spaces (the "skip" comment line).
	assert.Contains(t, got, "keep me   % mrevdiff-fmt: skip")
	// Line 3 is rewritten.
	assert.Contains(t, got, "rewrite me\n")
}

func TestSkipDirective_OffOnBlock(t *testing.T) {
	src := []byte(strings.Join([]string{
		"clean   ",
		"% mrevdiff-fmt: off",
		"keep me   ",
		"keep me too\t",
		"% mrevdiff-fmt: on",
		"trim me   ",
		"",
	}, "\n"))
	res := Apply(src, Options{})
	got := string(res.Src)
	assert.Contains(t, got, "keep me   \n")
	assert.Contains(t, got, "keep me too\t\n")
	assert.Contains(t, got, "trim me\n")
	assert.Contains(t, got, "clean\n")
}

func TestSkipDirective_FiltersDiags(t *testing.T) {
	// Tier-3 diagnostics on a masked line must not appear.
	src := []byte(strings.Join([]string{
		`\documentclass{amsart}`,
		`\begin{document}`,
		`See \ref{nope}. % mrevdiff-fmt: skip`,
		`Also \ref{nope2}.`,
		`\end{document}`,
		``,
	}, "\n"))
	res := Apply(src, Options{Diag: true})
	var lines []int
	for _, d := range res.Diags {
		if d.RuleID == "lint.ref-undefined" {
			lines = append(lines, d.Line)
		}
	}
	// Only the un-skipped line 4 should produce a diagnostic.
	assert.NotContains(t, lines, 3, "skipped line must not yield a diagnostic")
	assert.Contains(t, lines, 4, "un-skipped line must yield a diagnostic")
}

func TestBuildSkipMask_Preamble(t *testing.T) {
	src := []byte(strings.Join([]string{
		`\documentclass{article}`,
		`\usepackage{amsmath}`,
		`\newcommand{\foo}{bar}`,
		`\begin{document}`,
		`Body line.`,
		`\end{document}`,
	}, "\n"))
	mask := BuildSkipMask(src)
	assert.True(t, mask[1], "documentclass must be masked (preamble)")
	assert.True(t, mask[2], "usepackage must be masked (preamble)")
	assert.True(t, mask[3], "newcommand must be masked (preamble)")
	assert.True(t, mask[4], `\begin{document} line itself must be masked`)
	assert.False(t, mask[5], "body must not be masked")
	assert.False(t, mask[6], `\end{document} must not be masked`)
}

func TestBuildSkipMask_NoBeginDocument(t *testing.T) {
	// Fragments without \begin{document} (e.g. \input'd files) must not
	// have everything masked — only directive-scoped masking applies.
	src := []byte(strings.Join([]string{
		`Section body.`,
		`Another line.`,
	}, "\n"))
	mask := BuildSkipMask(src)
	assert.False(t, mask[1])
	assert.False(t, mask[2])
}

func TestBuildSkipMask_BeginDocumentInComment(t *testing.T) {
	// A commented-out \begin{document} must not be treated as the boundary.
	src := []byte(strings.Join([]string{
		`\documentclass{article}`,
		`% \begin{document} (commented out)`,
		`\begin{document}`,
		`Body.`,
	}, "\n"))
	mask := BuildSkipMask(src)
	assert.True(t, mask[1])
	assert.True(t, mask[2])
	assert.True(t, mask[3], "real \\begin{document} is the boundary")
	assert.False(t, mask[4])
}
