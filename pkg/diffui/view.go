package diffui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"

	"mrevdiff/pkg/pdf"
)

const statusBarHeight = 1

const (
	defaultOutlineFrac     = 0.25
	defaultPDFFrac         = 0.25
	defaultStackedTopFrac  = 0.55
	defaultSourceSplitFrac = 0.50
	diffResizeStep         = 0.03

	minDiffOutlineFrac = 0.10
	maxDiffOutlineFrac = 0.50
	minDiffPDFFrac     = 0.10
	maxDiffPDFFrac     = 0.55
	minDiffSourceFrac  = 0.25

	minDiffStackedTopFrac  = 0.20
	maxDiffStackedTopFrac  = 0.85
	minDiffSourceSplitFrac = 0.20
	maxDiffSourceSplitFrac = 0.80
)

// View renders the diff outline, old/new source diff, PDF placeholder, and
// status bar.
func (m Model) View() string {
	if m.quitting {
		return ""
	}
	if m.Width <= 0 || m.Height <= 0 {
		return "loading..."
	}
	bodyHeight := m.Height - statusBarHeight
	if bodyHeight < 1 {
		bodyHeight = 1
	}

	var main string
	switch m.Layout {
	case LayoutStacked:
		outlineW, rightW := m.stackedWidths(m.Width)
		topH, pdfH := m.stackedHeights(bodyHeight)
		outline := m.renderPane("Outline", m.renderOutline(outlineW-2, bodyHeight-2), outlineW, bodyHeight, m.Focus == PaneOutline)
		comparison := m.renderComparisonArea(rightW, topH)
		pdf := m.renderPDFPane(pdfWMin(rightW), pdfH, m.Focus == PanePDF)
		right := lipgloss.JoinVertical(lipgloss.Left, comparison, pdf)
		main = lipgloss.JoinHorizontal(lipgloss.Top, outline, right)
	case LayoutNoPDF:
		outlineW, sourceW := m.noPDFWidths(m.Width)
		outline := m.renderPane("Outline", m.renderOutline(outlineW-2, bodyHeight-2), outlineW, bodyHeight, m.Focus == PaneOutline)
		comparison := m.renderComparisonArea(sourceW, bodyHeight)
		main = lipgloss.JoinHorizontal(lipgloss.Top, outline, comparison)
	default:
		outlineW, sourceW, pdfW := m.paneWidths(m.Width)
		outline := m.renderPane("Outline", m.renderOutline(outlineW-2, bodyHeight-2), outlineW, bodyHeight, m.Focus == PaneOutline)
		comparison := m.renderComparisonArea(sourceW, bodyHeight)
		pdf := m.renderPDFPane(pdfW, bodyHeight, m.Focus == PanePDF)
		main = lipgloss.JoinHorizontal(lipgloss.Top, outline, comparison, pdf)
	}
	status := clipLine(m.statusText(), m.Width)
	view := lipgloss.JoinVertical(lipgloss.Left, main, status)
	if m.Layout == LayoutNoPDF && m.KittyAvailable {
		// Kitty graphics persist independently of the terminal text grid. When the
		// PDF pane is hidden, explicitly clear any image rendered by a previous
		// layout; otherwise the stale crop remains over the source panes.
		return pdf.KittyDeleteAll + view
	}
	return view
}

func (m Model) renderComparisonArea(width, height int) string {
	if m.ShowHelp {
		return m.renderPane("Help", RenderHelpBody(width-2, m.AllowModifications), width, height, m.Focus == PaneOldSource || m.Focus == PaneNewSource)
	}
	if m.LineEdit != nil {
		return m.renderPane("Line Edit", m.renderLineEditBody(width-2, height-2), width, height, m.Focus == PaneOldSource || m.Focus == PaneNewSource)
	}
	if m.Popup != nil {
		return m.renderPane("Annotation", m.Popup.TA.View(), width, height, m.Focus == PaneOldSource || m.Focus == PaneNewSource)
	}
	oldAnchor, newAnchor := m.sourceAnchorLines()
	pair := m.CurrentDisplayPair()
	if m.Layout != LayoutStacked && comparisonCombined(width) {
		body := RenderPairSourceHighlighted(pair, width-2, height-2, oldAnchor, newAnchor)
		return m.renderPaneRaw("Source", body, width, height, m.Focus == PaneOldSource || m.Focus == PaneNewSource)
	}
	oldW, newW := m.sourcePaneWidths(width)
	// In split panes, each side owns its own scroll anchor. Passing the new
	// anchor into the old pane (or conversely) lets inserted/deleted placeholder
	// rows steal the match and makes the visible source window jump around.
	oldBody := RenderPairSourceSideHighlighted(pair, true, oldW-2, height-2, oldAnchor, 0)
	newBody := RenderPairSourceSideHighlighted(pair, false, newW-2, height-2, 0, newAnchor)
	oldSource := m.renderPaneRaw("Old source", oldBody, oldW, height, m.Focus == PaneOldSource)
	newSource := m.renderPaneRaw("New source", newBody, newW, height, m.Focus == PaneNewSource)
	return lipgloss.JoinHorizontal(lipgloss.Top, oldSource, newSource)
}

func (m Model) renderLineEditBody(innerW, innerH int) string {
	if m.LineEdit == nil {
		return ""
	}
	if innerW < 1 {
		innerW = 1
	}
	if innerH < 2 {
		innerH = 2
	}
	hint := fmt.Sprintf("line %d · Enter submit · Esc cancel", m.LineEdit.AbsoluteLine)
	editorH := innerH - 1
	if editorH < 1 {
		editorH = 1
	}
	m.LineEdit.TA.SetWidth(innerW)
	m.LineEdit.TA.SetHeight(editorH)
	return m.LineEdit.TA.View() + "\n" + hint
}

func (m Model) renderPane(title, body string, width, height int, focusedOpt ...bool) string {
	focused := len(focusedOpt) > 0 && focusedOpt[0]
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	innerW := width - 2
	if innerW < 1 {
		innerW = 1
	}
	innerH := height - 2
	if innerH < 1 {
		innerH = 1
	}
	content := title
	if body != "" {
		content += "\n" + fitLines(body, innerW, innerH-1)
	}
	style := m.Styles.Pane
	if focused {
		style = m.Styles.PaneFocused
	}
	return style.Width(innerW).Height(innerH).Border(lipgloss.NormalBorder()).Render(content)
}

func (m Model) renderPaneRaw(title, body string, width, height int, focused bool) string {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	innerW := width - 2
	if innerW < 1 {
		innerW = 1
	}
	innerH := height - 2
	if innerH < 1 {
		innerH = 1
	}
	contentLines := []string{clipLine(title, innerW)}
	bodyH := innerH - 1
	if bodyH > 0 {
		// Source panes pre-wrap and ANSI-style their rows before they get here.
		// Normalize every raw row to exactly the pane width and body height; any
		// over-wide styled row would otherwise wrap at the terminal layer and push
		// that pane's bottom border away from its sibling.
		contentLines = append(contentLines, fitRawLines(body, innerW, bodyH)...)
	}
	for len(contentLines) < innerH {
		contentLines = append(contentLines, strings.Repeat(" ", innerW))
	}
	for i := range contentLines {
		contentLines[i] = padANSIToWidth(contentLines[i], innerW)
	}
	style := m.Styles.Pane
	if focused {
		style = m.Styles.PaneFocused
	}
	return style.Width(innerW).Height(innerH).Border(lipgloss.NormalBorder()).Render(strings.Join(contentLines, "\n"))
}

func (m Model) renderPDFPane(width, height int, focusedOpt ...bool) string {
	focused := len(focusedOpt) > 0 && focusedOpt[0]
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	innerW := width - 2
	if innerW < 1 {
		innerW = 1
	}
	innerH := height - 2
	if innerH < 1 {
		innerH = 1
	}
	content := "PDF"
	if body := m.pdfPaneBody(); body != "" {
		content += "\n" + body
	}
	style := m.Styles.Pane
	if focused {
		style = m.Styles.PaneFocused
	}
	return style.Width(innerW).Height(innerH).Border(lipgloss.NormalBorder()).Render(content)
}

func (m Model) paneWidths(width int) (outline, source, pdf int) {
	if width < 3 {
		return 1, 1, 1
	}
	outlineFrac, pdfFrac, _, _ := m.layoutValues()
	outline = int(float64(width) * outlineFrac)
	pdf = int(float64(width) * pdfFrac)
	source = width - outline - pdf
	if outline < 1 {
		outline = 1
	}
	if source < 1 {
		source = 1
	}
	if pdf < 1 {
		pdf = 1
	}
	return outline, source, pdf
}

// paneWidths keeps the original package-level splitter available for tests and
// small helpers that do not care about user-resized model state.
func paneWidths(width int) (outline, oldSource, newSource, source, pdf int, combined bool) {
	m := Model{}
	outline, source, pdf = m.paneWidths(width)
	combined = comparisonCombined(source)
	if !combined {
		oldSource, newSource = m.sourcePaneWidths(source)
	}
	return outline, oldSource, newSource, source, pdf, combined
}

func (m Model) stackedWidths(width int) (outline, right int) {
	if width < 2 {
		return 1, 1
	}
	outlineFrac, _, _, _ := m.layoutValues()
	outline = int(float64(width) * outlineFrac)
	if outline < 1 {
		outline = 1
	}
	right = width - outline
	if right < 1 {
		right = 1
	}
	return outline, right
}

func (m Model) noPDFWidths(width int) (outline, source int) {
	return m.stackedWidths(width)
}

func (m Model) stackedHeights(height int) (top, bottom int) {
	if height < 2 {
		return 1, 1
	}
	_, _, stackedTopFrac, _ := m.layoutValues()
	top = int(float64(height) * stackedTopFrac)
	if top < 1 {
		top = 1
	}
	if top >= height {
		top = height - 1
	}
	bottom = height - top
	if bottom < 1 {
		bottom = 1
	}
	return top, bottom
}

func (m Model) sourceAnchorLines() (oldLine, newLine int) {
	pair := m.CurrentDisplayPair()
	if pair == nil {
		return 0, 0
	}
	offset := m.SourceLineCursor
	if offset < 1 {
		offset = 1
	}
	if pair.Old != nil && pair.Old.StartLine > 0 {
		oldOffset := offset
		if count := len(blockSourceLines(pair.Old)); count > 0 && oldOffset > count {
			oldOffset = count
		}
		oldLine = pair.Old.StartLine + oldOffset - 1
	}
	if pair.New != nil && pair.New.StartLine > 0 {
		newOffset := offset
		if count := len(blockSourceLines(pair.New)); count > 0 && newOffset > count {
			newOffset = count
		}
		newLine = pair.New.StartLine + newOffset - 1
	}
	return oldLine, newLine
}

func (m Model) sourcePaneWidths(width int) (oldSource, newSource int) {
	if width < 2 {
		return 1, 1
	}
	_, _, _, split := m.layoutValues()
	oldSource = int(float64(width) * split)
	if oldSource < 1 {
		oldSource = 1
	}
	newSource = width - oldSource
	if newSource < 1 {
		newSource = 1
	}
	return oldSource, newSource
}

func comparisonCombined(width int) bool {
	return width < 70
}

func pdfWMin(width int) int {
	if width < 1 {
		return 1
	}
	return width
}

func (m Model) layoutValues() (outlineFrac, pdfFrac, stackedTopFrac, sourceSplitFrac float64) {
	outlineFrac = m.OutlineFrac
	if outlineFrac <= 0 {
		outlineFrac = defaultOutlineFrac
	}
	pdfFrac = m.PDFFrac
	if pdfFrac <= 0 {
		pdfFrac = defaultPDFFrac
	}
	stackedTopFrac = m.StackedTopFrac
	if stackedTopFrac <= 0 {
		stackedTopFrac = defaultStackedTopFrac
	}
	sourceSplitFrac = m.SourceSplitFrac
	if sourceSplitFrac <= 0 {
		sourceSplitFrac = defaultSourceSplitFrac
	}
	return clampLayoutValues(outlineFrac, pdfFrac, stackedTopFrac, sourceSplitFrac)
}

func (m *Model) ensureLayoutDefaults() {
	m.OutlineFrac, m.PDFFrac, m.StackedTopFrac, m.SourceSplitFrac = m.layoutValues()
}

func (m *Model) cycleLayout() {
	switch m.Layout {
	case LayoutThreeCol:
		m.Layout = LayoutStacked
		m.Status = "layout: PDF below source"
	case LayoutStacked:
		m.Layout = LayoutNoPDF
		if m.Focus == PanePDF {
			m.Focus = PaneNewSource
		}
		m.Status = "layout: PDF hidden"
	default:
		m.Layout = LayoutThreeCol
		m.Status = "layout: side-by-side"
	}
}

func (m Model) focusOrder() []Pane {
	if m.Layout == LayoutNoPDF {
		return []Pane{PaneOutline, PaneOldSource, PaneNewSource}
	}
	return []Pane{PaneOutline, PaneOldSource, PaneNewSource, PanePDF}
}

func (m *Model) moveFocus(delta int) {
	order := m.focusOrder()
	pos := 0
	for i, pane := range order {
		if pane == m.Focus {
			pos = i
			break
		}
	}
	pos += delta
	if pos < 0 {
		pos = 0
	}
	if pos >= len(order) {
		pos = len(order) - 1
	}
	m.Focus = order[pos]
	m.Status = "focus: " + m.Focus.String()
}

func (m *Model) resizeFocusedPane(delta int) bool {
	if delta == 0 {
		return false
	}
	m.ensureLayoutDefaults()
	before := [4]float64{m.OutlineFrac, m.PDFFrac, m.StackedTopFrac, m.SourceSplitFrac}
	step := diffResizeStep * float64(delta)
	switch m.Focus {
	case PaneOutline:
		m.OutlineFrac += step
	case PanePDF:
		if m.Layout == LayoutNoPDF {
			return false
		}
		if m.Layout == LayoutStacked {
			// Top share shrinks when the focused bottom PDF grows.
			m.StackedTopFrac -= step
		} else {
			m.PDFFrac += step
		}
	case PaneOldSource:
		m.SourceSplitFrac += step
	case PaneNewSource:
		m.SourceSplitFrac -= step
	}
	m.OutlineFrac, m.PDFFrac, m.StackedTopFrac, m.SourceSplitFrac = clampLayoutValues(
		m.OutlineFrac,
		m.PDFFrac,
		m.StackedTopFrac,
		m.SourceSplitFrac,
	)
	return before != [4]float64{m.OutlineFrac, m.PDFFrac, m.StackedTopFrac, m.SourceSplitFrac}
}

func clampLayoutValues(outlineFrac, pdfFrac, stackedTopFrac, sourceSplitFrac float64) (float64, float64, float64, float64) {
	outlineFrac = clampFloat(outlineFrac, minDiffOutlineFrac, maxDiffOutlineFrac)
	pdfFrac = clampFloat(pdfFrac, minDiffPDFFrac, maxDiffPDFFrac)
	if 1-outlineFrac-pdfFrac < minDiffSourceFrac {
		need := minDiffSourceFrac - (1 - outlineFrac - pdfFrac)
		if pdfFrac-need >= minDiffPDFFrac {
			pdfFrac -= need
		} else if outlineFrac-need >= minDiffOutlineFrac {
			outlineFrac -= need
		} else {
			outlineFrac = minDiffOutlineFrac
			pdfFrac = 1 - outlineFrac - minDiffSourceFrac
		}
	}
	stackedTopFrac = clampFloat(stackedTopFrac, minDiffStackedTopFrac, maxDiffStackedTopFrac)
	sourceSplitFrac = clampFloat(sourceSplitFrac, minDiffSourceSplitFrac, maxDiffSourceSplitFrac)
	return outlineFrac, pdfFrac, stackedTopFrac, sourceSplitFrac
}

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// RenderHelpBody returns the diff-specific help text.
func RenderHelpBody(width int, allowModifications bool) string {
	lines := []string{
		"j/k, ↑/↓ move by change pair; source panes scroll; counts work (10j/5k)",
		"J/K jump 10 down / 5 up pairs",
		"gg/G first/last pair",
		"{/} previous/next section",
		"f cycle filter",
		"m toggle semantic/coalesced diff mode",
		"z fold/unfold current outline group",
		"space mark reviewed",
		"a annotate pair",
		"ctrl+a edit annotation",
		"d delete annotation",
		"y copy selected change (old/new side follows focus)",
		"e/E edit new file only when --allow-modifications is supplied",
		"[/] select previous/next source line (PDF anchor)",
		"h/l or ←/→ focus pane",
		"< / > resize focused pane or source split",
		"\\ cycle PDF layout: side pane / below / hidden",
		"u undo last diff-mode edit",
		"ctrl+r redo undone diff-mode edit",
		"B reload source diff and rebuild/reload new PDF after edits",
		"S sync/open new PDF in Skim at selected line (s also works)",
		"P open new PDF in Preview",
		"C opens old+new in external compare",
		"? close help",
		"q quit (save sidecar + emit annotations)",
		"Q Q discard session changes and quit (no save, no emit)",
	}
	if !allowModifications {
		lines = append(lines, "editing is currently disabled")
	}
	for i, line := range lines {
		lines[i] = clipLine(line, width)
	}
	return strings.Join(lines, "\n")
}

func fitLines(text string, width, height int) string {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	lines := strings.Split(text, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for i, line := range lines {
		lines[i] = clipLine(line, width)
	}
	return strings.Join(lines, "\n")
}

func fitRawLines(text string, width, height int) []string {
	if height < 1 {
		return nil
	}
	lines := strings.Split(text, "\n")
	if text == "" {
		lines = nil
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	for i := range lines {
		lines[i] = padANSIToWidth(lines[i], width)
	}
	return lines
}

func padANSIToWidth(s string, width int) string {
	if width < 1 {
		return ""
	}
	s, w := truncateANSIToWidth(s, width)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

func truncateANSIToWidth(s string, width int) (string, int) {
	if width < 1 {
		return "", 0
	}
	w := 0
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '\x1b' {
			j := skipANSIEscape(s, i)
			b.WriteString(s[i:j])
			i = j
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if size <= 0 {
			break
		}
		if r == utf8.RuneError && size == 1 {
			if w+1 > width {
				break
			}
			b.WriteByte(s[i])
			i++
			w++
			continue
		}
		if w+1 > width {
			break
		}
		b.WriteString(s[i : i+size])
		i += size
		w++
	}
	return b.String(), w
}

func ansiVisibleWidth(s string) int {
	w := 0
	for i := 0; i < len(s); {
		if s[i] == '\x1b' {
			i = skipANSIEscape(s, i)
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if size <= 0 {
			break
		}
		if r == utf8.RuneError && size == 1 {
			i++
			w++
			continue
		}
		w++
		i += size
	}
	return w
}

func skipANSIEscape(s string, i int) int {
	if i+1 >= len(s) {
		return len(s)
	}
	switch s[i+1] {
	case '[':
		j := i + 2
		for j < len(s) {
			b := s[j]
			j++
			if b >= 0x40 && b <= 0x7e {
				return j
			}
		}
		return len(s)
	case '_', ']', 'P', '^':
		j := i + 2
		for j+1 < len(s) {
			if s[j] == '\x1b' && s[j+1] == '\\' {
				return j + 2
			}
			j++
		}
		return len(s)
	default:
		return i + 2
	}
}

func clipLine(line string, width int) string {
	if width < 1 {
		return ""
	}
	runes := []rune(line)
	if len(runes) <= width {
		return line
	}
	if width == 1 {
		return string(runes[:1])
	}
	return string(runes[:width-1]) + "…"
}
