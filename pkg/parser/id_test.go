package parser

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssignStableIDs_LabelBecomesID(t *testing.T) {
	src := []byte(`\section{Main}
\label{sec:m}
\begin{theorem}
\label{thm:x}
X.
\end{theorem}
`)
	doc, err := Parse(src)
	require.NoError(t, err)

	sec := doc.ByLabel["sec:m"]
	require.NotNil(t, sec)
	assert.Equal(t, "sec:m", sec.ID)

	thm := doc.ByLabel["thm:x"]
	require.NotNil(t, thm)
	assert.Equal(t, "thm:x", thm.ID)

	// ByID is rebuilt with the new IDs.
	assert.Same(t, sec, doc.ByID["sec:m"])
	assert.Same(t, thm, doc.ByID["thm:x"])
}

func TestAssignStableIDs_ProofInheritsTheoremLabel(t *testing.T) {
	src := []byte(`\begin{theorem}
\label{thm:main}
X.
\end{theorem}
\begin{proof}
Step one.

Step two.
\end{proof}
`)
	doc, err := Parse(src)
	require.NoError(t, err)

	thm := doc.ByLabel["thm:main"]
	require.NotNil(t, thm)

	proof := findFirst(doc, func(b *Block) bool { return b.Kind == KindProof })
	require.NotNil(t, proof)
	assert.Equal(t, "thm:main.proof", proof.ID)

	steps := collect(doc, func(b *Block) bool { return b.Kind == KindProofStep })
	require.Len(t, steps, 2)
	assert.Equal(t, "thm:main.proof.step.1", steps[0].ID)
	assert.Equal(t, "thm:main.proof.step.2", steps[1].ID)

	// ParentID / ChildIDs rewritten consistently.
	assert.Equal(t, "thm:main.proof", steps[0].ParentID)
	assert.Contains(t, proof.ChildIDs, "thm:main.proof.step.1")
	assert.Contains(t, proof.ChildIDs, "thm:main.proof.step.2")
}

func TestAssignStableIDs_UnlabeledBlockHasHashedID(t *testing.T) {
	src := []byte(`\begin{theorem}
Untitled.
\end{theorem}
`)
	doc, err := Parse(src)
	require.NoError(t, err)

	thm := findFirst(doc, func(b *Block) bool { return b.Kind == KindTheoremLike })
	require.NotNil(t, thm)

	// Format: "<slug>-<siblingIdx>-<8hex>"
	re := regexp.MustCompile(`^[a-z0-9-]+-\d+-[0-9a-f]{8}$`)
	assert.True(t, re.MatchString(thm.ID), "ID %q should match slug-idx-hash8 pattern", thm.ID)
	assert.True(t, strings.HasPrefix(thm.ID, "theorem-"), "slug should come from envname")
}

func TestAssignStableIDs_StabilityUnderLineShifts(t *testing.T) {
	body := `\begin{theorem}
Untitled theorem body.
\end{theorem}
`
	docA, err := Parse([]byte(body))
	require.NoError(t, err)
	thmA := findFirst(docA, func(b *Block) bool { return b.Kind == KindTheoremLike })
	require.NotNil(t, thmA)

	shifted := "\n\n% padding\n\n" + body
	docB, err := Parse([]byte(shifted))
	require.NoError(t, err)
	thmB := findFirst(docB, func(b *Block) bool { return b.Kind == KindTheoremLike })
	require.NotNil(t, thmB)

	assert.Equal(t, thmA.ID, thmB.ID,
		"content-derived ID must stay the same when the block's line number shifts")
}

func TestAssignStableIDs_UniquenessAmongHashedSiblings(t *testing.T) {
	// Two identical unknown-env blocks side by side — sibling index must
	// differ so IDs don't collide.
	src := []byte(`\begin{mytool}
same
\end{mytool}
\begin{mytool}
same
\end{mytool}
`)
	doc, err := Parse(src)
	require.NoError(t, err)

	require.Len(t, doc.Root.ChildIDs, 2)
	a := doc.ByID[doc.Root.ChildIDs[0]]
	b := doc.ByID[doc.Root.ChildIDs[1]]
	assert.NotEqual(t, a.ID, b.ID, "sibling index must disambiguate otherwise-identical blocks")
}

func TestAssignStableIDs_SectionSlug(t *testing.T) {
	src := []byte(`\section{Main Results!}
content
`)
	doc, err := Parse(src)
	require.NoError(t, err)

	require.Len(t, doc.Root.ChildIDs, 1)
	sec := doc.ByID[doc.Root.ChildIDs[0]]
	assert.Equal(t, KindSection, sec.Kind)
	assert.True(t, strings.HasPrefix(sec.ID, "main-results-"),
		"section slug should come from title, got %q", sec.ID)
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Main Results":     "main-results",
		"Main  Results!":   "main-results",
		"Proof Of Theorem": "proof-of-theorem",
		"":                 "",
		"---":              "",
		"A_B_C":            "a-b-c",
	}
	for in, want := range cases {
		assert.Equal(t, want, slugify(in), "slugify(%q)", in)
	}
}

func TestAssignStableIDs_SampleFixture_KnownIDs(t *testing.T) {
	src := readFixture(t, "sample.tex")
	doc, err := Parse(src)
	require.NoError(t, err)

	// Labeled blocks should have their label as ID.
	for _, lbl := range []string{"sec:main", "thm:main", "fig:diagram"} {
		b, ok := doc.ByID[lbl]
		require.True(t, ok, "labeled block %s should appear in ByID", lbl)
		assert.Equal(t, lbl, b.ID)
	}

	// The proof following thm:main should be "thm:main.proof".
	proof, ok := doc.ByID["thm:main.proof"]
	require.True(t, ok, "proof after thm:main should get dotted ID")
	assert.Equal(t, KindProof, proof.Kind)

	// Three proof steps.
	for _, n := range []string{"thm:main.proof.step.1", "thm:main.proof.step.2", "thm:main.proof.step.3"} {
		b, ok := doc.ByID[n]
		require.True(t, ok, "proof step %s should exist", n)
		assert.Equal(t, KindProofStep, b.Kind)
	}
}
