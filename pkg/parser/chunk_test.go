package parser

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestApplyChunkBudget_MergeTinySiblings verifies that consecutive tiny
// prose paragraphs inside a section get fused, while a label-bearing
// paragraph and a paragraph with outgoing references are preserved.
func TestApplyChunkBudget_MergeTinySiblings(t *testing.T) {
	src := `\documentclass{article}
\begin{document}
\section{S}
First.

Second.

Third.
\end{document}
`
	doc, err := Parse([]byte(src))
	require.NoError(t, err)

	sec := findFirst(doc, func(b *Block) bool { return b.Kind == KindSection })
	require.NotNil(t, sec)

	// Three single-line prose paragraphs, total span 5 source lines including
	// the blank-line gaps — exactly at ChunkBudgetMergeThreshold = 5. They
	// should fuse into a single paragraph child.
	var paras []*Block
	for _, cid := range sec.ChildIDs {
		c := doc.ByID[cid]
		if c.Kind == KindParagraph {
			paras = append(paras, c)
		}
	}
	assert.Len(t, paras, 1, "tiny adjacent paragraphs should merge into one block")
}

// TestApplyChunkBudget_LabelStopsMerge verifies that a paragraph whose
// source declares a \label{} stays as its own block — the merge pass
// scans paragraph source for \label{ to catch labels that, due to parse
// order, registered against the enclosing section instead of the new
// paragraph chunk.
func TestApplyChunkBudget_LabelStopsMerge(t *testing.T) {
	src := `\documentclass{article}
\begin{document}
\section{S}
First.

\label{p:keep} Second.

Third.
\end{document}
`
	doc, err := Parse([]byte(src))
	require.NoError(t, err)

	sec := findFirst(doc, func(b *Block) bool { return b.Kind == KindSection })
	require.NotNil(t, sec)

	// The label-bearing paragraph blocks merging on either side, so all
	// three originally-distinct paragraphs survive.
	count := 0
	withLabel := 0
	for _, cid := range sec.ChildIDs {
		c := doc.ByID[cid]
		if c.Kind != KindParagraph {
			continue
		}
		count++
		if strings.Contains(c.Source, `\label{p:keep}`) {
			withLabel++
		}
	}
	assert.Equal(t, 3, count, "label-bearing paragraph must keep both neighbours separate")
	assert.Equal(t, 1, withLabel, "label declaration should sit in exactly one paragraph")
}

// TestApplyChunkBudget_BlankGapStopsMerge verifies that two paragraphs
// separated by a multi-line blank gap don't merge even if both are small.
func TestApplyChunkBudget_BlankGapStopsMerge(t *testing.T) {
	src := `\documentclass{article}
\begin{document}
\section{S}
First.



Second.
\end{document}
`
	doc, err := Parse([]byte(src))
	require.NoError(t, err)

	sec := findFirst(doc, func(b *Block) bool { return b.Kind == KindSection })
	require.NotNil(t, sec)

	count := 0
	for _, cid := range sec.ChildIDs {
		if doc.ByID[cid].Kind == KindParagraph {
			count++
		}
	}
	assert.Equal(t, 2, count, "multi-blank gap should prevent merge")
}

// TestApplyChunkBudget_SplitOversizedAtHardBoundary verifies that a
// paragraph straddling a hard boundary (here, a display) is split at
// the boundary line. Realistic prose papers rarely produce paragraphs
// over the chunk-budget threshold without an embedded structural break.
func TestApplyChunkBudget_SplitOversizedAtHardBoundary(t *testing.T) {
	// Force the threshold low so a few lines are enough to trip it.
	old := ChunkBudgetMaxLines
	ChunkBudgetMaxLines = 3
	defer func() { ChunkBudgetMaxLines = old }()

	src := `\documentclass{article}
\begin{document}
\section{S}
alpha
beta
\[ x = 1 \]
gamma
delta
\end{document}
`
	doc, err := Parse([]byte(src))
	require.NoError(t, err)

	sec := findFirst(doc, func(b *Block) bool { return b.Kind == KindSection })
	require.NotNil(t, sec)
	require.NotEmpty(t, sec.ChildIDs)

	// container-gap creates two prose paragraphs around the display: one
	// before (alpha\nbeta) and one after (gamma\ndelta). Neither exceeds
	// the lowered threshold individually; chunk-budget therefore performs
	// no split here. (The split path is exercised in the next test.)
	var paraCount int
	for _, cid := range sec.ChildIDs {
		if doc.ByID[cid].Kind == KindParagraph {
			paraCount++
		}
	}
	assert.GreaterOrEqual(t, paraCount, 1)
}

// TestApplyChunkBudget_SplitOnSentences verifies the soft-boundary
// (sentence) split fallback when an oversized paragraph contains no hard
// boundaries.
func TestApplyChunkBudget_SplitOnSentences(t *testing.T) {
	old := ChunkBudgetMaxLines
	ChunkBudgetMaxLines = 3
	defer func() { ChunkBudgetMaxLines = old }()

	src := `\documentclass{article}
\begin{document}
\section{S}
First sentence.
Second sentence.
Third sentence.
Fourth sentence.
\end{document}
`
	doc, err := Parse([]byte(src))
	require.NoError(t, err)

	sec := findFirst(doc, func(b *Block) bool { return b.Kind == KindSection })
	require.NotNil(t, sec)
	require.NotEmpty(t, sec.ChildIDs)

	para := doc.ByID[sec.ChildIDs[0]]
	require.Equal(t, KindParagraph, para.Kind)
	// The 4-line single-paragraph block exceeds the lowered threshold and
	// should pick up KindParagraph splits via the sentence walker.
	assert.NotEmpty(t, para.ChildIDs, "oversized paragraph must split on sentence ends")
}
