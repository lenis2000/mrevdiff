package parser

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseAux_SampleFixture(t *testing.T) {
	data := readFixture(t, "sample.aux")
	entries := ParseAux(data)

	require.Contains(t, entries, "sec:main")
	assert.Equal(t, "1", entries["sec:main"].Number)
	assert.Equal(t, "1", entries["sec:main"].Page)

	require.Contains(t, entries, "thm:main")
	assert.Equal(t, "1.1", entries["thm:main"].Number)
	assert.Equal(t, "1", entries["thm:main"].Page)

	require.Contains(t, entries, "fig:diagram")
	assert.Equal(t, "1", entries["fig:diagram"].Number)
	assert.Equal(t, "2", entries["fig:diagram"].Page)

	// @cref internal helper records must not leak into the user-visible map.
	assert.NotContains(t, entries, "thm:main@cref")
	assert.NotContains(t, entries, "fig:diagram@cref")
}

func TestParseAux_Cases(t *testing.T) {
	cases := []struct {
		name   string
		src    string
		expect map[string]AuxEntry
	}{
		{
			name:   "empty",
			src:    "",
			expect: map[string]AuxEntry{},
		},
		{
			name:   "comment and unknown records ignored",
			src:    "% a comment\n\\relax\n\\citation{foo}\n",
			expect: map[string]AuxEntry{},
		},
		{
			name: "plain newlabel",
			src:  "\\newlabel{eq:1}{{2.3}{5}}\n",
			expect: map[string]AuxEntry{
				"eq:1": {Label: "eq:1", Number: "2.3", Page: "5"},
			},
		},
		{
			name: "labels with nested braces in extra args",
			src:  "\\newlabel{lem:x}{{4.2}{7}{Some title with {braces}}{lemma.4.2}{}}\n",
			expect: map[string]AuxEntry{
				"lem:x": {Label: "lem:x", Number: "4.2", Page: "7"},
			},
		},
		{
			name: "malformed newlabel skipped",
			src:  "\\newlabel{broken\n\\newlabel{ok}{{1}{1}}\n",
			expect: map[string]AuxEntry{
				"ok": {Label: "ok", Number: "1", Page: "1"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseAux([]byte(tc.src))
			assert.Equal(t, tc.expect, got)
		})
	}
}

func TestLoadAux_MissingFile(t *testing.T) {
	entries, err := LoadAux(filepath.Join("..", "..", "testdata", "does-not-exist.aux"))
	require.NoError(t, err, "missing .aux must not be a hard error")
	assert.Empty(t, entries)
}

func TestApplyAux_SetsBlockNumbers(t *testing.T) {
	src := readFixture(t, "sample.tex")
	doc, err := Parse(src)
	require.NoError(t, err)

	// Before enrichment, parser-derived Number is empty.
	thm := doc.ByLabel["thm:main"]
	require.NotNil(t, thm)
	assert.Empty(t, thm.Number)

	entries := ParseAux(readFixture(t, "sample.aux"))
	n := ApplyAux(doc, entries)

	assert.GreaterOrEqual(t, n, 3, "section, theorem, figure should be enriched")
	assert.Equal(t, "1.1", thm.Number)

	fig := doc.ByLabel["fig:diagram"]
	require.NotNil(t, fig)
	assert.Equal(t, "1", fig.Number)

	sec := doc.ByLabel["sec:main"]
	require.NotNil(t, sec)
	assert.Equal(t, "1", sec.Number)
}

func TestApplyAux_Empty(t *testing.T) {
	src := readFixture(t, "sample.tex")
	doc, err := Parse(src)
	require.NoError(t, err)

	assert.Equal(t, 0, ApplyAux(doc, nil))
	assert.Equal(t, 0, ApplyAux(doc, map[string]AuxEntry{}))
}

func TestReadBracedGroup(t *testing.T) {
	cases := []struct {
		in   string
		want string
		rest string
		ok   bool
	}{
		{"{abc}", "abc", "", true},
		{"  { hello world }rest", " hello world ", "rest", true},
		{"{a{b}c}x", "a{b}c", "x", true},
		{"no brace", "", "no brace", false},
		{"{unterminated", "", "{unterminated", false},
		{`{a\}b}c`, `a\}b`, "c", true},
	}
	for _, tc := range cases {
		got, rest, ok := readBracedGroup(tc.in)
		assert.Equal(t, tc.ok, ok, "ok for %q", tc.in)
		if tc.ok {
			assert.Equal(t, tc.want, got, "content for %q", tc.in)
			assert.Equal(t, tc.rest, rest, "rest for %q", tc.in)
		}
	}
}
