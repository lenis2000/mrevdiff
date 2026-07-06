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
