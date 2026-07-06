package parser

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseBBL_SampleFixture(t *testing.T) {
	entries := ParseBBL(readFixture(t, "sample.bbl"))
	require.Len(t, entries, 2)

	assert.Equal(t, "GKP1994", entries[0].Key)
	assert.Equal(t, "", entries[0].Display)
	assert.Contains(t, entries[0].Text, "Graham")
	assert.Contains(t, entries[0].Authors, "Graham")
	assert.Equal(t, "Concrete Mathematics", entries[0].Title)

	assert.Equal(t, "Stanley2011", entries[1].Key)
	assert.Equal(t, "Stan11", entries[1].Display)
	assert.Contains(t, entries[1].Authors, "Stanley")
	assert.Equal(t, "Enumerative Combinatorics", entries[1].Title)
}

func TestParseBBL_EdgeCases(t *testing.T) {
	cases := []struct {
		name      string
		src       string
		wantKeys  []string
		wantFirst *BibEntry
	}{
		{
			name:     "empty input",
			src:      "",
			wantKeys: nil,
		},
		{
			name:     "no bibitems",
			src:      "\\begin{thebibliography}{1}\n\\end{thebibliography}\n",
			wantKeys: nil,
		},
		{
			name: "single entry no newblock",
			src: "\\begin{thebibliography}{1}\n" +
				"\\bibitem{k1} Author, Title.\n" +
				"\\end{thebibliography}\n",
			wantKeys: []string{"k1"},
			wantFirst: &BibEntry{
				Key:     "k1",
				Authors: "Author, Title",
				Text:    "Author, Title.",
			},
		},
		{
			name: "display label then key",
			src: "\\bibitem[DL]{k2}\n" +
				"An author. \\newblock {\\em Title.} Publisher, 2001.\n",
			wantKeys: []string{"k2"},
			wantFirst: &BibEntry{
				Key:     "k2",
				Display: "DL",
				Title:   "Title",
				Authors: "An author",
			},
		},
		{
			name: "content past end{thebibliography} is ignored",
			src: "\\bibitem{k3} X.\n" +
				"\\end{thebibliography}\n" +
				"\\bibitem{ignored} should not appear\n",
			wantKeys: []string{"k3"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseBBL([]byte(tc.src))
			var keys []string
			for _, e := range got {
				keys = append(keys, e.Key)
			}
			assert.Equal(t, tc.wantKeys, keys)
			if tc.wantFirst != nil {
				require.NotEmpty(t, got)
				first := got[0]
				assert.Equal(t, tc.wantFirst.Key, first.Key)
				if tc.wantFirst.Display != "" {
					assert.Equal(t, tc.wantFirst.Display, first.Display)
				}
				if tc.wantFirst.Authors != "" {
					assert.Equal(t, tc.wantFirst.Authors, first.Authors)
				}
				if tc.wantFirst.Title != "" {
					assert.Equal(t, tc.wantFirst.Title, first.Title)
				}
			}
		})
	}
}

func TestLoadBBL_MissingFile(t *testing.T) {
	entries, err := LoadBBL(filepath.Join("..", "..", "testdata", "does-not-exist.bbl"))
	require.NoError(t, err, "missing .bbl must not be a hard error")
	assert.Nil(t, entries)
}

func TestApplyBBL_ResolvesCitesAndBuildsEntries(t *testing.T) {
	src := readFixture(t, "sample.tex")
	doc, err := Parse(src)
	require.NoError(t, err)

	// Cite refs from sample.tex start life unresolved (see refs.go).
	var cites []Ref
	for _, b := range doc.Blocks {
		for _, r := range b.RefsOut {
			if r.Kind == "cite" {
				cites = append(cites, r)
			}
		}
	}
	require.NotEmpty(t, cites, "sample.tex carries at least one \\cite")
	for _, r := range cites {
		assert.False(t, r.Resolved)
	}

	entries := ParseBBL(readFixture(t, "sample.bbl"))
	added := ApplyBBL(doc, entries)
	// sample.tex has no \begin{thebibliography} wrapper, so no child blocks
	// get appended — but cite resolution and BibEntries still work.
	assert.Equal(t, 0, added)

	// Now cite refs whose keys match entries should flip to resolved.
	for _, b := range doc.Blocks {
		for _, r := range b.RefsOut {
			if r.Kind != "cite" {
				continue
			}
			assert.True(t, r.Resolved, "cite %q should resolve via .bbl", r.Target)
		}
	}

	// BibEntries map populated.
	require.Contains(t, doc.BibEntries, "GKP1994")
	require.Contains(t, doc.BibEntries, "Stanley2011")

	// Entries appear under the bibliography wrapper, if present. (sample.tex
	// has no \begin{thebibliography} — so no wrapper and added should be 0
	// for that case.)
	// Re-run on a doc that DOES have a wrapper:
	withBib := []byte("\\begin{thebibliography}{2}\n" +
		"\\bibitem{a} Aaron, Art.\n" +
		"\\bibitem{b} Bea, Book.\n" +
		"\\end{thebibliography}\n")
	doc2, err := Parse(withBib)
	require.NoError(t, err)
	n := ApplyBBL(doc2, ParseBBL(withBib))
	assert.Equal(t, 2, n)

	var wrapper *Block
	for _, b := range doc2.Blocks {
		if b.Kind == KindBibliography && b.EnvName == "thebibliography" {
			wrapper = b
		}
	}
	require.NotNil(t, wrapper)
	require.Len(t, wrapper.ChildIDs, 2)
	for _, cid := range wrapper.ChildIDs {
		c := doc2.ByID[cid]
		assert.Equal(t, KindBibliography, c.Kind)
		assert.NotEmpty(t, c.Label)
	}
}

func TestApplyBBL_NoWrapperStillPopulatesEntries(t *testing.T) {
	doc, err := Parse([]byte(`\section{S}`))
	require.NoError(t, err)

	entries := []BibEntry{{Key: "x", Text: "X authors, X title."}}
	n := ApplyBBL(doc, entries)
	assert.Equal(t, 0, n, "no wrapper → no child blocks added")
	require.Contains(t, doc.BibEntries, "x")
}

func TestApplyBBL_NilSafety(t *testing.T) {
	assert.Equal(t, 0, ApplyBBL(nil, nil))
}
