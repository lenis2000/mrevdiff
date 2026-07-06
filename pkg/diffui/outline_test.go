package diffui

import (
	"strings"
	"testing"

	"mrevdiff/pkg/diffreview"
	"mrevdiff/pkg/parser"
)

func TestBuildOutlineMarkersAndStats(t *testing.T) {
	review := fixtureReview()

	rows := BuildOutline(review, FilterAll, nil, map[string]string{"fmt": "note"}, map[string][]string{"deleted": []string{"issue"}})
	markers := map[string]string{}
	for _, row := range rows {
		markers[row.PairID] = row.Marker
	}

	want := map[string]string{
		"same":    "≡",
		"changed": "~",
		"added":   "+",
		"deleted": "-",
		"fmt":     "fmt",
		"moved":   "↷",
	}
	for id, marker := range want {
		if markers[id] != marker {
			t.Fatalf("marker for %s: got %q want %q", id, markers[id], marker)
		}
	}
	if rowByPairID(rows, "fmt") == nil || !rowByPairID(rows, "fmt").Annotated {
		t.Fatalf("expected annotated marker on fmt row")
	}
	if rowByPairID(rows, "deleted") == nil || !rowByPairID(rows, "deleted").Issues {
		t.Fatalf("expected issues marker on deleted row")
	}

	m := New(review, Options{Filter: FilterAll, Annotations: map[string]string{"fmt": "note"}, Issues: map[string][]string{"deleted": {"issue"}}})
	outline := m.renderOutline(120, 10)
	for _, needle := range []string{"stats total:6", "≡:1", "~:1", "+:1", "-:1", "fmt:1", "↷:1"} {
		if !strings.Contains(outline, needle) {
			t.Fatalf("outline stats missing %q in:\n%s", needle, outline)
		}
	}
}

func TestCoalescedOutlineGroupsAdjacentAddDeleteRewrite(t *testing.T) {
	review := &diffreview.Review{Pairs: []diffreview.Pair{
		{
			ID:             "added-rewrite",
			Status:         diffreview.Added,
			New:            fixtureBlock("new-added-rewrite", 10, "The new coherent replacement paragraph."),
			OldIndex:       -1,
			NewIndex:       0,
			SectionPathNew: []string{"Intro"},
		},
		{
			ID:             "deleted-rewrite-1",
			Status:         diffreview.Deleted,
			Old:            fixtureBlock("old-deleted-rewrite-1", 20, "Old paragraph one."),
			OldIndex:       0,
			NewIndex:       -1,
			SectionPathOld: []string{"Intro"},
		},
		{
			ID:             "deleted-rewrite-2",
			Status:         diffreview.Deleted,
			Old:            fixtureBlock("old-deleted-rewrite-2", 21, "Old paragraph two."),
			OldIndex:       1,
			NewIndex:       -1,
			SectionPathOld: []string{"Intro"},
		},
	}}

	semantic := BuildOutlineWithRegime(review, FilterChanged, DiffRegimeSemantic, nil, nil, nil)
	if rowByPairID(semantic, "added-rewrite") == nil || rowByPairID(semantic, "deleted-rewrite-1") == nil || rowByPairID(semantic, "deleted-rewrite-2") == nil {
		t.Fatalf("semantic outline should keep individual rows: %#v", semantic)
	}
	coalesced := BuildOutlineWithRegime(review, FilterChanged, DiffRegimeCoalesced, nil, nil, nil)
	var rows []OutlineRow
	for _, row := range coalesced {
		if !row.Group {
			rows = append(rows, row)
		}
	}
	if len(rows) != 1 || !rows[0].Coalesced {
		t.Fatalf("coalesced outline rows = %#v, want one rewrite row", coalesced)
	}
	if rows[0].Marker != "±" || !strings.Contains(rows[0].Title, "+1/-2") {
		t.Fatalf("rewrite row = %#v, want ± +1/-2 summary", rows[0])
	}
	if got := outlineCursorRow(coalesced, 2, 1); got != 1 {
		t.Fatalf("cursor for hidden member row = %d, want rewrite row 1 in %#v", got, coalesced)
	}

	m := New(review, Options{Filter: FilterChanged, DiffRegime: DiffRegimeCoalesced})
	m.Cursor = 1
	display := m.CurrentDisplayPair()
	if display == nil || display.Old == nil || display.New == nil {
		t.Fatalf("display pair = %#v, want synthetic old+new replacement", display)
	}
	if !strings.Contains(display.Old.Source, "Old paragraph one") || !strings.Contains(display.Old.Source, "Old paragraph two") || !strings.Contains(display.New.Source, "new coherent replacement") {
		t.Fatalf("display pair did not combine rewrite sources: old=%q new=%q", display.Old.Source, display.New.Source)
	}
}

func TestOutlineGroupsSectionsWithoutSplittingInternalHunks(t *testing.T) {
	review := &diffreview.Review{Pairs: []diffreview.Pair{
		{
			ID:     "intro-section",
			Status: diffreview.Changed,
			Old:    fixtureSectionBlock("old-intro", 1, "Introduction"),
			New:    fixtureSectionBlock("new-intro", 1, "Introduction"),
		},
		{
			ID:             "overview-section",
			Status:         diffreview.Changed,
			Old:            fixtureSectionBlock("old-overview", 2, "Overview"),
			New:            fixtureSectionBlock("new-overview", 2, "Overview"),
			SectionPathOld: []string{"Introduction"},
			SectionPathNew: []string{"Introduction"},
		},
		{
			ID:             "intro-para",
			Status:         diffreview.Changed,
			Old:            fixtureBlock("old-intro-para", 3, "first old\nunchanged middle\nsecond old"),
			New:            fixtureBlock("new-intro-para", 3, "first new\nunchanged middle\nsecond new"),
			SectionPathOld: []string{"Introduction", "Overview"},
			SectionPathNew: []string{"Introduction", "Overview"},
		},
	}}
	rows := BuildOutline(review, FilterChanged, nil, nil, nil)
	if len(rows) != 3 {
		t.Fatalf("rows = %#v, want two section groups plus one changed-pair row", rows)
	}
	if !rows[0].Group || rows[0].Title != "Introduction" || rows[0].Depth != 0 {
		t.Fatalf("first row = %#v, want top-level Introduction group", rows[0])
	}
	if !rows[1].Group || rows[1].Title != "Overview" || rows[1].Depth != 1 {
		t.Fatalf("second row = %#v, want nested Overview group", rows[1])
	}
	introGroups := 0
	for _, row := range rows {
		if row.PairID == "intro-section" || row.PairID == "overview-section" {
			t.Fatalf("section container pair should be a group, not a selectable chunk: %#v", rows)
		}
		if row.Group && row.Title == "Introduction" {
			introGroups++
		}
	}
	if introGroups != 1 {
		t.Fatalf("Introduction group should appear once, got %d rows=%#v", introGroups, rows)
	}
	if rows[2].PairID != "intro-para" || strings.Contains(rows[2].Title, "chunk") {
		t.Fatalf("paragraph should be one pair row, not split into chunks: %#v", rows)
	}
	outline := RenderOutlineAt(rows, 2, 3, 100, 10)
	if !strings.Contains(outline, "▾ Introduction") || !strings.Contains(outline, "▾ Overview") ||
		!strings.Contains(outline, ">") || strings.Contains(outline, "chunk") {
		t.Fatalf("outline should show nested groups and one pair row, not chunks:\n%s", outline)
	}
}

func TestFilterBehavior(t *testing.T) {
	review := fixtureReview()

	m := New(review, Options{})
	if m.Filter != FilterChanged {
		t.Fatalf("default filter: got %s want changed", m.Filter)
	}
	if got := visibleIDs(m); strings.Join(got, ",") != "changed,added,deleted,fmt,moved" {
		t.Fatalf("changed filter visible ids = %v", got)
	}

	m.Filter = FilterUnreviewed
	m.Reviewed["changed"] = true
	if got := visibleIDs(m); strings.Join(got, ",") != "added,deleted,fmt,moved" {
		t.Fatalf("unreviewed filter visible ids = %v", got)
	}

	m.Filter = FilterAnnotated
	m.Annotations["fmt"] = "format note"
	if got := visibleIDs(m); strings.Join(got, ",") != "fmt" {
		t.Fatalf("annotated filter visible ids = %v", got)
	}

	m.Filter = FilterIssues
	m.Issues["deleted"] = []string{"needs decision"}
	if got := visibleIDs(m); strings.Join(got, ",") != "deleted" {
		t.Fatalf("issues filter visible ids = %v", got)
	}
}

func rowByPairID(rows []OutlineRow, pairID string) *OutlineRow {
	for i := range rows {
		if rows[i].PairID == pairID {
			return &rows[i]
		}
	}
	return nil
}

func visibleIDs(m Model) []string {
	indices := m.visibleIndices()
	out := make([]string, 0, len(indices))
	for _, idx := range indices {
		out = append(out, m.Review.Pairs[idx].ID)
	}
	return out
}

func fixtureSectionBlock(id string, startLine int, title string) *parser.Block {
	source := "\\section{" + title + "}"
	return &parser.Block{
		ID:        id,
		Kind:      parser.KindSection,
		Title:     title,
		StartLine: startLine,
		EndLine:   startLine,
		Source:    source,
	}
}

func fixtureReview() *diffreview.Review {
	pairs := []diffreview.Pair{
		{
			ID:             "same",
			Status:         diffreview.Unchanged,
			Old:            fixtureBlock("old-same", 1, "Same paragraph."),
			New:            fixtureBlock("new-same", 1, "Same paragraph."),
			OldIndex:       0,
			NewIndex:       0,
			SectionPathOld: []string{"Intro"},
			SectionPathNew: []string{"Intro"},
		},
		{
			ID:             "changed",
			Status:         diffreview.Changed,
			Old:            fixtureBlock("old-changed", 3, "Alpha\nold beta"),
			New:            fixtureBlock("new-changed", 3, "Alpha\nnew beta"),
			OldIndex:       1,
			NewIndex:       1,
			SectionPathOld: []string{"Intro"},
			SectionPathNew: []string{"Intro"},
		},
		{
			ID:             "added",
			Status:         diffreview.Added,
			New:            fixtureBlock("new-added", 6, "Added line one.\nAdded line two."),
			OldIndex:       -1,
			NewIndex:       2,
			SectionPathNew: []string{"Intro"},
		},
		{
			ID:             "deleted",
			Status:         diffreview.Deleted,
			Old:            fixtureBlock("old-deleted", 9, "Deleted line one.\nDeleted line two."),
			OldIndex:       2,
			NewIndex:       -1,
			SectionPathOld: []string{"Methods"},
		},
		{
			ID:             "fmt",
			Status:         diffreview.FormatOnly,
			Old:            fixtureBlock("old-fmt", 12, "A  B"),
			New:            fixtureBlock("new-fmt", 12, "A B"),
			OldIndex:       3,
			NewIndex:       3,
			SectionPathOld: []string{"Methods"},
			SectionPathNew: []string{"Methods"},
		},
		{
			ID:             "moved",
			Status:         diffreview.Moved,
			Old:            fixtureBlock("old-moved", 14, "\\begin{theorem}\\label{thm:moved}\nOld section.\n\\end{theorem}"),
			New:            fixtureBlock("new-moved", 14, "\\begin{theorem}\\label{thm:moved}\nNew section.\n\\end{theorem}"),
			OldIndex:       4,
			NewIndex:       4,
			SectionPathOld: []string{"Old section"},
			SectionPathNew: []string{"New section"},
		},
	}
	return &diffreview.Review{Pairs: pairs}
}
