package diffui

import (
	"strings"
	"testing"
)

// After jumping to the last pair, the ">" cursor must remain visible in the
// outline pane exactly as View() composes it (renderPane wraps renderOutline).
func TestOutlineCursorVisibleAtBottom(t *testing.T) {
	for _, h := range []int{12, 16, 20, 24, 30} {
		m := New(fixtureManyChangedReview(60), Options{})
		m.Width, m.Height = 120, h
		m = pressKey(t, m, "G") // ActionLast → cursor on the final pair

		bodyHeight := m.Height - statusBarHeight
		outlineW, _ := m.noPDFWidths(m.Width)
		pane := m.renderPane("Outline", m.renderOutline(outlineW-2, bodyHeight-2), outlineW, bodyHeight, true)

		if !strings.Contains(pane, ">") {
			t.Errorf("height=%d: cursor '>' not visible in outline after jumping to last pair:\n%s", h, pane)
		}
	}
}
