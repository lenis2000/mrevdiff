package parser

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// findBlockByLabel returns the block with the given label, or nil.
func findBlockByLabel(doc *Document, label string) *Block {
	return doc.ByLabel[label]
}

func TestResolveRefs_SampleFixture_AbstractRefResolved(t *testing.T) {
	src := readFixture(t, "sample.tex")
	doc, err := Parse(src)
	require.NoError(t, err)

	abs := findFirst(doc, func(b *Block) bool { return b.Kind == KindAbstract })
	require.NotNil(t, abs)

	require.Len(t, abs.RefsOut, 1, "abstract should have a single \\ref{sec:main}")
	r := abs.RefsOut[0]
	assert.Equal(t, "ref", r.Kind)
	assert.Equal(t, "sec:main", r.Target)
	assert.True(t, r.Resolved, "forward ref to sec:main should resolve")
	assert.GreaterOrEqual(t, r.LineOffset, 0)
	assert.GreaterOrEqual(t, r.ColOffset, 0)
}

func TestResolveRefs_SampleFixture_MultiKeyCite(t *testing.T) {
	src := readFixture(t, "sample.tex")
	doc, err := Parse(src)
	require.NoError(t, err)

	// Multi-key \cite{GKP1994,Stanley2011} lives on the prose paragraph that
	// segmentContainerGaps lifted out of the section's pre-theorem gap.
	// The innermost block at that line is now the paragraph, not the section.
	sec := findBlockByLabel(doc, "sec:main")
	require.NotNil(t, sec)

	var cites []Ref
	for _, cid := range sec.ChildIDs {
		c := doc.ByID[cid]
		for _, r := range c.RefsOut {
			if r.Kind == "cite" {
				cites = append(cites, r)
			}
		}
	}
	require.Len(t, cites, 2, "multi-key \\cite should produce two Refs")
	targets := []string{cites[0].Target, cites[1].Target}
	assert.Contains(t, targets, "GKP1994")
	assert.Contains(t, targets, "Stanley2011")
	for _, c := range cites {
		assert.False(t, c.Resolved, "cite resolution is deferred to .bbl parser in Task 5")
	}
}

func TestResolveRefs_SampleFixture_ResolvedAndUnresolved(t *testing.T) {
	src := readFixture(t, "sample.tex")
	doc, err := Parse(src)
	require.NoError(t, err)

	// The proof contains \cref{thm:main} (resolved), \ref{fig:diagram}
	// (resolved, forward within document) and \ref{thm:missing} (unresolved).
	var all []Ref
	for _, b := range doc.Blocks {
		if b.Kind != KindProofStep {
			continue
		}
		all = append(all, b.RefsOut...)
	}

	byTarget := map[string]Ref{}
	for _, r := range all {
		byTarget[r.Target] = r
	}

	cref, ok := byTarget["thm:main"]
	require.True(t, ok, "proof should reference thm:main")
	assert.Equal(t, "cref", cref.Kind)
	assert.True(t, cref.Resolved)

	fref, ok := byTarget["fig:diagram"]
	require.True(t, ok, "proof should reference fig:diagram")
	assert.True(t, fref.Resolved, "forward ref to fig:diagram should resolve")

	miss, ok := byTarget["thm:missing"]
	require.True(t, ok, "proof should reference thm:missing")
	assert.False(t, miss.Resolved, "thm:missing is undefined")
}

func TestResolveRefs_OffsetsRelativeToBlockStart(t *testing.T) {
	src := []byte(`\begin{theorem}
\label{t:1}
Pointer \ref{t:1} here.
\end{theorem}
`)
	doc, err := Parse(src)
	require.NoError(t, err)

	thm := doc.ByLabel["t:1"]
	require.NotNil(t, thm)
	require.Len(t, thm.RefsOut, 1)

	r := thm.RefsOut[0]
	assert.Equal(t, "ref", r.Kind)
	assert.Equal(t, "t:1", r.Target)
	assert.True(t, r.Resolved)
	// Ref is on line 3, theorem starts at line 1 → offset 2.
	assert.Equal(t, 2, r.LineOffset)
	// Column of "\" in "\ref" is 9 (1-based) in "Pointer \ref..." → ColOffset 8.
	assert.Equal(t, 8, r.ColOffset)
}

func TestResolveRefs_RefAttachedToInnermostBlock(t *testing.T) {
	// A ref inside a proof step should land on that step, not the enclosing
	// proof or section.
	src := []byte(`\section{S}
\label{sec:s}
\begin{theorem}
\label{t:a}
A.
\end{theorem}
\begin{proof}
First step mentions \ref{t:a}.

Second step.
\end{proof}
`)
	doc, err := Parse(src)
	require.NoError(t, err)

	var withRef *Block
	for _, b := range doc.Blocks {
		if b.Kind == KindProofStep && len(b.RefsOut) > 0 {
			withRef = b
			break
		}
	}
	require.NotNil(t, withRef, "at least one proof step must carry the ref")
	assert.Equal(t, "t:a", withRef.RefsOut[0].Target)
}
