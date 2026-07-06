package diffui

import (
	"fmt"
	"strings"

	"mrevdiff/pkg/diffreview"
	"mrevdiff/pkg/parser"
)

func (m Model) currentOutlineRow() *OutlineRow {
	rows := m.outlineRows()
	for i := range rows {
		if rows[i].Group && !rows[i].Collapsed {
			continue
		}
		if outlineRowContainsPair(rows[i], m.Cursor) {
			return &rows[i]
		}
	}
	return nil
}

func (m Model) currentMemberPairIndices() []int {
	if row := m.currentOutlineRow(); row != nil && len(row.MemberPairIndices) > 0 {
		return append([]int(nil), row.MemberPairIndices...)
	}
	if m.CurrentPair() == nil {
		return nil
	}
	return []int{m.Cursor}
}

// CurrentDisplayPair returns the pair rendered in the source panes. In
// semantic mode it is the selected semantic pair. In coalesced mode it may be a
// synthetic replacement pair spanning the added/deleted members of one rewrite
// group, so the source panes show the FileMerge-like hunk while the underlying
// review state remains per semantic pair.
func (m Model) CurrentDisplayPair() *diffreview.Pair {
	if m.DiffRegime != DiffRegimeCoalesced {
		return m.CurrentPair()
	}
	row := m.currentOutlineRow()
	if row == nil || !row.Coalesced || len(row.MemberPairIndices) <= 1 {
		return m.CurrentPair()
	}
	return coalescedDisplayPair(m.Review, row.MemberPairIndices)
}

func coalescedDisplayPair(review *diffreview.Review, indices []int) *diffreview.Pair {
	if review == nil || len(indices) == 0 {
		return nil
	}
	rep := representativeRewritePair(review, indices)
	if rep < 0 || rep >= len(review.Pairs) {
		return nil
	}
	repPair := review.Pairs[rep]
	pair := diffreview.Pair{
		ID:             "rewrite:" + repPair.ID,
		Status:         diffreview.Changed,
		Score:          repPair.Score,
		OldIndex:       repPair.OldIndex,
		NewIndex:       repPair.NewIndex,
		SectionPathOld: append([]string(nil), repPair.SectionPathOld...),
		SectionPathNew: append([]string(nil), repPair.SectionPathNew...),
	}
	pair.Old = syntheticSideBlock(review.Old.Source, indices, review, true)
	pair.New = syntheticSideBlock(review.New.Source, indices, review, false)
	if pair.Old == nil && pair.New == nil {
		return &repPair
	}
	return &pair
}

func syntheticSideBlock(source []byte, indices []int, review *diffreview.Review, oldSide bool) *parser.Block {
	start, end := 0, 0
	kind := parser.KindParagraph
	for _, idx := range indices {
		if idx < 0 || idx >= len(review.Pairs) {
			continue
		}
		block := review.Pairs[idx].New
		if oldSide {
			block = review.Pairs[idx].Old
		}
		if block == nil || block.StartLine < 1 {
			continue
		}
		if start == 0 || block.StartLine < start {
			start = block.StartLine
		}
		if block.EndLine > end {
			end = block.EndLine
		}
		kind = block.Kind
	}
	if start == 0 || end == 0 {
		return nil
	}
	src := sourceLineRange(source, start, end)
	if strings.TrimSpace(src) == "" {
		src = joinedSideSource(indices, review, oldSide)
	}
	return &parser.Block{
		ID:        syntheticBlockID(indices, oldSide),
		Kind:      kind,
		StartLine: start,
		EndLine:   end,
		Source:    src,
	}
}

func syntheticBlockID(indices []int, oldSide bool) string {
	side := "new"
	if oldSide {
		side = "old"
	}
	if len(indices) == 0 {
		return "rewrite:" + side
	}
	return fmt.Sprintf("rewrite:%s:%d:%d", side, indices[0], indices[len(indices)-1])
}

func joinedSideSource(indices []int, review *diffreview.Review, oldSide bool) string {
	var parts []string
	for _, idx := range indices {
		if idx < 0 || idx >= len(review.Pairs) {
			continue
		}
		block := review.Pairs[idx].New
		if oldSide {
			block = review.Pairs[idx].Old
		}
		if block == nil || strings.TrimSpace(block.Source) == "" {
			continue
		}
		parts = append(parts, block.Source)
	}
	return strings.Join(parts, "\n")
}

func sourceLineRange(source []byte, startLine, endLine int) string {
	if startLine < 1 || endLine < startLine || len(source) == 0 {
		return ""
	}
	lines := strings.Split(string(source), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if startLine > len(lines) {
		return ""
	}
	if endLine > len(lines) {
		endLine = len(lines)
	}
	return strings.Join(lines[startLine-1:endLine], "\n")
}
