package diffui

import (
	"fmt"
	"strings"

	"github.com/lenis2000/mrevdiff/pkg/diffreview"
	"github.com/lenis2000/mrevdiff/pkg/parser"
)

// OutlineRow is one visible row in the diff outline. Selectable rows point to
// a semantic pair plus a source-line anchor; group rows are section headers.
type OutlineRow struct {
	PairID            string
	PairIndex         int
	MemberPairIndices []int
	AnchorLine        int
	HunkIndex         int
	HunkCount         int
	Marker            string
	Status            diffreview.PairStatus
	Title             string
	Section           string
	GroupPath         []string
	GroupKey          string
	Reviewed          bool
	Annotated         bool
	Issues            bool
	Group             bool
	Collapsed         bool
	Coalesced         bool
	Depth             int
}

// BuildOutline returns visible section headers and diff chunks under the
// selected filter. Container section pairs are rendered as folded-looking
// group headers; selectable rows point at individual diff hunks.
func BuildOutline(
	review *diffreview.Review,
	filter Filter,
	reviewed map[string]bool,
	annotations map[string]string,
	issues map[string][]string,
) []OutlineRow {
	return BuildOutlineWithRegime(review, filter, DiffRegimeSemantic, reviewed, annotations, issues)
}

func BuildOutlineWithRegime(
	review *diffreview.Review,
	filter Filter,
	regime DiffRegime,
	reviewed map[string]bool,
	annotations map[string]string,
	issues map[string][]string,
) []OutlineRow {
	return BuildOutlineWithRegimeAndCollapsed(review, filter, regime, reviewed, annotations, issues, nil)
}

func BuildOutlineWithRegimeAndCollapsed(
	review *diffreview.Review,
	filter Filter,
	regime DiffRegime,
	reviewed map[string]bool,
	annotations map[string]string,
	issues map[string][]string,
	collapsed map[string]bool,
) []OutlineRow {
	if review == nil {
		return nil
	}
	rows := make([]OutlineRow, 0, len(review.Pairs))
	groupRows := map[string]int{}
	var currentPath []string
	for i := 0; i < len(review.Pairs); i++ {
		if regime == DiffRegimeCoalesced {
			if group, ok := collectRewriteGroup(review, i, filter, reviewed, annotations, issues); ok {
				path := outlinePairPath(review.Pairs[group[0]])
				visiblePath, collapsedKey := visibleOutlinePath(path, collapsed)
				rows, currentPath = appendOutlineGroups(rows, currentPath, visiblePath, collapsed, groupRows)
				if collapsedKey != "" {
					for _, idx := range group {
						rows = addCollapsedMember(rows, groupRows, collapsedKey, idx)
					}
					i = group[len(group)-1]
					continue
				}
				rows = append(rows, outlineRewriteRow(review, group, len(path), reviewed, annotations, issues))
				i = group[len(group)-1]
				continue
			}
		}
		pair := review.Pairs[i]
		if !pairMatchesFilter(pair, filter, reviewed, annotations, issues) {
			continue
		}
		if isSectionPair(pair) {
			path := outlineSectionPairPath(pair)
			visiblePath, collapsedKey := visibleOutlinePath(path, collapsed)
			rows, currentPath = appendOutlineGroups(rows, currentPath, visiblePath, collapsed, groupRows)
			if collapsedKey != "" {
				rows = addCollapsedMember(rows, groupRows, collapsedKey, i)
			}
			continue
		}
		path := outlinePairPath(pair)
		visiblePath, collapsedKey := visibleOutlinePath(path, collapsed)
		rows, currentPath = appendOutlineGroups(rows, currentPath, visiblePath, collapsed, groupRows)
		if collapsedKey != "" {
			rows = addCollapsedMember(rows, groupRows, collapsedKey, i)
			continue
		}
		info := outlinePairInfo(pair)
		rows = append(rows, OutlineRow{
			PairID:            pair.ID,
			PairIndex:         i,
			MemberPairIndices: []int{i},
			AnchorLine:        info.AnchorLine,
			HunkIndex:         1,
			HunkCount:         info.HunkCount,
			Marker:            StatusMarker(pair.Status),
			Status:            pair.Status,
			Title:             pairTitle(pair),
			Reviewed:          reviewed[pair.ID],
			Annotated:         annotations[pair.ID] != "",
			Issues:            len(issues[pair.ID]) > 0,
			Depth:             len(path),
		})
	}
	return rows
}

func collectRewriteGroup(
	review *diffreview.Review,
	start int,
	filter Filter,
	reviewed map[string]bool,
	annotations map[string]string,
	issues map[string][]string,
) ([]int, bool) {
	if review == nil || start < 0 || start >= len(review.Pairs) {
		return nil, false
	}
	if !coalescingFilter(filter) {
		return nil, false
	}
	first := review.Pairs[start]
	if !rewriteGroupCandidate(first, filter, reviewed, annotations, issues) {
		return nil, false
	}
	section := sectionKey(first)
	group := []int{start}
	hasAdded := first.Status == diffreview.Added
	hasDeleted := first.Status == diffreview.Deleted
	for i := start + 1; i < len(review.Pairs); i++ {
		pair := review.Pairs[i]
		if !rewriteGroupCandidate(pair, filter, reviewed, annotations, issues) {
			break
		}
		if sectionKey(pair) != section {
			break
		}
		group = append(group, i)
		hasAdded = hasAdded || pair.Status == diffreview.Added
		hasDeleted = hasDeleted || pair.Status == diffreview.Deleted
	}
	if len(group) < 2 || !hasAdded || !hasDeleted {
		return nil, false
	}
	return group, true
}

func coalescingFilter(filter Filter) bool {
	switch filter {
	case FilterAll, FilterChanged, FilterUnreviewed:
		return true
	default:
		return false
	}
}

func rewriteGroupCandidate(pair diffreview.Pair, filter Filter, reviewed map[string]bool, annotations map[string]string, issues map[string][]string) bool {
	if pair.Status != diffreview.Added && pair.Status != diffreview.Deleted {
		return false
	}
	return pairMatchesFilter(pair, filter, reviewed, annotations, issues)
}

func outlineRewriteRow(review *diffreview.Review, indices []int, depth int, reviewed map[string]bool, annotations map[string]string, issues map[string][]string) OutlineRow {
	rep := representativeRewritePair(review, indices)
	added, deleted := 0, 0
	row := OutlineRow{
		PairID:            review.Pairs[rep].ID,
		PairIndex:         rep,
		MemberPairIndices: append([]int(nil), indices...),
		AnchorLine:        1,
		HunkIndex:         1,
		HunkCount:         1,
		Marker:            "±",
		Status:            diffreview.Changed,
		Coalesced:         true,
		Depth:             depth,
	}
	reviewedAll := true
	for _, idx := range indices {
		pair := review.Pairs[idx]
		switch pair.Status {
		case diffreview.Added:
			added++
		case diffreview.Deleted:
			deleted++
		}
		if !reviewed[pair.ID] {
			reviewedAll = false
		}
		row.Annotated = row.Annotated || annotations[pair.ID] != ""
		row.Issues = row.Issues || len(issues[pair.ID]) > 0
	}
	row.Reviewed = reviewedAll
	row.Title = fmt.Sprintf("rewrite +%d/-%d: %s", added, deleted, rewriteGroupTitle(review, indices))
	return row
}

func representativeRewritePair(review *diffreview.Review, indices []int) int {
	if len(indices) == 0 {
		return 0
	}
	for _, idx := range indices {
		if idx >= 0 && idx < len(review.Pairs) && review.Pairs[idx].New != nil {
			return idx
		}
	}
	return indices[0]
}

func rewriteGroupTitle(review *diffreview.Review, indices []int) string {
	if review == nil {
		return "replacement"
	}
	for _, idx := range indices {
		if idx < 0 || idx >= len(review.Pairs) {
			continue
		}
		pair := review.Pairs[idx]
		if pair.New != nil {
			return pairTitle(pair)
		}
	}
	if len(indices) > 0 && indices[0] >= 0 && indices[0] < len(review.Pairs) {
		return pairTitle(review.Pairs[indices[0]])
	}
	return "replacement"
}

// StatusMarker returns the compact marker used for a pair status.
func StatusMarker(status diffreview.PairStatus) string {
	switch status {
	case diffreview.Unchanged:
		return "≡"
	case diffreview.FormatOnly:
		return "fmt"
	case diffreview.Changed:
		return "~"
	case diffreview.Added:
		return "+"
	case diffreview.Deleted:
		return "-"
	case diffreview.Moved:
		return "↷"
	default:
		return "?"
	}
}

func (m Model) outlineRows() []OutlineRow {
	return BuildOutlineWithRegimeAndCollapsed(m.Review, m.Filter, m.DiffRegime, m.Reviewed, m.Annotations, m.Issues, m.Collapsed)
}

func (m Model) renderOutline(width, height int) string {
	rows := m.outlineRows()
	stats := reviewStats(m.Review)
	header := fmt.Sprintf(
		"stats total:%d %s:%d ~:%d +:%d -:%d fmt:%d ↷:%d",
		stats.Total,
		StatusMarker(diffreview.Unchanged),
		stats.Unchanged,
		stats.Changed,
		stats.Added,
		stats.Deleted,
		stats.FormatOnly,
		stats.Moved,
	)
	if height <= 1 {
		return clipLine(header, width)
	}
	body := RenderOutlineAt(rows, m.Cursor, m.SourceLineCursor, width, height-1)
	if body == "" {
		body = "(no pairs)"
	}
	return clipLine(header, width) + "\n" + body
}

func (m *Model) toggleOutlineFold() {
	key, path, ok := m.currentFoldTarget()
	if !ok {
		m.Status = "z: no foldable outline group"
		return
	}
	if m.Collapsed == nil {
		m.Collapsed = map[string]bool{}
	}
	label := path[len(path)-1]
	if m.Collapsed[key] {
		delete(m.Collapsed, key)
		m.Status = "unfolded " + label
		return
	}
	m.Collapsed[key] = true
	m.Status = "folded " + label
}

func (m Model) currentFoldTarget() (string, []string, bool) {
	rows := m.outlineRows()
	if len(rows) > 0 {
		cursorRow := outlineCursorRow(rows, m.Cursor, m.SourceLineCursor)
		if cursorRow >= 0 && cursorRow < len(rows) {
			row := rows[cursorRow]
			if row.Group && row.GroupKey != "" && len(row.GroupPath) > 0 {
				return row.GroupKey, append([]string(nil), row.GroupPath...), true
			}
		}
	}
	pair := m.CurrentPair()
	if pair == nil {
		return "", nil, false
	}
	path := outlinePairPath(*pair)
	if len(path) == 0 {
		return "", nil, false
	}
	path = append([]string(nil), path...)
	return outlinePathKey(path), path, true
}

// RenderOutline renders already-built outline rows. The cursor is the index
// into Review.Pairs, not an index into the filtered row slice.
func RenderOutline(rows []OutlineRow, cursorPairIndex int, width, height int) string {
	return RenderOutlineAt(rows, cursorPairIndex, 1, width, height)
}

// RenderOutlineAt also receives the current source-line cursor for compatibility
// with callers that keep source-line state; the outline itself stays pair-based.
func RenderOutlineAt(rows []OutlineRow, cursorPairIndex, sourceLineCursor int, width, height int) string {
	if height < 1 {
		height = 1
	}
	if width < 1 {
		width = 1
	}
	if len(rows) == 0 {
		return clipLine("(no pairs)", width)
	}

	cursorRow := outlineCursorRow(rows, cursorPairIndex, sourceLineCursor)
	start := cursorRow - height/2
	if start < 0 {
		start = 0
	}
	if start > len(rows)-height {
		start = len(rows) - height
		if start < 0 {
			start = 0
		}
	}
	end := start + height
	if end > len(rows) {
		end = len(rows)
	}

	rendered := make([]string, 0, end-start)
	for i, row := range rows[start:end] {
		absoluteRow := start + i
		cursor := " "
		if absoluteRow == cursorRow {
			cursor = ">"
		}
		if row.Group {
			indent := strings.Repeat("  ", row.Depth)
			line := fmt.Sprintf("%s %s%s %s", cursor, indent, row.Marker, row.Title)
			rendered = append(rendered, clipLine(line, width))
			continue
		}
		flags := "   "
		if row.Reviewed || row.Annotated || row.Issues {
			marks := []rune{' ', ' ', ' '}
			if row.Reviewed {
				marks[0] = '✓'
			}
			if row.Annotated {
				marks[1] = '*'
			}
			if row.Issues {
				marks[2] = '!'
			}
			flags = string(marks)
		}
		indent := strings.Repeat("  ", row.Depth)
		line := fmt.Sprintf("%s %s %-3s %s%s", cursor, flags, row.Marker, indent, row.Title)
		if row.Section != "" {
			line += " [" + row.Section + "]"
		}
		rendered = append(rendered, clipLine(line, width))
	}
	return strings.Join(rendered, "\n")
}

func outlineCursorRow(rows []OutlineRow, cursorPairIndex, sourceLineCursor int) int {
	if len(rows) == 0 {
		return 0
	}
	if sourceLineCursor < 1 {
		sourceLineCursor = 1
	}
	fallback := -1
	best := -1
	bestAnchor := -1
	for i, row := range rows {
		if !outlineRowContainsPair(row, cursorPairIndex) {
			continue
		}
		if row.Group && !row.Collapsed {
			continue
		}
		if fallback < 0 || (row.Group && row.Collapsed) {
			fallback = i
		}
		anchor := row.AnchorLine
		if anchor < 1 {
			anchor = 1
		}
		if !row.Group && anchor <= sourceLineCursor && anchor >= bestAnchor {
			best = i
			bestAnchor = anchor
		}
	}
	if best >= 0 {
		return best
	}
	if fallback >= 0 {
		return fallback
	}
	for i, row := range rows {
		if !row.Group {
			return i
		}
	}
	return 0
}

func outlineRowContainsPair(row OutlineRow, pairIndex int) bool {
	if row.PairIndex == pairIndex {
		return true
	}
	for _, idx := range row.MemberPairIndices {
		if idx == pairIndex {
			return true
		}
	}
	return false
}

func appendOutlineGroups(rows []OutlineRow, currentPath, nextPath []string, collapsed map[string]bool, groupRows map[string]int) ([]OutlineRow, []string) {
	common := commonStringPrefixLen(currentPath, nextPath)
	for level := common; level < len(nextPath); level++ {
		path := append([]string(nil), nextPath[:level+1]...)
		key := outlinePathKey(path)
		isCollapsed := collapsed[key]
		marker := "▾"
		if isCollapsed {
			marker = "▸"
		}
		rows = append(rows, OutlineRow{
			PairIndex: -1,
			Marker:    marker,
			Title:     nextPath[level],
			GroupPath: path,
			GroupKey:  key,
			Group:     true,
			Collapsed: isCollapsed,
			Depth:     level,
		})
		if groupRows != nil {
			groupRows[key] = len(rows) - 1
		}
	}
	return rows, append([]string(nil), nextPath...)
}

func visibleOutlinePath(path []string, collapsed map[string]bool) ([]string, string) {
	if len(path) == 0 {
		return nil, ""
	}
	for i := range path {
		prefix := append([]string(nil), path[:i+1]...)
		key := outlinePathKey(prefix)
		if collapsed[key] {
			return prefix, key
		}
	}
	return append([]string(nil), path...), ""
}

func addCollapsedMember(rows []OutlineRow, groupRows map[string]int, groupKey string, pairIndex int) []OutlineRow {
	rowIndex, ok := groupRows[groupKey]
	if !ok || rowIndex < 0 || rowIndex >= len(rows) || pairIndex < 0 {
		return rows
	}
	if rows[rowIndex].PairIndex < 0 {
		rows[rowIndex].PairIndex = pairIndex
		rows[rowIndex].AnchorLine = 1
	}
	if !containsIndex(rows[rowIndex].MemberPairIndices, pairIndex) {
		rows[rowIndex].MemberPairIndices = append(rows[rowIndex].MemberPairIndices, pairIndex)
	}
	return rows
}

func outlinePathKey(path []string) string {
	return strings.Join(path, "\x00")
}

func commonStringPrefixLen(a, b []string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

func outlinePairPath(pair diffreview.Pair) []string {
	path := pair.SectionPathNew
	if len(path) == 0 {
		path = pair.SectionPathOld
	}
	return append([]string(nil), path...)
}

func outlineSectionPairPath(pair diffreview.Pair) []string {
	path := outlinePairPath(pair)
	title := strings.TrimSpace(pairTitle(pair))
	if title != "" && title != "(missing block)" {
		path = append(path, title)
	}
	return path
}

type outlinePairInfoResult struct {
	AnchorLine int
	HunkCount  int
}

func outlinePairInfo(pair diffreview.Pair) outlinePairInfoResult {
	infos := diffHunkInfos(&pair)
	if len(infos) == 0 {
		return outlinePairInfoResult{AnchorLine: 1, HunkCount: 1}
	}
	anchor := infos[0].AnchorLine
	if anchor < 1 {
		anchor = 1
	}
	return outlinePairInfoResult{AnchorLine: anchor, HunkCount: len(infos)}
}

func isSectionPair(pair diffreview.Pair) bool {
	block := pair.New
	if block == nil {
		block = pair.Old
	}
	return block != nil && block.Kind == parser.KindSection
}

func pairTitle(pair diffreview.Pair) string {
	block := pair.New
	if block == nil {
		block = pair.Old
	}
	if block == nil {
		return "(missing block)"
	}
	if block.Label != "" {
		return block.Label
	}
	if block.Title != "" {
		return block.Title
	}
	first := firstNonBlankLine(block.Source)
	if first != "" {
		return first
	}
	return block.Kind.String()
}

func sectionLabel(pair diffreview.Pair) string {
	path := pair.SectionPathNew
	if len(path) == 0 {
		path = pair.SectionPathOld
	}
	if len(path) == 0 {
		return ""
	}
	return strings.Join(path, " / ")
}

func blockLineCount(source string) int {
	source = strings.TrimSuffix(source, "\n")
	if source == "" {
		return 1
	}
	return strings.Count(source, "\n") + 1
}

func firstNonBlankLine(source string) string {
	for _, line := range strings.Split(source, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

type stats struct {
	Total      int
	Unchanged  int
	FormatOnly int
	Changed    int
	Added      int
	Deleted    int
	Moved      int
}

func reviewStats(review *diffreview.Review) stats {
	if review == nil {
		return stats{}
	}
	out := stats{}
	for _, pair := range review.Pairs {
		out.Total++
		switch pair.Status {
		case diffreview.Unchanged:
			out.Unchanged++
		case diffreview.FormatOnly:
			out.FormatOnly++
		case diffreview.Changed:
			out.Changed++
		case diffreview.Added:
			out.Added++
		case diffreview.Deleted:
			out.Deleted++
		case diffreview.Moved:
			out.Moved++
		}
	}
	return out
}
