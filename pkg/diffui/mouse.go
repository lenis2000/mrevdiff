package diffui

import tea "github.com/charmbracelet/bubbletea"

type mouseWheelEdgeState struct {
	Active           bool
	Pane             Pane
	Delta            int
	Cursor           int
	SourceLineCursor int
}

func mouseWheelDelta(button tea.MouseButton) (int, bool) {
	switch button {
	case tea.MouseButtonWheelUp:
		return -1, true
	case tea.MouseButtonWheelDown:
		return +1, true
	default:
		return 0, false
	}
}

// ShouldDropMouseWheel reports whether msg repeats a mouse-wheel event that
// already hit a scroll edge. It is intended for tea.WithFilter so the message
// can be discarded before Bubble Tea recomputes the whole view.
func (m Model) ShouldDropMouseWheel(msg tea.MouseMsg) bool {
	delta, ok := mouseWheelDelta(msg.Button)
	if !ok || !m.mouseWheelEdge.Active {
		return false
	}
	pane, ok := m.paneAtPoint(msg.X, msg.Y)
	if !ok {
		return false
	}
	edge := m.mouseWheelEdge
	return edge.Pane == pane &&
		edge.Delta == delta &&
		edge.Cursor == m.Cursor &&
		edge.SourceLineCursor == m.SourceLineCursor
}

// handleMouse maps mouse wheel events to the pane under the pointer. Outline
// and PDF wheel move between semantic pairs; old/new source wheel scrolls only
// within the selected pair. Left clicks only update focus for now.
func (m Model) handleMouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	if m.Width <= 0 || m.Height <= 0 {
		return m, nil
	}
	isWheel := msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown
	if msg.Action != tea.MouseActionPress && !isWheel {
		return m, nil
	}
	pane, ok := m.paneAtPoint(msg.X, msg.Y)
	if !ok {
		m.mouseWheelEdge = mouseWheelEdgeState{}
		return m, nil
	}
	switch msg.Button {
	case tea.MouseButtonLeft:
		m.mouseWheelEdge = mouseWheelEdgeState{}
		m.Focus = pane
		switch pane {
		case PaneOutline:
			if next, cmd, handled := m.clickOutlineRow(msg.Y); handled {
				return next, cmd
			}
		case PaneOldSource, PaneNewSource:
			if next, handled := m.clickSourceLine(pane, msg.X, msg.Y); handled {
				return next, nil
			}
		}
		m.Status = "focus: " + m.Focus.String()
		return m, nil
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
		delta, _ := mouseWheelDelta(msg.Button)
		m.Focus = pane
		return m.applyMouseWheel(pane, delta)
	default:
		m.mouseWheelEdge = mouseWheelEdgeState{}
		return m, nil
	}
}

func (m Model) applyMouseWheel(pane Pane, delta int) (Model, tea.Cmd) {
	beforeCursor, beforeLine := m.Cursor, m.SourceLineCursor
	switch pane {
	case PaneOldSource, PaneNewSource:
		m = m.scrollFocusedSource(delta)
	case PaneOutline, PanePDF:
		m.moveDiffChunkOrPair(delta)
	}
	cursorMoved := m.Cursor != beforeCursor
	lineMoved := m.SourceLineCursor != beforeLine
	if !cursorMoved && !lineMoved {
		m.mouseWheelEdge = mouseWheelEdgeState{
			Active:           true,
			Pane:             pane,
			Delta:            delta,
			Cursor:           m.Cursor,
			SourceLineCursor: m.SourceLineCursor,
		}
		return m, nil
	}
	m.mouseWheelEdge = mouseWheelEdgeState{}
	if cursorMoved {
		return m.withPDFRender()
	}
	return m, nil
}

func (m Model) scrollFocusedSource(delta int) Model {
	if delta == 0 {
		return m
	}
	count, startLine, side := sourceLineTarget(m.CurrentDisplayPair(), m.Focus == PaneOldSource)
	if count == 0 {
		m.Status = "no source line for current pair"
		return m
	}
	if m.SourceLineCursor < 1 {
		m.SourceLineCursor = 1
	}
	m.SourceLineCursor += delta
	if m.SourceLineCursor < 1 {
		m.SourceLineCursor = 1
	}
	if m.SourceLineCursor > count {
		m.SourceLineCursor = count
	}
	m.Status = fmtSourceLineStatus(side, startLine+m.SourceLineCursor-1)
	return m
}

func (m Model) paneAtPoint(x, y int) (Pane, bool) {
	bodyH := m.Height - statusBarHeight
	if x < 0 || y < 0 || y >= bodyH {
		return PaneOutline, false
	}
	switch m.Layout {
	case LayoutStacked:
		outlineW, rightW := m.stackedWidths(m.Width)
		if x < outlineW {
			return PaneOutline, true
		}
		relX := x - outlineW
		if relX < 0 || relX >= rightW {
			return PaneOutline, false
		}
		topH, _ := m.stackedHeights(bodyH)
		if y >= topH {
			return PanePDF, true
		}
		oldW, _ := m.sourcePaneWidths(rightW)
		if relX < oldW {
			return PaneOldSource, true
		}
		return PaneNewSource, true
	case LayoutNoPDF:
		outlineW, sourceW := m.noPDFWidths(m.Width)
		if x < outlineW {
			return PaneOutline, true
		}
		relX := x - outlineW
		if relX < 0 || relX >= sourceW {
			return PaneOutline, false
		}
		if comparisonCombined(sourceW) {
			if relX < sourceW/2 {
				return PaneOldSource, true
			}
			return PaneNewSource, true
		}
		oldW, _ := m.sourcePaneWidths(sourceW)
		if relX < oldW {
			return PaneOldSource, true
		}
		return PaneNewSource, true
	case LayoutSourcesPDF:
		topH, _ := m.stackedHeights(bodyH)
		if y >= topH {
			return PanePDF, true
		}
		if comparisonCombined(m.Width) {
			if x < m.Width/2 {
				return PaneOldSource, true
			}
			return PaneNewSource, true
		}
		oldW, _ := m.sourcePaneWidths(m.Width)
		if x < oldW {
			return PaneOldSource, true
		}
		return PaneNewSource, true
	case LayoutNewPDF:
		sourceW, _ := m.newPDFWidths(m.Width)
		if x < sourceW {
			return PaneNewSource, true
		}
		return PanePDF, true
	case LayoutPDFOnly:
		return PanePDF, true
	}
	outlineW, sourceW, _ := m.paneWidths(m.Width)
	if x < outlineW {
		return PaneOutline, true
	}
	if x < outlineW+sourceW {
		relX := x - outlineW
		if comparisonCombined(sourceW) {
			if relX < sourceW/2 {
				return PaneOldSource, true
			}
			return PaneNewSource, true
		}
		oldW, _ := m.sourcePaneWidths(sourceW)
		if relX < oldW {
			return PaneOldSource, true
		}
		return PaneNewSource, true
	}
	return PanePDF, true
}

// clickOutlineRow jumps to the outline row under the pointer: a pair row
// moves the cursor there (like j/k), a group header toggles its fold
// (like z). Follows the docviewer pattern: reuse the renderer's own
// scroll math so the hit test can never disagree with what is drawn.
func (m Model) clickOutlineRow(y int) (Model, tea.Cmd, bool) {
	// Rows begin below the pane border, the pane title, and the stats
	// header; the row viewport height mirrors renderOutline.
	const rowsTop = 3
	bodyH := m.Height - statusBarHeight
	rowHeight := bodyH - 4
	if y < rowsTop || rowHeight < 1 {
		return m, nil, false
	}
	rows := m.outlineRows()
	if len(rows) == 0 {
		return m, nil, false
	}
	cursorRow := outlineCursorRow(rows, m.Cursor, m.SourceLineCursor)
	idx := outlineScrollStart(len(rows), cursorRow, rowHeight) + (y - rowsTop)
	if idx >= len(rows) || y-rowsTop >= rowHeight {
		return m, nil, false
	}
	row := rows[idx]
	if row.Group {
		if row.GroupKey == "" {
			return m, nil, false
		}
		if m.Collapsed == nil {
			m.Collapsed = map[string]bool{}
		}
		label := row.Title
		if len(row.GroupPath) > 0 {
			label = row.GroupPath[len(row.GroupPath)-1]
		}
		if m.Collapsed[row.GroupKey] {
			delete(m.Collapsed, row.GroupKey)
			m.Status = "unfolded " + label
		} else {
			m.Collapsed[row.GroupKey] = true
			m.Status = "folded " + label
		}
		return m, nil, true
	}
	if row.PairIndex < 0 || m.Review == nil || row.PairIndex >= len(m.Review.Pairs) {
		return m, nil, false
	}
	m.Cursor = row.PairIndex
	m.SourceLineCursor = row.AnchorLine
	m.snapSourceLine()
	m.Status = ""
	next, cmd := m.withPDFRender()
	return next, cmd, true
}

// clickSourceLine places the source-line cursor on the line under the
// pointer in a source pane.
func (m Model) clickSourceLine(pane Pane, x, y int) (Model, bool) {
	geo, ok := m.sourcePaneGeometry(pane)
	if !ok || m.LineEdit != nil || m.Popup != nil {
		return m, false
	}
	// Content rows start below the pane border and title line.
	const rowsTop = 2
	rowInView := y - rowsTop
	viewH := geo.height - 3 // top border + title + bottom border
	if rowInView < 0 || rowInView >= viewH {
		return m, false
	}
	pair := m.CurrentDisplayPair()
	oldAnchor, newAnchor := m.sourceAnchorLines()
	oldSide := pane == PaneOldSource
	var absLine int
	var found bool
	if geo.combined {
		contentX := x - geo.x0 - 1
		oldW := (geo.width - 2 - 3) / 2
		oldSide = contentX <= oldW
		absLine, found = sourceCombinedLineAtRow(pair, geo.width-2, viewH, oldAnchor, newAnchor, rowInView, oldSide)
	} else if oldSide {
		absLine, found = sourceSideLineAtRow(pair, true, geo.width-2, viewH, oldAnchor, 0, rowInView)
	} else {
		absLine, found = sourceSideLineAtRow(pair, false, geo.width-2, viewH, 0, newAnchor, rowInView)
	}
	if !found {
		return m, false
	}
	if geo.combined {
		if oldSide {
			m.Focus = PaneOldSource
		} else {
			m.Focus = PaneNewSource
		}
	}
	count, startLine, side := sourceLineTarget(pair, oldSide)
	if count == 0 {
		return m, false
	}
	cursor := absLine - startLine + 1
	if cursor < 1 {
		cursor = 1
	}
	if cursor > count {
		cursor = count
	}
	m.SourceLineCursor = cursor
	m.Status = fmtSourceLineStatus(side, startLine+cursor-1)
	return m, true
}

// sourcePaneGeometry returns where a source pane sits on screen, mirroring
// paneAtPoint's layout math in the opposite direction.
type paneGeometry struct {
	x0, width, height int
	combined          bool
}

func (m Model) sourcePaneGeometry(pane Pane) (paneGeometry, bool) {
	bodyH := m.Height - statusBarHeight
	if bodyH < 1 {
		return paneGeometry{}, false
	}
	switch m.Layout {
	case LayoutStacked:
		outlineW, rightW := m.stackedWidths(m.Width)
		topH, _ := m.stackedHeights(bodyH)
		oldW, newW := m.sourcePaneWidths(rightW)
		if pane == PaneOldSource {
			return paneGeometry{x0: outlineW, width: oldW, height: topH}, true
		}
		return paneGeometry{x0: outlineW + oldW, width: newW, height: topH}, true
	case LayoutNoPDF:
		outlineW, sourceW := m.noPDFWidths(m.Width)
		if comparisonCombined(sourceW) {
			return paneGeometry{x0: outlineW, width: sourceW, height: bodyH, combined: true}, true
		}
		oldW, newW := m.sourcePaneWidths(sourceW)
		if pane == PaneOldSource {
			return paneGeometry{x0: outlineW, width: oldW, height: bodyH}, true
		}
		return paneGeometry{x0: outlineW + oldW, width: newW, height: bodyH}, true
	case LayoutSourcesPDF:
		topH, _ := m.stackedHeights(bodyH)
		if comparisonCombined(m.Width) {
			return paneGeometry{x0: 0, width: m.Width, height: topH, combined: true}, true
		}
		oldW, newW := m.sourcePaneWidths(m.Width)
		if pane == PaneOldSource {
			return paneGeometry{x0: 0, width: oldW, height: topH}, true
		}
		return paneGeometry{x0: oldW, width: newW, height: topH}, true
	case LayoutNewPDF:
		if pane != PaneNewSource {
			return paneGeometry{}, false
		}
		sourceW, _ := m.newPDFWidths(m.Width)
		return paneGeometry{x0: 0, width: sourceW, height: bodyH}, true
	case LayoutPDFOnly:
		return paneGeometry{}, false
	}
	outlineW, sourceW, _ := m.paneWidths(m.Width)
	if comparisonCombined(sourceW) {
		return paneGeometry{x0: outlineW, width: sourceW, height: bodyH, combined: true}, true
	}
	oldW, newW := m.sourcePaneWidths(sourceW)
	if pane == PaneOldSource {
		return paneGeometry{x0: outlineW, width: oldW, height: bodyH}, true
	}
	return paneGeometry{x0: outlineW + oldW, width: newW, height: bodyH}, true
}
