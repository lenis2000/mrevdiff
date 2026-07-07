package format

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func runSafeRule(t *testing.T, ruleID, src string) string {
	t.Helper()
	res := Apply([]byte(src), Options{Rules: []string{ruleID}})
	return string(res.Src)
}

func TestApplyItemPerLine(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "shared line splits",
			in:   "\\begin{itemize}\nfoo \\item bar\n\\end{itemize}\n",
			want: "\\begin{itemize}\nfoo \n\\item bar\n\\end{itemize}\n",
		},
		{
			name: "multiple items on same line all split",
			in:   "\\begin{itemize}\n\\item one \\item two \\item three\n\\end{itemize}\n",
			want: "\\begin{itemize}\n\\item one \n\\item two \n\\item three\n\\end{itemize}\n",
		},
		{
			name: "already on own line is no-op",
			in:   "\\begin{itemize}\n\\item one\n\\item two\n\\end{itemize}\n",
			want: "\\begin{itemize}\n\\item one\n\\item two\n\\end{itemize}\n",
		},
		{
			name: "item outside list env is left alone",
			in:   "stray \\item not in list\n",
			want: "stray \\item not in list\n",
		},
		{
			name: "item with bracket label",
			in:   "\\begin{description}\nLeading text \\item[Lemma] foo\n\\end{description}\n",
			want: "\\begin{description}\nLeading text \n\\item[Lemma] foo\n\\end{description}\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runSafeRule(t, "space.item-per-line", tc.in)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestApplyItemPerLineIdempotent(t *testing.T) {
	src := "\\begin{itemize}\nfoo \\item bar \\item baz\n\\end{itemize}\n"
	once := runSafeRule(t, "space.item-per-line", src)
	twice := runSafeRule(t, "space.item-per-line", once)
	assert.Equal(t, once, twice, "second pass must produce no further changes")
}

func TestApplyProofDelimPerLine(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "begin proof on shared line",
			in:   "Theorem 1. \\begin{proof} content\n\\end{proof}\n",
			want: "Theorem 1. \n\\begin{proof}\n content\n\\end{proof}\n",
		},
		{
			name: "begin proof with optional arg",
			in:   "Theorem 1. \\begin{proof}[Proof of Theorem 1] content\n\\end{proof}\n",
			want: "Theorem 1. \n\\begin{proof}[Proof of Theorem 1]\n content\n\\end{proof}\n",
		},
		{
			name: "end proof on shared line",
			in:   "\\begin{proof}\ncontent \\end{proof} trailing\n",
			want: "\\begin{proof}\ncontent \n\\end{proof}\n trailing\n",
		},
		{
			name: "already on own lines is no-op",
			in:   "\\begin{proof}\ncontent\n\\end{proof}\n",
			want: "\\begin{proof}\ncontent\n\\end{proof}\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runSafeRule(t, "space.proof-delim-per-line", tc.in)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestApplyProofDelimPerLineIdempotent(t *testing.T) {
	src := "Theorem. \\begin{proof}[Of T] body \\end{proof} text\n"
	once := runSafeRule(t, "space.proof-delim-per-line", src)
	twice := runSafeRule(t, "space.proof-delim-per-line", once)
	assert.Equal(t, once, twice, "second pass must produce no further changes")
}

func TestApplyDisplayDelimPerLine(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "bracket display on shared line",
			in:   "intro \\[ x = 1 \\] tail\n",
			want: "intro \n\\[\n x = 1 \n\\]\n tail\n",
		},
		{
			name: "equation env on shared line",
			in:   "intro \\begin{equation} x = 1 \\end{equation} tail\n",
			want: "intro \n\\begin{equation}\n x = 1 \n\\end{equation}\n tail\n",
		},
		{
			name: "align star env",
			in:   "p \\begin{align*}\nx &= y\n\\end{align*}\n",
			want: "p \n\\begin{align*}\nx &= y\n\\end{align*}\n",
		},
		{
			name: "already on own lines is no-op",
			in:   "\\[\nx\n\\]\n",
			want: "\\[\nx\n\\]\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runSafeRule(t, "space.display-delim-per-line", tc.in)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestApplyDisplayDelimPerLineIdempotent(t *testing.T) {
	src := "intro \\begin{align}\nx &= 1\n\\end{align} tail\n"
	once := runSafeRule(t, "space.display-delim-per-line", src)
	twice := runSafeRule(t, "space.display-delim-per-line", once)
	assert.Equal(t, once, twice, "second pass must produce no further changes")
}
