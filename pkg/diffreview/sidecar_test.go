package diffreview

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultSidecarPathUsesNewFileAndBaseRev(t *testing.T) {
	review := &Review{
		Old: Endpoint{Kind: GitBlob, Spec: "master:paper.tex", Label: "master:paper.tex"},
		New: Endpoint{Kind: WorkingFile, Spec: "paper.tex", Path: filepath.Join(t.TempDir(), "paper.tex")},
	}

	got := DefaultSidecarPath(review)
	assert.Equal(t, review.New.Path+".mrevdiff.master.md", got)
}

func TestDefaultSidecarPathForGitNewEndpointAvoidsMaterializedSnapshot(t *testing.T) {
	review := &Review{
		Old: Endpoint{Kind: GitBlob, Spec: "master:paper.tex", Label: "master:paper.tex"},
		New: Endpoint{
			Kind:         GitBlob,
			Spec:         "branch:sections/paper.tex",
			RelPath:      "sections/paper.tex",
			Path:         filepath.Join(t.TempDir(), ".mrevdiff", "session", "branch", "sections", "paper.tex"),
			Materialized: true,
		},
	}

	got := DefaultSidecarPath(review)
	assert.Equal(t, "branch-sections-paper.tex.mrevdiff.master.md", filepath.ToSlash(got))
	assert.Equal(t, ".", filepath.Dir(got))
	assert.NotContains(t, got, ".mrevdiff/session")
}

func TestDiffSidecarSaveLoadAndRemap(t *testing.T) {
	oldSrc := `\section{Intro}

\begin{theorem}
\label{thm:main}
Old statement.
\end{theorem}
`
	newSrc := `\section{Intro}

\begin{theorem}
\label{thm:main}
New statement.
\end{theorem}
`
	review := buildReviewForTest(t, oldSrc, newSrc)
	pair := review.ByID["thm:main"]
	require.NotNil(t, pair)

	side := NewSidecar(review)
	side.CursorPairID = pair.ID
	side.SetReviewed(pair.ID, true)
	side.UpsertAnnotation(AnnotationForPair(review, pair, "check the changed statement"))

	path := filepath.Join(t.TempDir(), "paper.tex.mrevdiff.master.md")
	require.NoError(t, SaveSidecar(path, side))

	loaded, err := LoadSidecar(path)
	require.NoError(t, err)
	require.NotEmpty(t, loaded.Pairs)
	assertPairSummary(t, loaded.Pairs, "thm:main", "changed")
	remapped := RemapSidecar(loaded, review)

	assert.Equal(t, review.Old.Spec, remapped.OldSpec)
	assert.Equal(t, review.New.Spec, remapped.NewSpec)
	assert.Equal(t, pair.ID, remapped.CursorPairID)
	assert.Equal(t, []string{pair.ID}, remapped.Reviewed)
	require.Len(t, remapped.Annotations, 1)
	assert.Equal(t, pair.ID, remapped.Annotations[0].PairID)
	assert.Equal(t, "check the changed statement", remapped.Annotations[0].Note)
	assert.Contains(t, remapped.Annotations[0].SourceQuote, "New statement")
	require.NotEmpty(t, remapped.Pairs)
	assertPairSummary(t, remapped.Pairs, "thm:main", "changed")
}

func TestDiffSidecarRemapPreservesDetachedAnnotations(t *testing.T) {
	review := buildReviewForTest(t, "\\section{Intro}\n\nCurrent text.\n", "\\section{Intro}\n\nChanged current text.\n")
	loaded := &Sidecar{
		Annotations: []Annotation{{
			PairID:      "missing-pair",
			Status:      "changed",
			Side:        "new",
			SourceQuote: "old quote",
			Note:        "keep me",
		}},
		Detached: []Annotation{{
			PairID: "already-detached",
			Note:   "keep me too",
		}},
	}

	remapped := RemapSidecar(loaded, review)

	assert.Empty(t, remapped.Annotations)
	require.Len(t, remapped.Detached, 2)
	assert.Equal(t, "missing-pair", remapped.Detached[0].PairID)
	assert.Equal(t, "already-detached", remapped.Detached[1].PairID)
}

func TestDiffSidecarRemapKeepsUnlabeledMatchedPairAcrossNewTextEdit(t *testing.T) {
	oldSrc := "\\section{Intro}\n\nThis paragraph has enough shared words to match before the edit.\n"
	firstNew := "\\section{Intro}\n\nThis paragraph has enough shared words to match after one edit.\n"
	secondNew := "\\section{Intro}\n\nThis paragraph has enough shared words to match after another edit.\n"
	firstReview := buildReviewForTest(t, oldSrc, firstNew)
	firstPair := requireParagraphPairContaining(t, firstReview, "This paragraph")
	require.NotNil(t, firstPair.Old)
	require.NotNil(t, firstPair.New)

	side := NewSidecar(firstReview)
	side.CursorPairID = firstPair.ID
	side.SetReviewed(firstPair.ID, true)
	side.UpsertAnnotation(AnnotationForPair(firstReview, &firstPair, "keep note"))

	secondReview := buildReviewForTest(t, oldSrc, secondNew)
	secondPair := requireParagraphPairContaining(t, secondReview, "This paragraph")
	assert.Equal(t, firstPair.ID, secondPair.ID)

	remapped := RemapSidecar(side, secondReview)
	assert.Equal(t, secondPair.ID, remapped.CursorPairID)
	assert.Equal(t, []string{secondPair.ID}, remapped.Reviewed)
	require.Len(t, remapped.Annotations, 1)
	assert.Equal(t, "keep note", remapped.Annotations[0].Note)
	assert.Empty(t, remapped.Detached)
}

func TestParseSidecarMalformedInputs(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
	}{
		{name: "empty", data: ""},
		{name: "no frontmatter", data: "# notes\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			side, err := ParseSidecar([]byte(tc.data))
			require.NoError(t, err)
			assert.Empty(t, side.Reviewed)
			assert.Empty(t, side.Annotations)
		})
	}

	_, err := ParseSidecar([]byte("---\nreviewed:\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unterminated")

	_, err = ParseSidecar([]byte("---\nannotations: [\n---\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sidecar yaml")
}

func TestDiffPairIDsPreferLabelAndNewBlockID(t *testing.T) {
	review := buildReviewForTest(t, "\\section{Intro}\n\n\\begin{theorem}\n\\label{thm:x}\nOld.\n\\end{theorem}\n", "\\section{Intro}\n\n\\begin{theorem}\n\\label{thm:x}\nNew.\n\\end{theorem}\n")
	require.NotNil(t, review.ByID["thm:x"])

	added := buildReviewForTest(t, "\\section{Intro}\n", "\\section{Intro}\n\nAdded paragraph with enough words to become one semantic block.\n")
	var addedID string
	for _, pair := range added.Pairs {
		if pair.Status == Added && pair.New != nil && strings.Contains(pair.New.Source, "Added paragraph") {
			addedID = pair.ID
		}
	}
	require.NotEmpty(t, addedID)
	assert.NotContains(t, addedID, "new:")

	deleted := buildReviewForTest(t, "\\section{Intro}\n\nDeleted paragraph with enough words to become one semantic block.\n", "\\section{Intro}\n")
	var deletedID string
	for _, pair := range deleted.Pairs {
		if pair.Status == Deleted && pair.Old != nil && strings.Contains(pair.Old.Source, "Deleted paragraph") {
			deletedID = pair.ID
		}
	}
	require.NotEmpty(t, deletedID)
	assert.True(t, strings.HasPrefix(deletedID, "old:"))
}

func TestDiffStdoutMarkdownContainsSpecsAndPairStatuses(t *testing.T) {
	review := buildReviewForTest(t, "\\section{Intro}\n\nOld paragraph with enough words for matching.\n", "\\section{Intro}\n\nNew paragraph with enough words for matching.\n")
	side := NewSidecar(review)

	var out bytes.Buffer
	require.NoError(t, EmitMarkdown(&out, side, review))
	text := out.String()

	assert.Contains(t, text, "Old: old.tex")
	assert.Contains(t, text, "New: new.tex")
	assert.Contains(t, text, "## Pair statuses")
	assert.Contains(t, text, "- changed")
}

func TestDiffStdoutJSONContainsPairs(t *testing.T) {
	review := buildReviewForTest(t, "\\section{Intro}\n\nOld paragraph with enough words for matching.\n", "\\section{Intro}\n\nNew paragraph with enough words for matching.\n")
	side := NewSidecar(review)

	var out bytes.Buffer
	require.NoError(t, Emit(&out, side, review, StdoutJSON))

	assert.Contains(t, out.String(), `"old_spec": "old.tex"`)
	assert.Contains(t, out.String(), `"status": "changed"`)
}

func assertPairSummary(t *testing.T, pairs []PairSummary, id, status string) {
	t.Helper()
	for _, pair := range pairs {
		if pair.ID == id {
			assert.Equal(t, status, pair.Status)
			return
		}
	}
	require.FailNow(t, "missing pair summary", "id=%s pairs=%#v", id, pairs)
}

func TestMergeSidecarsPreservesConcurrentDiskAndMemoryChanges(t *testing.T) {
	base := &Sidecar{
		Reviewed: []string{"a", "remove"},
		Annotations: []Annotation{
			{PairID: "edit", Note: "old note"},
			{PairID: "delete", Note: "delete me"},
		},
	}
	disk := &Sidecar{
		Reviewed: []string{"a", "external"},
		Annotations: []Annotation{
			{PairID: "external", Note: "external note"},
		},
	}
	mem := &Sidecar{
		OldSpec:      "old.tex",
		NewSpec:      "new.tex",
		CursorPairID: "cursor",
		Reviewed:     []string{"a", "memory"},
		Annotations: []Annotation{
			{PairID: "edit", Note: "edited note"},
			{PairID: "memory", Note: "memory note"},
		},
	}

	merged := MergeSidecars(base, disk, mem)
	reviewed := merged.ReviewedSet()
	for _, id := range []string{"a", "external", "memory"} {
		assert.True(t, reviewed[id], "merged reviewed missing %q: %#v", id, merged.Reviewed)
	}
	assert.False(t, reviewed["remove"], "user-removed reviewed ID survived: %#v", merged.Reviewed)
	notes := merged.AnnotationNotes()
	assert.Equal(t, "external note", notes["external"])
	assert.Equal(t, "edited note", notes["edit"])
	assert.Equal(t, "memory note", notes["memory"])
	assert.Empty(t, notes["delete"])
	assert.Equal(t, "cursor", merged.CursorPairID)
	assert.Equal(t, "old.tex", merged.OldSpec)
	assert.Equal(t, "new.tex", merged.NewSpec)
}

func TestSaveSidecarMergingMergesFileCreatedDuringSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review.md")
	require.NoError(t, SaveSidecar(path, &Sidecar{
		Annotations: []Annotation{{PairID: "external", Note: "external note"}},
	}))

	require.NoError(t, SaveSidecarMerging(path, &Sidecar{}, time.Time{}, &Sidecar{
		Annotations: []Annotation{{PairID: "memory", Note: "memory note"}},
	}))

	saved, err := LoadSidecar(path)
	require.NoError(t, err)
	notes := saved.AnnotationNotes()
	assert.Equal(t, "external note", notes["external"])
	assert.Equal(t, "memory note", notes["memory"])
}
