package diffui

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/pmezard/go-difflib/difflib"

	"github.com/lenis2000/mrevdiff/pkg/diffreview"
	"github.com/lenis2000/mrevdiff/pkg/parser"
)

type sourcePartKind int

const (
	sourcePartEqual sourcePartKind = iota
	sourcePartDelete
	sourcePartAdd
	sourcePartChange
)

type sourcePart struct {
	Text string
	Kind sourcePartKind
	// Search marks the part as an occurrence of the active / query; the
	// styling layer paints it with the search background. Set only by
	// decorateSearchParts at render time — never inside memoized rows,
	// which outlive any particular query.
	Search bool
}

const sourceTabReplacement = "    "

// renderSearchTerm is the active / query, set by View before rendering.
// Package-level (like the render memos) because the cell renderers are
// free functions shared with the mouse click mapping; Update and View run
// on one goroutine. Lowercase; empty disables highlighting.
var renderSearchTerm string

// decorateSearchParts splits parts so occurrences of the active search
// term (case-insensitive) carry Search=true. The concatenated text is
// unchanged, so wrapping — and therefore the click mapping's geometry —
// is identical with or without an active search.
func decorateSearchParts(parts []sourcePart, term string) []sourcePart {
	if term == "" {
		return parts
	}
	out := make([]sourcePart, 0, len(parts))
	for _, part := range parts {
		lower := strings.ToLower(part.Text)
		if len(lower) != len(part.Text) {
			// Lowercasing changed byte offsets (rare non-ASCII case
			// folding); keep the part undecorated rather than mis-slice.
			out = append(out, part)
			continue
		}
		rest := part.Text
		restLower := lower
		for {
			i := strings.Index(restLower, term)
			if i < 0 {
				break
			}
			if i > 0 {
				out = append(out, sourcePart{Text: rest[:i], Kind: part.Kind})
			}
			out = append(out, sourcePart{Text: rest[i : i+len(term)], Kind: part.Kind, Search: true})
			rest = rest[i+len(term):]
			restLower = restLower[i+len(term):]
		}
		if rest != "" {
			out = append(out, sourcePart{Text: rest, Kind: part.Kind})
		}
	}
	return out
}

type sourceRow struct {
	oldMark   string
	oldLine   int
	oldText   string
	oldParts  []sourcePart
	newMark   string
	newLine   int
	newText   string
	newParts  []sourcePart
	separator bool
}

var (
	diffDeleteLineStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("224")).Background(lipgloss.Color("52"))
	diffAddLineStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("194")).Background(lipgloss.Color("22"))
	diffChangeLineStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Background(lipgloss.Color("58"))
	diffDeleteTokenStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Background(lipgloss.Color("160")).Bold(true)
	diffAddTokenStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("82")).Bold(true)
	diffChangeTokenStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("226")).Bold(true)
)

// RenderPairSource renders the old/new source for one semantic pair. Long
// physical TeX lines are soft-wrapped inside their side of the diff so content
// does not disappear behind an ellipsis on narrow panes.
func RenderPairSource(pair *diffreview.Pair, width, height int) string {
	return RenderPairSourceAt(pair, width, height, 0, 0)
}

// RenderPairSourceAt is RenderPairSource with an optional source-line anchor.
// When an anchor is supplied, the rendered window scrolls so that line is
// visible inside long semantic blocks.
func RenderPairSourceAt(pair *diffreview.Pair, width, height, oldAnchorLine, newAnchorLine int) string {
	return renderPairSource(pair, width, height, oldAnchorLine, newAnchorLine, false)
}

// RenderPairSourceHighlighted is the TUI variant: same geometry as
// RenderPairSourceAt, but changed/added/deleted lines get FileMerge-like
// full-row highlighting and paired changed lines get token-level highlights.
func RenderPairSourceHighlighted(pair *diffreview.Pair, width, height, oldAnchorLine, newAnchorLine int) string {
	return renderPairSource(pair, width, height, oldAnchorLine, newAnchorLine, true)
}

func renderPairSource(pair *diffreview.Pair, width, height, oldAnchorLine, newAnchorLine int, highlight bool) string {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	rows := memoizedSourceRows(pair)
	if len(rows) == 0 {
		return "(no source)"
	}
	oldW := (width - 3) / 2
	if oldW < 1 {
		oldW = 1
	}
	newW := width - oldW - 3
	if newW < 1 {
		newW = 1
	}

	rendered, _, _, anchorRendered := sourceCombinedRenderedLines(rows, oldW, newW, oldAnchorLine, newAnchorLine, highlight)
	return visibleRenderedLines(rendered, height, anchorRendered)
}

// sourceCombinedRenderedLines is the combined (narrow, single-pane)
// renderer's core loop, also returning per-line old/new source numbers
// for the mouse click mapping.
func sourceCombinedRenderedLines(rows []sourceRow, oldW, newW, oldAnchorLine, newAnchorLine int, highlight bool) ([]string, []int, []int, int) {
	rendered := make([]string, 0, len(rows))
	oldNums := make([]int, 0, len(rows))
	newNums := make([]int, 0, len(rows))
	anchorRendered := -1
	for rowIndex, row := range rows {
		rowStart := len(rendered)
		if anchorRendered < 0 && sourceRowMatchesAnchor(row, rowIndex, oldAnchorLine, newAnchorLine) {
			anchorRendered = rowStart
		}
		if row.separator {
			rendered = append(rendered, hunkSeparatorLine(oldW, newW))
			oldNums = append(oldNums, 0)
			newNums = append(newNums, 0)
			continue
		}
		oldCursor := oldAnchorLine > 0 && row.oldLine == oldAnchorLine
		newCursor := newAnchorLine > 0 && row.newLine == newAnchorLine
		oldLines := renderSourceCell(row.oldMark, row.oldLine, row.oldText, row.oldParts, oldW, true, highlight, oldCursor)
		newLines := renderSourceCell(row.newMark, row.newLine, row.newText, row.newParts, newW, false, highlight, newCursor)
		lineCount := max(len(oldLines), len(newLines))
		for i := 0; i < lineCount; i++ {
			oldCell := strings.Repeat(" ", oldW)
			if i < len(oldLines) {
				oldCell = oldLines[i]
			}
			newCell := ""
			if i < len(newLines) {
				newCell = newLines[i]
			}
			rendered = append(rendered, oldCell+" │ "+newCell)
			oldNums = append(oldNums, row.oldLine)
			newNums = append(newNums, row.newLine)
		}
	}
	return rendered, oldNums, newNums, anchorRendered
}

// sourceCombinedLineAtRow maps a viewport row of the combined renderer to
// the absolute source line of the requested side.
func sourceCombinedLineAtRow(pair *diffreview.Pair, width, height, oldAnchorLine, newAnchorLine, rowInView int, oldSide bool) (int, bool) {
	if rowInView < 0 || height < 1 || width < 1 {
		return 0, false
	}
	rows := memoizedSourceRows(pair)
	if len(rows) == 0 || rowInView >= height {
		return 0, false
	}
	oldW := (width - 3) / 2
	if oldW < 1 {
		oldW = 1
	}
	newW := width - oldW - 3
	if newW < 1 {
		newW = 1
	}
	rendered, oldNums, newNums, anchorRendered := sourceCombinedRenderedLines(rows, oldW, newW, oldAnchorLine, newAnchorLine, true)
	idx := visibleStart(len(rendered), height, anchorRendered) + rowInView
	if idx < 0 || idx >= len(rendered) {
		return 0, false
	}
	nums := newNums
	if oldSide {
		nums = oldNums
	}
	if nums[idx] < 1 {
		return 0, false
	}
	return nums[idx], true
}

// RenderPairSourceSide renders one side of a semantic pair for the wide
// four-pane layout.
func RenderPairSourceSide(pair *diffreview.Pair, oldSide bool, width, height int) string {
	return RenderPairSourceSideAt(pair, oldSide, width, height, 0, 0)
}

// RenderPairSourceSideAt is RenderPairSourceSide with an optional source-line
// anchor used by the TUI to scroll within long blocks.
func RenderPairSourceSideAt(pair *diffreview.Pair, oldSide bool, width, height, oldAnchorLine, newAnchorLine int) string {
	return renderPairSourceSide(pair, oldSide, width, height, oldAnchorLine, newAnchorLine, false)
}

// RenderPairSourceSideHighlighted is the TUI variant with FileMerge-like
// full-row and token-level highlights.
func RenderPairSourceSideHighlighted(pair *diffreview.Pair, oldSide bool, width, height, oldAnchorLine, newAnchorLine int) string {
	return renderPairSourceSide(pair, oldSide, width, height, oldAnchorLine, newAnchorLine, true)
}

func renderPairSourceSide(pair *diffreview.Pair, oldSide bool, width, height, oldAnchorLine, newAnchorLine int, highlight bool) string {
	rendered, _, anchorRendered := sourceSideRenderedLines(pair, oldSide, width, oldAnchorLine, newAnchorLine, highlight)
	if rendered == nil {
		return "(no source)"
	}
	return visibleRenderedLines(rendered, height, anchorRendered)
}

// sourceSideRenderedLines builds the side pane's rendered lines plus a
// parallel slice with each rendered line's absolute source line number
// (0 for separators and padding). The mouse click mapping shares this
// with the renderer so a click can never land on a different line than
// the one drawn there.
func sourceSideRenderedLines(pair *diffreview.Pair, oldSide bool, width, oldAnchorLine, newAnchorLine int, highlight bool) ([]string, []int, int) {
	if width < 1 {
		width = 1
	}
	rows := compactSourceRowsForSide(memoizedSourceRows(pair), oldSide)
	if len(rows) == 0 {
		return nil, nil, -1
	}
	rendered := make([]string, 0, len(rows))
	lineNums := make([]int, 0, len(rows))
	anchorRendered := -1
	for rowIndex, row := range rows {
		rowStart := len(rendered)
		if anchorRendered < 0 && sourceRowMatchesAnchor(row, rowIndex, oldAnchorLine, newAnchorLine) {
			anchorRendered = rowStart
		}
		if row.separator {
			rendered = append(rendered, hunkSeparatorSide(width))
			lineNums = append(lineNums, 0)
			continue
		}
		var lines []string
		lineNum := row.newLine
		if oldSide {
			lineNum = row.oldLine
			cursor := oldAnchorLine > 0 && row.oldLine == oldAnchorLine
			lines = renderSourceCell(row.oldMark, row.oldLine, row.oldText, row.oldParts, width, true, highlight, cursor)
		} else {
			cursor := newAnchorLine > 0 && row.newLine == newAnchorLine
			lines = renderSourceCell(row.newMark, row.newLine, row.newText, row.newParts, width, false, highlight, cursor)
		}
		rendered = append(rendered, lines...)
		for range lines {
			lineNums = append(lineNums, lineNum)
		}
	}
	return rendered, lineNums, anchorRendered
}

// sourceSideLineAtRow maps a viewport row (0-based, below the pane title)
// of the side renderer back to the absolute source line drawn there.
func sourceSideLineAtRow(pair *diffreview.Pair, oldSide bool, width, height, oldAnchorLine, newAnchorLine, rowInView int) (int, bool) {
	if rowInView < 0 || height < 1 {
		return 0, false
	}
	rendered, lineNums, anchorRendered := sourceSideRenderedLines(pair, oldSide, width, oldAnchorLine, newAnchorLine, true)
	if rendered == nil || rowInView >= height {
		return 0, false
	}
	idx := visibleStart(len(rendered), height, anchorRendered) + rowInView
	if idx < 0 || idx >= len(lineNums) || lineNums[idx] < 1 {
		return 0, false
	}
	return lineNums[idx], true
}

func sourceRows(pair *diffreview.Pair) []sourceRow {
	if pair == nil {
		return []sourceRow{{oldText: "(no pair selected)", newText: "(no pair selected)"}}
	}
	switch pair.Status {
	case diffreview.Added:
		return rowsForAdded(pair.New)
	case diffreview.Deleted:
		return rowsForDeleted(pair.Old)
	default:
		return rowsForMatched(pair.Old, pair.New)
	}
}

func compactSourceRowsForSide(rows []sourceRow, oldSide bool) []sourceRow {
	out := make([]sourceRow, 0, len(rows))
	for i := 0; i < len(rows); {
		row := rows[i]
		if row.separator || sourceRowHasSideContent(row, oldSide) {
			out = append(out, row)
			i++
			continue
		}
		if !sourceRowChanged(row) {
			i++
			continue
		}
		start := i
		for i < len(rows) && !rows[i].separator && sourceRowChanged(rows[i]) && !sourceRowHasSideContent(rows[i], oldSide) {
			i++
		}
		out = append(out, oppositeSidePlaceholderRow(rows[start:i], oldSide))
	}
	return out
}

func sourceRowHasSideContent(row sourceRow, oldSide bool) bool {
	if oldSide {
		return row.oldLine > 0 || row.oldMark != "" || row.oldText != "" || len(row.oldParts) > 0
	}
	return row.newLine > 0 || row.newMark != "" || row.newText != "" || len(row.newParts) > 0
}

func oppositeSidePlaceholderRow(rows []sourceRow, oldSide bool) sourceRow {
	if len(rows) == 0 {
		return sourceRow{}
	}
	text := oppositeSidePlaceholderText(len(rows), oldSide)
	row := sourceRow{}
	if oldSide {
		row.oldText = text
		row.newLine = rows[0].newLine
	} else {
		row.newText = text
		row.oldLine = rows[0].oldLine
	}
	return row
}

func oppositeSidePlaceholderText(n int, oldSide bool) string {
	if n < 1 {
		n = 1
	}
	lineWord := "lines"
	if n == 1 {
		lineWord = "line"
	}
	if oldSide {
		return fmt.Sprintf("(%d %s added in new)", n, lineWord)
	}
	return fmt.Sprintf("(%d %s deleted from new)", n, lineWord)
}

func rowsForAdded(newBlock *parser.Block) []sourceRow {
	newLines := blockSourceLines(newBlock)
	rows := make([]sourceRow, 0, max(1, len(newLines)))
	if len(newLines) == 0 {
		return []sourceRow{{oldText: "(added in new)", newText: "(no source)"}}
	}
	for i, line := range newLines {
		row := sourceRow{newMark: "+", newLine: sourceLineNumber(newBlock, i), newText: line}
		if i == 0 {
			row.oldText = "(added in new)"
		}
		rows = append(rows, row)
	}
	return rows
}

func rowsForDeleted(oldBlock *parser.Block) []sourceRow {
	oldLines := blockSourceLines(oldBlock)
	rows := make([]sourceRow, 0, max(1, len(oldLines)))
	if len(oldLines) == 0 {
		return []sourceRow{{oldText: "(no source)", newText: "(deleted from new)"}}
	}
	for i, line := range oldLines {
		row := sourceRow{oldMark: "-", oldLine: sourceLineNumber(oldBlock, i), oldText: line}
		if i == 0 {
			row.newText = "(deleted from new)"
		}
		rows = append(rows, row)
	}
	return rows
}

func rowsForMatched(oldBlock, newBlock *parser.Block) []sourceRow {
	oldLines := blockSourceLines(oldBlock)
	newLines := blockSourceLines(newBlock)
	if len(oldLines) == 0 && len(newLines) == 0 {
		return []sourceRow{{oldText: "(no old source)", newText: "(no new source)"}}
	}
	return rowsForMatchedTokenBlock(oldBlock, newBlock, oldLines, newLines)
}

type lineToken struct {
	Text    string
	Norm    string
	Line    int
	Visible bool
}

func rowsForMatchedTokenBlock(oldBlock, newBlock *parser.Block, oldLines, newLines []string) []sourceRow {
	oldParts, newParts, scores := tokenBlockDiffParts(oldLines, newLines)
	oldMarks := sideLineMarks(oldLines, oldParts, true)
	newMarks := sideLineMarks(newLines, newParts, false)
	return alignDisplayRows(oldBlock, newBlock, oldLines, newLines, oldParts, newParts, oldMarks, newMarks, scores)
}

func tokenBlockDiffParts(oldLines, newLines []string) ([][]sourcePart, [][]sourcePart, [][]float64) {
	oldTokens := tokenizeLatexLineTokens(oldLines)
	newTokens := tokenizeLatexLineTokens(newLines)
	oldParts := equalLineParts(oldLines)
	newParts := equalLineParts(newLines)
	scores := make([][]float64, len(oldLines))
	for i := range scores {
		scores[i] = make([]float64, len(newLines))
	}
	oldVisibleIdx, oldKeys := visibleLineTokenKeys(oldTokens)
	newVisibleIdx, newKeys := visibleLineTokenKeys(newTokens)
	matcher := difflib.NewMatcher(oldKeys, newKeys)
	oldKinds := make([]sourcePartKind, len(oldTokens))
	newKinds := make([]sourcePartKind, len(newTokens))
	for _, op := range matcher.GetOpCodes() {
		switch op.Tag {
		case 'e':
			for oi, nj := op.I1, op.J1; oi < op.I2 && nj < op.J2; oi, nj = oi+1, nj+1 {
				ot := oldTokens[oldVisibleIdx[oi]]
				nt := newTokens[newVisibleIdx[nj]]
				if ot.Line >= 0 && ot.Line < len(scores) && nt.Line >= 0 && nt.Line < len(newLines) {
					scores[ot.Line][nt.Line] += tokenDisplayWeight(ot.Norm)
				}
			}
		case 'd':
			for oi := op.I1; oi < op.I2; oi++ {
				oldKinds[oldVisibleIdx[oi]] = sourcePartDelete
			}
		case 'i':
			for nj := op.J1; nj < op.J2; nj++ {
				newKinds[newVisibleIdx[nj]] = sourcePartAdd
			}
		case 'r':
			for oi := op.I1; oi < op.I2; oi++ {
				oldKinds[oldVisibleIdx[oi]] = sourcePartChange
			}
			for nj := op.J1; nj < op.J2; nj++ {
				newKinds[newVisibleIdx[nj]] = sourcePartChange
			}
		}
	}
	oldParts = partsFromLineTokens(oldLines, oldTokens, oldKinds)
	newParts = partsFromLineTokens(newLines, newTokens, newKinds)
	return oldParts, newParts, scores
}

func tokenizeLatexLineTokens(lines []string) []lineToken {
	var out []lineToken
	for lineIdx, line := range lines {
		for _, tok := range tokenizeLatex(line) {
			out = append(out, lineToken{
				Text:    tok,
				Norm:    normalizeLineToken(tok),
				Line:    lineIdx,
				Visible: strings.TrimSpace(tok) != "",
			})
		}
	}
	return out
}

func normalizeLineToken(tok string) string {
	if strings.TrimSpace(tok) == "" {
		return ""
	}
	return strings.ToLower(tok)
}

func visibleLineTokenKeys(tokens []lineToken) ([]int, []string) {
	idx := make([]int, 0, len(tokens))
	keys := make([]string, 0, len(tokens))
	for i, tok := range tokens {
		if !tok.Visible {
			continue
		}
		idx = append(idx, i)
		keys = append(keys, tok.Norm)
	}
	return idx, keys
}

func equalLineParts(lines []string) [][]sourcePart {
	parts := make([][]sourcePart, len(lines))
	for i, line := range lines {
		parts[i] = []sourcePart{{Text: line, Kind: sourcePartEqual}}
	}
	return parts
}

func partsFromLineTokens(lines []string, tokens []lineToken, kinds []sourcePartKind) [][]sourcePart {
	parts := make([][]sourcePart, len(lines))
	for i := range lines {
		parts[i] = nil
	}
	for i, tok := range tokens {
		kind := sourcePartEqual
		if i < len(kinds) && kinds[i] != sourcePartEqual {
			kind = kinds[i]
		}
		if tok.Line >= 0 && tok.Line < len(parts) {
			parts[tok.Line] = appendPart(parts[tok.Line], kind, false, tok.Text)
		}
	}
	for i, line := range lines {
		if parts[i] == nil {
			parts[i] = []sourcePart{{Text: line, Kind: sourcePartEqual}}
		}
	}
	return parts
}

func tokenDisplayWeight(norm string) float64 {
	if norm == "" {
		return 0
	}
	if isDiffStopword(norm) {
		return 0.35
	}
	if strings.HasPrefix(norm, "\\") {
		return 2
	}
	if len([]rune(norm)) >= 6 {
		return 2
	}
	return 1
}

func isDiffStopword(s string) bool {
	switch s {
	case "the", "a", "an", "of", "in", "on", "for", "to", "and", "or", "but", "with", "by", "as", "is", "are", "be", "this", "that":
		return true
	default:
		return false
	}
}

func sideLineMarks(lines []string, parts [][]sourcePart, oldSide bool) []string {
	marks := make([]string, len(lines))
	for i := range lines {
		marks[i] = lineMarkForParts(parts[i], oldSide)
	}
	return marks
}

func lineMarkForParts(parts []sourcePart, oldSide bool) string {
	visibleEqual := false
	visibleChange := false
	visibleInsertDelete := false
	for _, part := range parts {
		if strings.TrimSpace(part.Text) == "" {
			continue
		}
		switch part.Kind {
		case sourcePartEqual:
			visibleEqual = true
		case sourcePartDelete, sourcePartAdd:
			visibleInsertDelete = true
		case sourcePartChange:
			visibleChange = true
		}
	}
	if !visibleChange && !visibleInsertDelete {
		return " "
	}
	if !visibleEqual && !visibleChange && visibleInsertDelete {
		if oldSide {
			return "-"
		}
		return "+"
	}
	return "~"
}

func alignDisplayRows(
	oldBlock, newBlock *parser.Block,
	oldLines, newLines []string,
	oldParts, newParts [][]sourcePart,
	oldMarks, newMarks []string,
	scores [][]float64,
) []sourceRow {
	oldN, newN := len(oldLines), len(newLines)
	dp := make([][]float64, oldN+1)
	choice := make([][]byte, oldN+1)
	for i := range dp {
		dp[i] = make([]float64, newN+1)
		choice[i] = make([]byte, newN+1)
	}
	for i := oldN; i >= 0; i-- {
		for j := newN; j >= 0; j-- {
			if i == oldN && j == newN {
				continue
			}
			best := -1.0
			if i < oldN {
				best = dp[i+1][j]
				choice[i][j] = 'd'
			}
			if j < newN && dp[i][j+1] > best {
				best = dp[i][j+1]
				choice[i][j] = 'i'
			}
			if i < oldN && j < newN {
				if pairScore, ok := displayPairScore(oldLines[i], newLines[j], oldMarks[i], newMarks[j], scores[i][j]); ok {
					score := pairScore + dp[i+1][j+1]
					if score >= best {
						best = score
						choice[i][j] = 'p'
					}
				}
			}
			dp[i][j] = best
		}
	}
	var rows []sourceRow
	for i, j := 0, 0; i < oldN || j < newN; {
		switch choice[i][j] {
		case 'p':
			rows = append(rows, sourceRow{
				oldMark:  oldMarks[i],
				oldLine:  sourceLineNumber(oldBlock, i),
				oldText:  oldLines[i],
				oldParts: oldParts[i],
				newMark:  newMarks[j],
				newLine:  sourceLineNumber(newBlock, j),
				newText:  newLines[j],
				newParts: newParts[j],
			})
			i++
			j++
		case 'i':
			rows = append(rows, sourceRow{
				newMark:  newMarks[j],
				newLine:  sourceLineNumber(newBlock, j),
				newText:  newLines[j],
				newParts: newParts[j],
			})
			j++
		default:
			rows = append(rows, sourceRow{
				oldMark:  oldMarks[i],
				oldLine:  sourceLineNumber(oldBlock, i),
				oldText:  oldLines[i],
				oldParts: oldParts[i],
			})
			i++
		}
	}
	return separateDiffHunks(rows)
}

func separateDiffHunks(rows []sourceRow) []sourceRow {
	if len(rows) == 0 {
		return rows
	}
	out := make([]sourceRow, 0, len(rows)+4)
	seenChangedGroup := false
	inChangedGroup := false
	for _, row := range rows {
		changed := sourceRowChanged(row)
		if changed && !inChangedGroup {
			if seenChangedGroup {
				out = append(out, sourceRow{separator: true})
			}
			seenChangedGroup = true
			inChangedGroup = true
		} else if !changed {
			inChangedGroup = false
		}
		out = append(out, row)
	}
	return out
}

func sourceRowChanged(row sourceRow) bool {
	if row.separator {
		return false
	}
	if row.oldMark == "+" || row.oldMark == "-" || row.oldMark == "~" || row.newMark == "+" || row.newMark == "-" || row.newMark == "~" {
		return true
	}
	return sourcePartsChanged(row.oldParts) || sourcePartsChanged(row.newParts)
}

func sourcePartsChanged(parts []sourcePart) bool {
	for _, part := range parts {
		if strings.TrimSpace(part.Text) != "" && part.Kind != sourcePartEqual {
			return true
		}
	}
	return false
}

func hunkSeparatorLine(oldW, newW int) string {
	return padToWidth(hunkSeparatorSide(oldW), oldW) + " │ " + hunkSeparatorSide(newW)
}

func hunkSeparatorSide(width int) string {
	if width < 1 {
		return ""
	}
	label := "⋯ next change ⋯"
	if len([]rune(label)) >= width {
		return clipLine(label, width)
	}
	pad := width - len([]rune(label))
	left := pad / 2
	right := pad - left
	return strings.Repeat("─", left) + label + strings.Repeat("─", right)
}

type diffHunkInfo struct {
	AnchorLine int
	Title      string
}

func diffHunkInfos(pair *diffreview.Pair) []diffHunkInfo {
	if pair == nil {
		return nil
	}
	rows := memoizedSourceRows(pair)
	infos := make([]diffHunkInfo, 0, 4)
	inChangedGroup := false
	for _, row := range rows {
		changed := sourceRowChanged(row)
		if changed && !inChangedGroup {
			if off := sourceRowAnchorOffset(pair, row); off > 0 {
				infos = append(infos, diffHunkInfo{AnchorLine: off, Title: sourceRowSummary(row)})
			}
			inChangedGroup = true
		} else if !changed {
			inChangedGroup = false
		}
	}
	return infos
}

func sourceRowSummary(row sourceRow) string {
	for _, text := range []string{row.newText, row.oldText} {
		text = strings.TrimSpace(text)
		if text != "" {
			return text
		}
	}
	return "change"
}

func sourceRowAnchorOffset(pair *diffreview.Pair, row sourceRow) int {
	if pair == nil {
		return 0
	}
	if pair.New != nil && pair.New.StartLine > 0 && row.newLine > 0 {
		return row.newLine - pair.New.StartLine + 1
	}
	if pair.Old != nil && pair.Old.StartLine > 0 && row.oldLine > 0 {
		return row.oldLine - pair.Old.StartLine + 1
	}
	return 0
}

func displayPairScore(oldLine, newLine, oldMark, newMark string, score float64) (float64, bool) {
	if score > 0 {
		return score + 0.2, true
	}
	if strings.TrimSpace(oldLine) == strings.TrimSpace(newLine) {
		return 0.2, true
	}
	if oldMark != " " && newMark != " " {
		return 0.05, true
	}
	return 0, false
}

// appendPart carries Search alongside Kind: a run flagged by
// decorateSearchParts must survive wrapping, and it may only merge into a
// neighbour that agrees on both, or the highlight is folded back into the
// surrounding diff styling and never paints.
func appendPart(parts []sourcePart, kind sourcePartKind, search bool, text string) []sourcePart {
	if text == "" {
		return parts
	}
	if n := len(parts); n > 0 && parts[n-1].Kind == kind && parts[n-1].Search == search {
		parts[n-1].Text += text
		return parts
	}
	return append(parts, sourcePart{Text: text, Kind: kind, Search: search})
}

func tokenizeLatex(s string) []string {
	var tokens []string
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 0 {
			break
		}
		start := i
		switch {
		case r == '\\':
			i += size
			if i < len(s) {
				next, nextSize := utf8.DecodeRuneInString(s[i:])
				if unicode.IsLetter(next) {
					for i < len(s) {
						next, nextSize = utf8.DecodeRuneInString(s[i:])
						if !unicode.IsLetter(next) {
							break
						}
						i += nextSize
					}
				} else {
					i += nextSize
				}
			}
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			i += size
			for i < len(s) {
				next, nextSize := utf8.DecodeRuneInString(s[i:])
				if !unicode.IsLetter(next) && !unicode.IsDigit(next) {
					break
				}
				i += nextSize
			}
		case unicode.IsSpace(r):
			i += size
			for i < len(s) {
				next, nextSize := utf8.DecodeRuneInString(s[i:])
				if !unicode.IsSpace(next) {
					break
				}
				i += nextSize
			}
		default:
			i += size
		}
		tokens = append(tokens, s[start:i])
	}
	return tokens
}

func blockSourceLines(block *parser.Block) []string {
	if block == nil || block.Source == "" {
		return nil
	}
	source := strings.TrimSuffix(block.Source, "\n")
	if source == "" {
		return []string{""}
	}
	return strings.Split(source, "\n")
}

func sourceLineNumber(block *parser.Block, offset int) int {
	if block == nil || block.StartLine <= 0 {
		return 0
	}
	return block.StartLine + offset
}

func formatSourceCell(mark string, line int, text string) string {
	prefix := sourceCellPrefix(mark, line, false)
	return prefix + text
}

func renderSourceCell(mark string, line int, text string, parts []sourcePart, width int, oldSide bool, highlight bool, cursor bool) []string {
	if !highlight {
		return wrapSourceCell(mark, line, text, width, cursor)
	}
	return wrapSourceCellStyled(mark, line, text, parts, width, oldSide, cursor)
}

func wrapSourceCell(mark string, line int, text string, width int, cursor bool) []string {
	if width < 1 {
		width = 1
	}
	prefix := sourceCellPrefix(mark, line, cursor)
	prefixW := len([]rune(prefix))
	if prefixW >= width {
		return []string{clipLine(prefix+text, width)}
	}
	text = expandSourceTabs(text)
	textW := width - prefixW
	chunks := wrapTextRunes(text, textW)
	out := make([]string, 0, len(chunks))
	for i, chunk := range chunks {
		p := prefix
		if i > 0 {
			p = continuationPrefix(prefixW)
		}
		out = append(out, p+chunk)
	}
	return out
}

func wrapSourceCellStyled(mark string, line int, text string, parts []sourcePart, width int, oldSide bool, cursor bool) []string {
	if width < 1 {
		width = 1
	}
	prefix := sourceCellPrefix(mark, line, cursor)
	prefixW := len([]rune(prefix))
	if prefixW >= width {
		return []string{styleSourceParts([]sourcePart{{Text: clipLine(prefix+text, width)}}, mark, oldSide)}
	}
	if len(parts) == 0 {
		parts = []sourcePart{{Text: text, Kind: sourcePartEqual}}
	}
	parts = expandSourcePartTabs(parts)
	parts = decorateSearchParts(parts, renderSearchTerm)
	contentW := width - prefixW
	chunks := wrapPartsHard(parts, contentW)
	if len(chunks) == 0 {
		chunks = [][]sourcePart{{}}
	}
	out := make([]string, 0, len(chunks))
	for i, chunk := range chunks {
		p := prefix
		if i > 0 {
			p = continuationPrefix(prefixW)
		}
		lineParts := append([]sourcePart{{Text: p, Kind: sourcePartEqual}}, chunk...)
		if visiblePartsWidth(lineParts) < width {
			lineParts = append(lineParts, sourcePart{Text: strings.Repeat(" ", width-visiblePartsWidth(lineParts)), Kind: sourcePartEqual})
		}
		out = append(out, styleSourceParts(lineParts, mark, oldSide))
	}
	return out
}

// expandSourceTabs keeps source rows independent of the pane's absolute
// terminal column. A literal tab advances to the terminal's next tab stop, so
// counting it as one rune makes the painted row wider than the wrapper thought
// it was and clips characters against the pane border. Four spaces matches the
// formatter's space.tabs rule and gives both sides identical geometry.
func expandSourceTabs(text string) string {
	return strings.ReplaceAll(text, "\t", sourceTabReplacement)
}

func expandSourcePartTabs(parts []sourcePart) []sourcePart {
	var out []sourcePart
	for i, part := range parts {
		if !strings.ContainsRune(part.Text, '\t') {
			if out != nil {
				out = append(out, part)
			}
			continue
		}
		if out == nil {
			out = make([]sourcePart, 0, len(parts))
			out = append(out, parts[:i]...)
		}
		part.Text = expandSourceTabs(part.Text)
		out = append(out, part)
	}
	if out == nil {
		return parts
	}
	return out
}

func sourceCellPrefix(mark string, line int, cursor bool) string {
	if mark == "" {
		mark = " "
	}
	cursorMark := " "
	if cursor {
		cursorMark = ">"
	}
	if line > 0 {
		return fmt.Sprintf("%s%s%4d ", mark, cursorMark, line)
	}
	return fmt.Sprintf("%s%s     ", mark, cursorMark)
}

func continuationPrefix(width int) string {
	if width <= 0 {
		return ""
	}
	if width == 1 {
		return "·"
	}
	return strings.Repeat(" ", width-1) + "·"
}

func wrapTextRunes(text string, width int) []string {
	if width < 1 {
		width = 1
	}
	runes := []rune(text)
	if len(runes) == 0 {
		return []string{""}
	}
	out := make([]string, 0, (len(runes)+width-1)/width)
	for len(runes) > width {
		breakAt := wrapBreakIndex(runes, width)
		out = append(out, string(runes[:breakAt]))
		runes = trimLeadingWrapSpace(runes[breakAt:])
	}
	out = append(out, string(runes))
	return out
}

func wrapPartsHard(parts []sourcePart, width int) [][]sourcePart {
	if width < 1 {
		width = 1
	}
	var out [][]sourcePart
	var cur []sourcePart
	curW := 0
	flush := func() {
		out = append(out, cur)
		cur = nil
		curW = 0
	}
	for _, part := range parts {
		runes := []rune(part.Text)
		for len(runes) > 0 {
			space := width - curW
			if space <= 0 {
				flush()
				space = width
			}
			take := len(runes)
			if take > space {
				take = space
			}
			cur = appendPart(cur, part.Kind, part.Search, string(runes[:take]))
			curW += take
			runes = runes[take:]
			if curW >= width && len(runes) > 0 {
				flush()
			}
		}
	}
	if cur != nil || len(out) == 0 {
		out = append(out, cur)
	}
	return out
}

func visiblePartsWidth(parts []sourcePart) int {
	w := 0
	for _, part := range parts {
		w += len([]rune(part.Text))
	}
	return w
}

func wrapBreakIndex(runes []rune, width int) int {
	if len(runes) <= width {
		return len(runes)
	}
	for i := width; i > 0; i-- {
		if runes[i-1] == ' ' || runes[i-1] == '\t' {
			return i
		}
	}
	return width
}

func trimLeadingWrapSpace(runes []rune) []rune {
	for len(runes) > 0 && (runes[0] == ' ' || runes[0] == '\t') {
		runes = runes[1:]
	}
	return runes
}

func padToWidth(s string, width int) string {
	if width < 1 {
		return ""
	}
	runes := []rune(s)
	if len(runes) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(runes))
}

func visibleRenderedLines(lines []string, height int, anchor int) string {
	if height < 1 {
		height = 1
	}
	if len(lines) <= height {
		return strings.Join(lines, "\n")
	}
	start := visibleStart(len(lines), height, anchor)
	return strings.Join(lines[start:start+height], "\n")
}

// visibleStart computes the scroll window's first rendered line — shared
// by the renderers and the mouse click mapping so they cannot disagree.
func visibleStart(total, height, anchor int) int {
	if height < 1 {
		height = 1
	}
	if total <= height {
		return 0
	}
	start := 0
	if anchor >= 0 {
		start = anchor - height/3
	}
	if start < 0 {
		start = 0
	}
	if start > total-height {
		start = total - height
	}
	return start
}

func sourceRowMatchesAnchor(row sourceRow, rowIndex int, oldAnchorLine, newAnchorLine int) bool {
	if newAnchorLine > 0 && row.newLine == newAnchorLine {
		return true
	}
	if oldAnchorLine > 0 && row.oldLine == oldAnchorLine {
		return true
	}
	return rowIndex == 0 && oldAnchorLine <= 0 && newAnchorLine <= 0
}

func styleSourceParts(parts []sourcePart, mark string, oldSide bool) string {
	var b strings.Builder
	for _, part := range parts {
		b.WriteString(styleForSourcePartFull(part, mark, oldSide).Render(part.Text))
	}
	return b.String()
}

// searchMatchStyle paints / query hits. It is deliberately blue: every other
// colour in the source panes is warm (olive for a changed row, green for an
// added token, red for a deleted one), so a saturated blue with white text is
// the one thing that cannot be mistaken for diff styling underneath it.
var searchMatchStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Background(lipgloss.Color("33")).Bold(true)

func styleForSourcePartFull(part sourcePart, mark string, oldSide bool) lipgloss.Style {
	if part.Search {
		return searchMatchStyle
	}
	return styleForSourcePart(mark, part.Kind, oldSide)
}

func styleForSourcePart(mark string, kind sourcePartKind, oldSide bool) lipgloss.Style {
	switch kind {
	case sourcePartDelete:
		return diffDeleteTokenStyle
	case sourcePartAdd:
		return diffAddTokenStyle
	case sourcePartChange:
		if oldSide {
			return diffDeleteTokenStyle
		}
		return diffAddTokenStyle
	}
	switch mark {
	case "-":
		return diffDeleteLineStyle
	case "+":
		return diffAddLineStyle
	case "~":
		return diffChangeLineStyle
	default:
		return lipgloss.NewStyle()
	}
}
