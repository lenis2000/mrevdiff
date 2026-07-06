package parser

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// findFirst returns the first block in doc matching the predicate, or nil.
func findFirst(doc *Document, pred func(*Block) bool) *Block {
	for _, b := range doc.Blocks {
		if b == doc.Root {
			continue
		}
		if pred(b) {
			return b
		}
	}
	return nil
}

// collect returns all non-root blocks matching pred, in parse order.
func collect(doc *Document, pred func(*Block) bool) []*Block {
	var out []*Block
	for _, b := range doc.Blocks {
		if b == doc.Root {
			continue
		}
		if pred(b) {
			out = append(out, b)
		}
	}
	return out
}

func TestParse_SampleFixture_TheoremEnvs(t *testing.T) {
	src := readFixture(t, "sample.tex")
	doc, err := Parse(src)
	require.NoError(t, err)

	// sample.tex declares three theorem envs: theorem, lemma (chained), remark (starred).
	require.Contains(t, doc.TheoremEnvs, "theorem")
	require.Contains(t, doc.TheoremEnvs, "lemma")
	require.Contains(t, doc.TheoremEnvs, "remark")

	assert.Equal(t, "Theorem", doc.TheoremEnvs["theorem"].Title)
	assert.Equal(t, "theorem", doc.TheoremEnvs["lemma"].Chain)
	assert.True(t, doc.TheoremEnvs["remark"].Starred)

	// Built-in defaults must still be present for envs the author didn't declare.
	assert.Contains(t, doc.TheoremEnvs, "proposition")
}

func TestParse_SampleFixture_TopLevelStructure(t *testing.T) {
	src := readFixture(t, "sample.tex")
	doc, err := Parse(src)
	require.NoError(t, err)

	// Top-level children: abstract + section "Main results".
	require.GreaterOrEqual(t, len(doc.Root.ChildIDs), 2)

	var kinds []Kind
	for _, cid := range doc.Root.ChildIDs {
		kinds = append(kinds, doc.ByID[cid].Kind)
	}
	assert.Contains(t, kinds, KindAbstract)
	assert.Contains(t, kinds, KindSection)
}

func TestParse_SampleFixture_SectionContents(t *testing.T) {
	src := readFixture(t, "sample.tex")
	doc, err := Parse(src)
	require.NoError(t, err)

	section := findFirst(doc, func(b *Block) bool { return b.Kind == KindSection })
	require.NotNil(t, section)
	assert.Equal(t, "Main results", section.Title)
	assert.Equal(t, "sec:main", section.Label)

	// Section should contain the theorem, proof, figure and remark as direct children.
	childKinds := map[Kind]int{}
	for _, cid := range section.ChildIDs {
		childKinds[doc.ByID[cid].Kind]++
	}
	assert.GreaterOrEqual(t, childKinds[KindTheoremLike], 2, "theorem + remark")
	assert.Equal(t, 1, childKinds[KindProof])
	assert.Equal(t, 1, childKinds[KindFigure])
}

func TestParse_SampleFixture_TheoremLabelAndDisplay(t *testing.T) {
	src := readFixture(t, "sample.tex")
	doc, err := Parse(src)
	require.NoError(t, err)

	thm := doc.ByLabel["thm:main"]
	require.NotNil(t, thm, "thm:main should be recorded in ByLabel")
	assert.Equal(t, KindTheoremLike, thm.Kind)
	assert.Equal(t, "theorem", thm.EnvName)

	// Theorem contains a display block (\[ … \]).
	var hasDisplay bool
	for _, cid := range thm.ChildIDs {
		if doc.ByID[cid].Kind == KindDisplay {
			hasDisplay = true
		}
	}
	assert.True(t, hasDisplay, "theorem should contain display math child")
}

func TestParse_SampleFixture_FigureLabel(t *testing.T) {
	src := readFixture(t, "sample.tex")
	doc, err := Parse(src)
	require.NoError(t, err)

	fig := doc.ByLabel["fig:diagram"]
	require.NotNil(t, fig)
	assert.Equal(t, KindFigure, fig.Kind)
	assert.Equal(t, "figure", fig.EnvName)

	// tikzpicture inside figure should be KindOther.
	var seenOther bool
	for _, cid := range fig.ChildIDs {
		c := doc.ByID[cid]
		if c.Kind == KindOther && c.EnvName == "tikzpicture" {
			seenOther = true
		}
	}
	assert.True(t, seenOther, "tikzpicture should be a KindOther child of the figure")
}

func TestParse_SampleFixture_ProofSteps(t *testing.T) {
	src := readFixture(t, "sample.tex")
	doc, err := Parse(src)
	require.NoError(t, err)

	proof := findFirst(doc, func(b *Block) bool { return b.Kind == KindProof })
	require.NotNil(t, proof)

	// sample.tex proof has three blank-line-separated runs; the third run
	// embeds an align display, which segmentProof now treats as a forced
	// boundary, so the last run splits into its leading text and the
	// align-anchored tail. Total: four steps.
	steps := collect(doc, func(b *Block) bool {
		return b.Kind == KindProofStep && b.ParentID == proof.ID
	})
	require.Len(t, steps, 4, "sample.tex proof should segment into four steps")

	// The align display should live under the step that begins on its own
	// line — the final step, the one created by the forced boundary.
	var alignStepID string
	for _, b := range doc.Blocks {
		if b.Kind == KindDisplay && b.EnvName == "align" {
			alignStepID = b.ParentID
		}
	}
	require.NotEmpty(t, alignStepID)
	assert.Equal(t, steps[3].ID, alignStepID, "align should be child of the last proof step")

	// Proof's direct children are exactly the ProofStep IDs.
	for _, cid := range proof.ChildIDs {
		assert.Equal(t, KindProofStep, doc.ByID[cid].Kind)
	}
}

func TestParse_SampleFixture_RemarkAndDollarDisplay(t *testing.T) {
	src := readFixture(t, "sample.tex")
	doc, err := Parse(src)
	require.NoError(t, err)

	var remark *Block
	for _, b := range doc.Blocks {
		if b.EnvName == "remark" {
			remark = b
		}
	}
	require.NotNil(t, remark)
	assert.Equal(t, KindTheoremLike, remark.Kind)

	// The $$ … $$ inside the remark should appear as a KindDisplay child.
	var hasDisplay bool
	for _, cid := range remark.ChildIDs {
		if doc.ByID[cid].Kind == KindDisplay {
			hasDisplay = true
		}
	}
	assert.True(t, hasDisplay, "remark should carry a dollar-display child")
}

func TestParse_SampleFixture_DocumentEnvIsTransparent(t *testing.T) {
	src := readFixture(t, "sample.tex")
	doc, err := Parse(src)
	require.NoError(t, err)

	for _, b := range doc.Blocks {
		assert.NotEqual(t, "document", b.EnvName,
			"\\begin{document} should be transparent and not produce a block")
	}
}

func TestParse_SampleFixture_SourceSliceNonEmpty(t *testing.T) {
	src := readFixture(t, "sample.tex")
	doc, err := Parse(src)
	require.NoError(t, err)

	// Any non-root block with explicit Start/End lines should carry a non-empty
	// source slice that contains its start-line text.
	for _, b := range doc.Blocks {
		if b == doc.Root {
			continue
		}
		if b.StartLine == 0 || b.EndLine == 0 {
			continue
		}
		assert.NotEmpty(t, b.Source, "block %s (%s) has empty Source", b.ID, b.Kind)
	}
}

func TestParse_Cases(t *testing.T) {
	cases := []struct {
		name  string
		src   string
		check func(t *testing.T, doc *Document)
	}{
		{
			name: "section then subsection nest",
			src: `\section{A}
\subsection{A.1}
\subsection{A.2}
\section{B}
`,
			check: func(t *testing.T, doc *Document) {
				tops := doc.Root.ChildIDs
				require.Len(t, tops, 2, "two top-level sections")
				a := doc.ByID[tops[0]]
				b := doc.ByID[tops[1]]
				assert.Equal(t, "A", a.Title)
				assert.Equal(t, "B", b.Title)
				assert.Len(t, a.ChildIDs, 2, "A has two subsections")
				for _, cid := range a.ChildIDs {
					assert.Equal(t, KindSection, doc.ByID[cid].Kind)
				}
				assert.Empty(t, b.ChildIDs)
			},
		},
		{
			name: "subsection closes when new section starts",
			src: `\section{A}
\subsection{A.1}
\section{B}
\subsection{B.1}
`,
			check: func(t *testing.T, doc *Document) {
				require.Len(t, doc.Root.ChildIDs, 2)
				a := doc.ByID[doc.Root.ChildIDs[0]]
				b := doc.ByID[doc.Root.ChildIDs[1]]
				assert.Equal(t, "A", a.Title)
				assert.Equal(t, "B", b.Title)
				require.Len(t, a.ChildIDs, 1)
				require.Len(t, b.ChildIDs, 1)
			},
		},
		{
			name: "theorem outside section attaches to root",
			src: `\begin{theorem}
\label{thm:x}
X.
\end{theorem}
`,
			check: func(t *testing.T, doc *Document) {
				require.Len(t, doc.Root.ChildIDs, 1)
				thm := doc.ByID[doc.Root.ChildIDs[0]]
				assert.Equal(t, KindTheoremLike, thm.Kind)
				assert.Equal(t, "thm:x", thm.Label)
				assert.Equal(t, thm, doc.ByLabel["thm:x"])
			},
		},
		{
			name: "unknown env becomes Other",
			src: `\begin{mytool}
content
\end{mytool}
`,
			check: func(t *testing.T, doc *Document) {
				require.Len(t, doc.Root.ChildIDs, 1)
				b := doc.ByID[doc.Root.ChildIDs[0]]
				assert.Equal(t, KindOther, b.Kind)
				assert.Equal(t, "mytool", b.EnvName)
			},
		},
		{
			name: "nested envs: figure > tikzpicture",
			src: `\begin{figure}
\label{fig:x}
\begin{tikzpicture}
\draw (0,0);
\end{tikzpicture}
\end{figure}
`,
			check: func(t *testing.T, doc *Document) {
				fig := doc.ByLabel["fig:x"]
				require.NotNil(t, fig)
				require.Len(t, fig.ChildIDs, 1)
				inner := doc.ByID[fig.ChildIDs[0]]
				assert.Equal(t, KindOther, inner.Kind)
				assert.Equal(t, "tikzpicture", inner.EnvName)
			},
		},
		{
			name: "display math outside proof attaches to enclosing block",
			src: `\section{A}
\label{sec:a}
\[ x=1 \]
`,
			check: func(t *testing.T, doc *Document) {
				sec := doc.ByLabel["sec:a"]
				require.NotNil(t, sec)
				require.Len(t, sec.ChildIDs, 1)
				d := doc.ByID[sec.ChildIDs[0]]
				assert.Equal(t, KindDisplay, d.Kind)
			},
		},
		{
			name: "auto-discovered theorem env is classified as TheoremLike",
			src: `\newtheorem{mythm}{My Theorem}
\begin{mythm}
\label{m:1}
stuff
\end{mythm}
`,
			check: func(t *testing.T, doc *Document) {
				_, ok := doc.TheoremEnvs["mythm"]
				assert.True(t, ok, "mythm should be auto-discovered")
				b := doc.ByLabel["m:1"]
				require.NotNil(t, b)
				assert.Equal(t, KindTheoremLike, b.Kind)
			},
		},
		{
			name: "bibliography env becomes one wrapper",
			src: `\begin{thebibliography}{99}
\bibitem{a} A.
\bibitem{b} B.
\end{thebibliography}
`,
			check: func(t *testing.T, doc *Document) {
				require.Len(t, doc.Root.ChildIDs, 1)
				b := doc.ByID[doc.Root.ChildIDs[0]]
				assert.Equal(t, KindBibliography, b.Kind)
			},
		},
		{
			name: "abstract is a top-level KindAbstract block",
			src: `\begin{document}
\begin{abstract}
Short abstract.
\end{abstract}
\end{document}
`,
			check: func(t *testing.T, doc *Document) {
				require.Len(t, doc.Root.ChildIDs, 1)
				b := doc.ByID[doc.Root.ChildIDs[0]]
				assert.Equal(t, KindAbstract, b.Kind)
				assert.Equal(t, "abstract", b.EnvName)
			},
		},
		{
			name: "proof step count: single paragraph is one step",
			src: `\begin{proof}
Just one paragraph of proof.
\end{proof}
`,
			check: func(t *testing.T, doc *Document) {
				proof := findFirst(doc, func(b *Block) bool { return b.Kind == KindProof })
				require.NotNil(t, proof)
				require.Len(t, proof.ChildIDs, 1)
				assert.Equal(t, KindProofStep, doc.ByID[proof.ChildIDs[0]].Kind)
			},
		},
		{
			name: "proof step count: two paragraphs with blank line",
			src: `\begin{proof}
First.

Second.
\end{proof}
`,
			check: func(t *testing.T, doc *Document) {
				proof := findFirst(doc, func(b *Block) bool { return b.Kind == KindProof })
				require.NotNil(t, proof)
				require.Len(t, proof.ChildIDs, 2)
				for _, cid := range proof.ChildIDs {
					assert.Equal(t, KindProofStep, doc.ByID[cid].Kind)
				}
			},
		},
		{
			name: "empty proof produces no steps",
			src: `\begin{proof}
\end{proof}
`,
			check: func(t *testing.T, doc *Document) {
				proof := findFirst(doc, func(b *Block) bool { return b.Kind == KindProof })
				require.NotNil(t, proof)
				assert.Empty(t, proof.ChildIDs)
			},
		},
		{
			name: "labels on enclosing block, not siblings",
			src: `\begin{theorem}
\label{t:1}
\end{theorem}
\begin{theorem}
\label{t:2}
\end{theorem}
`,
			check: func(t *testing.T, doc *Document) {
				t1 := doc.ByLabel["t:1"]
				t2 := doc.ByLabel["t:2"]
				require.NotNil(t, t1)
				require.NotNil(t, t2)
				assert.NotEqual(t, t1.ID, t2.ID)
				assert.Equal(t, "t:1", t1.Label)
				assert.Equal(t, "t:2", t2.Label)
			},
		},
		{
			name: "source slice covers begin to end",
			src: `\begin{theorem}
foo
\end{theorem}
`,
			check: func(t *testing.T, doc *Document) {
				thm := findFirst(doc, func(b *Block) bool { return b.Kind == KindTheoremLike })
				require.NotNil(t, thm)
				assert.True(t, strings.Contains(thm.Source, "\\begin{theorem}"))
				assert.True(t, strings.Contains(thm.Source, "\\end{theorem}"))
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := Parse([]byte(tc.src))
			require.NoError(t, err)
			tc.check(t, doc)
		})
	}
}

func TestKindString(t *testing.T) {
	for k := KindSection; k <= KindOther; k++ {
		assert.NotEmpty(t, k.String(), "kind %d has empty string", int(k))
	}
	assert.Equal(t, "Kind(42)", Kind(42).String())
}
