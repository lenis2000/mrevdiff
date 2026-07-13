package diffui

import "strings"

// overlayScrollThumb swaps the right-border │ for a heavy ┃ on the rows
// covered by the scroll thumb — position feedback without giving up a
// column (revdiff's applyPaneScrollbar pattern). pane is a fully rendered
// bordered pane; topSkip counts the non-scrolling rows above the body
// (border + title, plus any header). No-op when everything fits or the
// pane shape doesn't match expectations.
func overlayScrollThumb(pane string, total, visible, offset, topSkip int) string {
	if total <= visible || visible < 1 || offset < 0 {
		return pane
	}
	thumbLen := visible * visible / total
	if thumbLen < 1 {
		thumbLen = 1
	}
	maxStart := visible - thumbLen
	span := total - visible
	thumbStart := 0
	if span > 0 {
		thumbStart = (offset*maxStart + span/2) / span
	}
	if thumbStart > maxStart {
		thumbStart = maxStart
	}
	lines := strings.Split(pane, "\n")
	for i := 0; i < thumbLen; i++ {
		row := topSkip + thumbStart + i
		if row < 0 || row >= len(lines)-1 { // never touch the bottom border
			continue
		}
		if idx := strings.LastIndex(lines[row], "│"); idx >= 0 {
			lines[row] = lines[row][:idx] + "┃" + lines[row][idx+len("│"):]
		}
	}
	return strings.Join(lines, "\n")
}

// overlayOutlineScrollbar decorates the rendered outline pane with the
// scroll thumb, mirroring renderOutline's row-window math.
func (m Model) overlayOutlineScrollbar(pane string, bodyHeight int) string {
	rowHeight := bodyHeight - 4 // borders, title, stats header
	if rowHeight < 1 {
		return pane
	}
	rows := m.outlineRows()
	if len(rows) <= rowHeight {
		return pane
	}
	cursorRow := outlineCursorRow(rows, m.Cursor, m.SourceLineCursor)
	start := outlineScrollStart(len(rows), cursorRow, rowHeight)
	return overlayScrollThumb(pane, len(rows), rowHeight, start, 3)
}

// overlaySourceScrollbar decorates a rendered source pane, mirroring
// renderPairSourceSide's window math (the renderer is asked for
// paneHeight-2 lines; the pane displays one fewer).
func (m Model) overlaySourceScrollbar(pane string, oldSide bool, innerW, paneHeight int) string {
	windowH := paneHeight - 2
	if windowH < 1 {
		return pane
	}
	pair := m.CurrentDisplayPair()
	oldAnchor, newAnchor := m.sourceAnchorLines()
	if oldSide {
		newAnchor = 0
	} else {
		oldAnchor = 0
	}
	rendered, _, anchorRendered := sourceSideRenderedLines(pair, oldSide, innerW, oldAnchor, newAnchor, true)
	if len(rendered) <= windowH {
		return pane
	}
	start := visibleStart(len(rendered), windowH, anchorRendered)
	return overlayScrollThumb(pane, len(rendered), windowH, start, 2)
}
