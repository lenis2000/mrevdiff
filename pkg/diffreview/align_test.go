package diffreview

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"mrevdiff/pkg/parser"
)

func TestAlignLabeledTheoremSurvivesLineDriftAndTextEdits(t *testing.T) {
	oldSrc := `\section{Intro}

\begin{theorem}
\label{thm:main}
Let $x=1$.
\end{theorem}
`
	newSrc := `\section{Intro}

Opening sentence before the theorem.

\begin{theorem}
\label{thm:main}
Let $x=2$.
\end{theorem}
`
	review := buildReviewForTest(t, oldSrc, newSrc)

	pair := requirePairByNewLabel(t, review, "thm:main")
	require.NotNil(t, pair.Old)
	assert.Equal(t, "thm:main", pair.Old.Label)
	assert.Equal(t, Changed, pair.Status)
	assert.Greater(t, pair.New.StartLine, pair.Old.StartLine)
	assert.Equal(t, 1.0, pair.Score)
}

func TestAlignLabelOnlyChangeIsSemantic(t *testing.T) {
	oldSrc := `\section{Intro}

\begin{theorem}
\label{thm:old}
This theorem has enough shared words to match after a label rename.
\end{theorem}
`
	newSrc := `\section{Intro}

\begin{theorem}
\label{thm:new}
This theorem has enough shared words to match after a label rename.
\end{theorem}
`
	review := buildReviewForTest(t, oldSrc, newSrc)

	pair := requirePairByNewLabel(t, review, "thm:new")
	require.NotNil(t, pair.Old)
	assert.Equal(t, "thm:old", pair.Old.Label)
	assert.Equal(t, Changed, pair.Status)
	assert.Equal(t, NormalizeSourceForMatch(pair.Old.Source), NormalizeSourceForMatch(pair.New.Source))
}

func TestAlignUnlabeledParagraphsMatchByNormalizedText(t *testing.T) {
	oldSrc := `\section{Intro}

This paragraph spans
several words.
`
	newSrc := `\section{Intro}

This paragraph spans   several words.
`
	review := buildReviewForTest(t, oldSrc, newSrc)

	pair := requireParagraphPairContaining(t, review, "This paragraph")
	require.NotNil(t, pair.Old)
	require.NotNil(t, pair.New)
	assert.Equal(t, FormatOnly, pair.Status)
	assert.Equal(t, NormalizeSourceForMatch(pair.Old.Source), NormalizeSourceForMatch(pair.New.Source))
}

func TestAlignUnlabeledParagraphsMatchByFuzzyEdit(t *testing.T) {
	oldSrc := `\section{Intro}

This paragraph has enough shared words to match conservatively before the edit.
`
	newSrc := `\section{Intro}

This paragraph has enough shared words to match conservatively after the edit.
`
	review := buildReviewForTest(t, oldSrc, newSrc)

	pair := requireParagraphPairContaining(t, review, "This paragraph")
	require.NotNil(t, pair.Old)
	require.NotNil(t, pair.New)
	assert.Equal(t, Changed, pair.Status)
	assert.Equal(t, pair.Old.ID, pair.ID)
}

func TestAlignAddedAndDeletedParagraphsAppearNearNeighbors(t *testing.T) {
	oldSrc := `\section{Intro}

Keep first.

\[
a
\]

Delete me.

\[
b
\]

Keep second.
`
	newSrc := `\section{Intro}

Keep first.

\[
a
\]

Add me.

\[
b
\]

Keep second.
`
	review := buildReviewForTest(t, oldSrc, newSrc)

	paragraphPairs := paragraphPairs(review)
	first := indexOfPair(paragraphPairs, func(p Pair) bool {
		return p.New != nil && strings.Contains(p.New.Source, "Keep first")
	})
	added := indexOfPair(paragraphPairs, func(p Pair) bool {
		return p.Status == Added && p.New != nil && strings.Contains(p.New.Source, "Add me")
	})
	deleted := indexOfPair(paragraphPairs, func(p Pair) bool {
		return p.Status == Deleted && p.Old != nil && strings.Contains(p.Old.Source, "Delete me")
	})
	second := indexOfPair(paragraphPairs, func(p Pair) bool {
		return p.New != nil && strings.Contains(p.New.Source, "Keep second")
	})

	require.NotEqual(t, -1, first)
	require.NotEqual(t, -1, added)
	require.NotEqual(t, -1, deleted)
	require.NotEqual(t, -1, second)
	assert.Less(t, first, added)
	assert.Less(t, first, deleted)
	assert.Less(t, added, second)
	assert.Less(t, deleted, second)
}

func TestAlignProofFollowsMatchedTheoremLabel(t *testing.T) {
	oldSrc := `\section{Intro}

\begin{theorem}
\label{thm:proof}
Let $x=1$.
\end{theorem}
\begin{proof}
The claim follows from $x=1$.
\end{proof}
`
	newSrc := `\section{Intro}

\begin{theorem}
\label{thm:proof}
Let $x=1$.
\end{theorem}
\begin{proof}
The claim follows from $x=2$ after the correction.
\end{proof}
`
	review := buildReviewForTest(t, oldSrc, newSrc)

	pair := requirePairByNewKind(t, review, parser.KindProof)
	require.NotNil(t, pair.Old)
	assert.Equal(t, parser.KindProof, pair.Old.Kind)
	assert.Equal(t, Changed, pair.Status)
	assert.Equal(t, "thm:proof.proof", pair.Old.ID)
	assert.Equal(t, "thm:proof.proof", pair.New.ID)
}

func TestAlignRepeatedGenericTextDoesNotProduceBogusMatch(t *testing.T) {
	oldSrc := `\section{Intro}

Repeated generic text.

\[
x
\]

Repeated generic text.
`
	newSrc := `\section{Intro}

Repeated generic text.
`
	review := buildReviewForTest(t, oldSrc, newSrc)

	var matchedRepeated int
	var addedRepeated int
	var deletedRepeated int
	for _, pair := range paragraphPairs(review) {
		oldRepeated := pair.Old != nil && strings.Contains(pair.Old.Source, "Repeated generic text")
		newRepeated := pair.New != nil && strings.Contains(pair.New.Source, "Repeated generic text")
		switch {
		case oldRepeated && newRepeated:
			matchedRepeated++
		case newRepeated && pair.Status == Added:
			addedRepeated++
		case oldRepeated && pair.Status == Deleted:
			deletedRepeated++
		}
	}
	assert.Zero(t, matchedRepeated)
	assert.Equal(t, 1, addedRepeated)
	assert.Equal(t, 2, deletedRepeated)
}

func TestAlignReviewBlocksDoNotDuplicateContainerDescendants(t *testing.T) {
	oldSrc := `\section{Intro}

\begin{lemma}
\label{lem:nested}
Statement before the display.
\begin{equation}
x=1
\end{equation}
Statement after the display.
\end{lemma}

\begin{proof}
First proof sentence.
\begin{equation}
y=1
\end{equation}
Second proof sentence.
\end{proof}
`
	newSrc := `\section{Intro}

\begin{lemma}
\label{lem:nested}
Statement before the display.
\begin{equation}
x=1
\end{equation}
Statement after the display.
\end{lemma}

\begin{proof}
First proof sentence.
\begin{equation}
y=2
\end{equation}
Second proof sentence.
\end{proof}
`
	review := buildReviewForTest(t, oldSrc, newSrc)

	var changedProofs int
	for _, pair := range review.Pairs {
		for _, block := range []*parser.Block{pair.Old, pair.New} {
			if block == nil {
				continue
			}
			if block.Kind == parser.KindProofStep {
				t.Fatalf("proof step %q was emitted alongside its proof container", block.ID)
			}
			if block.Kind == parser.KindDisplay && hasAncestorKind(block, review.OldDoc, parser.KindProof, parser.KindTheoremLike) {
				t.Fatalf("nested display %q was emitted alongside its container", block.ID)
			}
			if block.Kind == parser.KindDisplay && hasAncestorKind(block, review.NewDoc, parser.KindProof, parser.KindTheoremLike) {
				t.Fatalf("nested display %q was emitted alongside its container", block.ID)
			}
		}
		if pair.New != nil && pair.New.Kind == parser.KindProof {
			changedProofs++
			assert.Equal(t, Changed, pair.Status)
			assert.Contains(t, pair.New.Source, "y=2")
		}
	}
	assert.Equal(t, 1, changedProofs)
	assertNoOverlappingReviewBlocks(t, review)
}

func TestAlignReviewBlocksDescendsIntoListItemsWithoutListWrapper(t *testing.T) {
	oldSrc := `\section{Intro}

\begin{itemize}
\item First item has enough shared words before the edit.
\item Second item is unchanged.
\end{itemize}
`
	newSrc := `\section{Intro}

\begin{itemize}
\item First item has enough shared words after the edit.
\item Second item is unchanged.
\end{itemize}
`
	review := buildReviewForTest(t, oldSrc, newSrc)

	for _, pair := range review.Pairs {
		for _, block := range []*parser.Block{pair.Old, pair.New} {
			if block != nil && block.Kind == parser.KindOther && block.EnvName == "itemize" {
				t.Fatalf("list wrapper %q should not be emitted alongside item rows", block.ID)
			}
		}
	}
	pair := requireParagraphPairContaining(t, review, "First item")
	assert.Equal(t, Changed, pair.Status)
	assert.Contains(t, pair.Old.Source, "before the edit")
	assert.Contains(t, pair.New.Source, "after the edit")
	assertNoOverlappingReviewBlocks(t, review)
}

func TestAlignFormatOnlyChangeDetectedSeparately(t *testing.T) {
	oldSrc := `\section{Intro}

Value  is 100\% here. % trailing note
`
	newSrc := `\section{Intro}

Value is 100\% here.
`
	review := buildReviewForTest(t, oldSrc, newSrc)

	pair := requireParagraphPairContaining(t, review, "Value")
	assert.Equal(t, FormatOnly, pair.Status)
	assert.Equal(t, `Value is 100\% here.`, NormalizeSourceForMatch(pair.New.Source))
	assert.Equal(t, NormalizeSourceForMatch(pair.New.Source), NormalizeSourceForMatch(pair.Old.Source))
}

func TestAlignMovedLabeledBlockDetected(t *testing.T) {
	oldSrc := `\section{A}

\begin{theorem}
\label{thm:moved}
Move me.
\end{theorem}

\section{B}
`
	newSrc := `\section{A}

\section{B}

\begin{theorem}
\label{thm:moved}
Move me, with an edit.
\end{theorem}
`
	review := buildReviewForTest(t, oldSrc, newSrc)

	pair := requirePairByNewLabel(t, review, "thm:moved")
	assert.Equal(t, Moved, pair.Status)
	assert.Equal(t, []string{"A"}, pair.SectionPathOld)
	assert.Equal(t, []string{"B"}, pair.SectionPathNew)
}

func TestAlignPairIDsStayUniqueForDuplicateLabels(t *testing.T) {
	oldSrc := `\section{Intro}

\begin{theorem}
\label{thm:dup}
First duplicate theorem has enough shared words before the edit.
\end{theorem}

\begin{theorem}
\label{thm:dup}
Second duplicate theorem has enough shared words before the edit.
\end{theorem}
`
	newSrc := `\section{Intro}

\begin{theorem}
\label{thm:dup}
First duplicate theorem has enough shared words after the edit.
\end{theorem}

\begin{theorem}
\label{thm:dup}
Second duplicate theorem has enough shared words after the edit.
\end{theorem}
`
	review := buildReviewForTest(t, oldSrc, newSrc)

	seen := map[string]bool{}
	var duplicateLabelPairs int
	for _, pair := range review.Pairs {
		if seen[pair.ID] {
			t.Fatalf("duplicate pair ID %q in %#v", pair.ID, review.Pairs)
		}
		seen[pair.ID] = true
		if pair.New != nil && pair.New.Label == "thm:dup" {
			duplicateLabelPairs++
		}
	}
	assert.Equal(t, 2, duplicateLabelPairs)
	assert.True(t, seen["thm:dup"], "first duplicate-label pair missing")
	assert.True(t, seen["thm:dup~2"], "second duplicate-label pair missing")
}

func buildReviewForTest(t *testing.T, oldSrc, newSrc string) *Review {
	t.Helper()
	review, err := BuildReview(
		Endpoint{Label: "old", Spec: "old.tex", Path: "old.tex", Source: []byte(oldSrc)},
		Endpoint{Label: "new", Spec: "new.tex", Path: "new.tex", Source: []byte(newSrc), Editable: true},
	)
	require.NoError(t, err)
	return review
}

func requirePairByNewLabel(t *testing.T, review *Review, label string) Pair {
	t.Helper()
	for _, pair := range review.Pairs {
		if pair.New != nil && pair.New.Label == label {
			return pair
		}
	}
	require.FailNow(t, "missing pair by new label", label)
	return Pair{}
}

func requirePairByNewKind(t *testing.T, review *Review, kind parser.Kind) Pair {
	t.Helper()
	for _, pair := range review.Pairs {
		if pair.New != nil && pair.New.Kind == kind {
			return pair
		}
	}
	require.FailNow(t, "missing pair by new kind", kind.String())
	return Pair{}
}

func requireParagraphPairContaining(t *testing.T, review *Review, text string) Pair {
	t.Helper()
	for _, pair := range paragraphPairs(review) {
		if pair.New != nil && strings.Contains(pair.New.Source, text) {
			return pair
		}
	}
	require.FailNow(t, "missing paragraph pair", text)
	return Pair{}
}

func paragraphPairs(review *Review) []Pair {
	var out []Pair
	for _, pair := range review.Pairs {
		if (pair.Old != nil && pair.Old.Kind == parser.KindParagraph) ||
			(pair.New != nil && pair.New.Kind == parser.KindParagraph) {
			out = append(out, pair)
		}
	}
	return out
}

func indexOfPair(pairs []Pair, pred func(Pair) bool) int {
	for i, pair := range pairs {
		if pred(pair) {
			return i
		}
	}
	return -1
}

func hasAncestorKind(block *parser.Block, doc *parser.Document, kinds ...parser.Kind) bool {
	if block == nil || doc == nil {
		return false
	}
	want := map[parser.Kind]bool{}
	for _, kind := range kinds {
		want[kind] = true
	}
	for parentID := block.ParentID; parentID != ""; {
		parent := doc.ByID[parentID]
		if parent == nil {
			return false
		}
		if want[parent.Kind] {
			return true
		}
		parentID = parent.ParentID
	}
	return false
}

func assertNoOverlappingReviewBlocks(t *testing.T, review *Review) {
	t.Helper()
	assertNoOverlappingSide(t, review.Pairs, true)
	assertNoOverlappingSide(t, review.Pairs, false)
}

func assertNoOverlappingSide(t *testing.T, pairs []Pair, oldSide bool) {
	t.Helper()
	lastEnd := 0
	for _, pair := range pairs {
		block := pair.New
		if oldSide {
			block = pair.Old
		}
		if block == nil || block.StartLine < 1 {
			continue
		}
		if block.StartLine <= lastEnd {
			t.Fatalf("overlapping review block on oldSide=%v: %q starts at %d before previous end %d", oldSide, block.ID, block.StartLine, lastEnd)
		}
		if block.EndLine > lastEnd {
			lastEnd = block.EndLine
		}
	}
}
