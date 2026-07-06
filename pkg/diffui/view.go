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

	// Help is a full-screen overlay, not pane content: kitty images live on
	// their own plane, so retire the painted frame while the overlay is up
	// (the repaint on close re-transmits it).
	if m.ShowHelp {
		help := m.renderHelpOverlay(m.Width, bodyHeight)
		view := lipgloss.JoinVertical(lipgloss.Left, help, clipLine(m.statusText(), m.Width))
		if m.KittyAvailable {
			return m.kittyClear() + view
		}
		return view
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
	case LayoutSourcesPDF:
		topH, pdfH := m.stackedHeights(bodyHeight)
		comparison := m.renderComparisonArea(m.Width, topH)
		pdfPane := m.renderPDFPane(m.Width, pdfH, m.Focus == PanePDF)
		main = lipgloss.JoinVertical(lipgloss.Left, comparison, pdfPane)
	case LayoutNewPDF:
		sourceW, pdfW := m.newPDFWidths(m.Width)
		source := m.renderNewSourceArea(sourceW, bodyHeight)
		pdfPane := m.renderPDFPane(pdfW, bodyHeight, m.Focus == PanePDF)
		main = lipgloss.JoinHorizontal(lipgloss.Top, source, pdfPane)
	case LayoutPDFOnly:
		main = m.renderPDFPane(m.Width, bodyHeight, true)
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

// renderNewSourceArea renders just the new side of the current pair — the
// LayoutNewPDF reading/edit mode. Shares the overlay behavior (help, line
// edit, annotation popup) with renderComparisonArea.
func (m Model) renderNewSourceArea(width, height int) string {
	if m.LineEdit != nil {
		return m.renderPane("Line Edit", m.renderLineEditBody(width-2, height-2), width, height, m.Focus == PaneOldSource || m.Focus == PaneNewSource)
	}
	if m.Popup != nil {
		return m.renderPane("Annotation", m.Popup.TA.View(), width, height, m.Focus == PaneOldSource || m.Focus == PaneNewSource)
	}
	_, newAnchor := m.sourceAnchorLines()
	pair := m.CurrentDisplayPair()
	body := RenderPairSourceSideHighlighted(pair, false, width-2, height-2, 0, newAnchor)
	return m.renderPaneRaw("New source", body, width, height, m.Focus == PaneNewSource)
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

// newPDFWidths splits the terminal between the new-source pane and the PDF
// pane for LayoutNewPDF. Derived from PDFFrac (so </> resizing carries
// over) but re-centered: with only two panes the PDF deserves roughly half
// the width, not the sliver it gets as a fourth column.
func (m Model) newPDFWidths(width int) (source, pdf int) {
	if width < 2 {
		return 1, 1
	}
	_, pdfFrac, _, _ := m.layoutValues()
	frac := clampFloat(pdfFrac*2, 0.30, 0.70)
	pdf = int(float64(width) * frac)
	if pdf < 1 {
		pdf = 1
	}
	source = width - pdf
	if source < 1 {
		source = 1
	}
	return source, pdf
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

// layoutName is the status-line label for each layout.
func layoutName(l LayoutMode) string {
	switch l {
	case LayoutThreeCol:
		return "full — PDF side"
	case LayoutStacked:
		return "full — PDF below"
	case LayoutSourcesPDF:
		return "sources + PDF (outline hidden)"
	case LayoutNewPDF:
		return "new + PDF"
	case LayoutNoPDF:
		return "sources only (PDF hidden)"
	case LayoutPDFOnly:
		return "PDF only"
	}
	return "?"
}

// cycleLayout steps through the five working layouts:
//
//	full/PDF-side → full/PDF-below → sources+PDF → new+PDF → sources-only →
//
// The `|` PDF-only zoom sits outside the cycle; pressing `\` inside it
// drops back to the interrupted layout.
func (m *Model) cycleLayout() {
	switch m.Layout {
	case LayoutThreeCol:
		m.Layout = LayoutStacked
	case LayoutStacked:
		m.Layout = LayoutSourcesPDF
	case LayoutSourcesPDF:
		m.Layout = LayoutNewPDF
	case LayoutNewPDF:
		m.Layout = LayoutNoPDF
	case LayoutPDFOnly:
		m.exitPDFOnly()
		m.Status = "layout: " + layoutName(m.Layout)
		return
	default: // LayoutNoPDF
		m.Layout = LayoutThreeCol
	}
	m.snapFocusToLayout()
	m.Status = "layout: " + layoutName(m.Layout)
}

// togglePDFOnly zooms the PDF pane to the whole terminal and back,
// remembering the layout and the focused pane it interrupted.
func (m *Model) togglePDFOnly() {
	if m.Layout == LayoutPDFOnly {
		m.exitPDFOnly()
		m.Status = "layout: " + layoutName(m.Layout)
		return
	}
	m.prevLayout = m.Layout
	m.prevFocus = m.Focus
	m.Layout = LayoutPDFOnly
	m.Focus = PanePDF
	m.Status = "layout: PDF only (| or \\ to restore)"
}

// exitPDFOnly restores the layout and focus interrupted by the zoom.
// snapFocusToLayout stays as the safety net in case the remembered pane
// is not part of the restored layout.
func (m *Model) exitPDFOnly() {
	m.Layout = m.prevLayout
	m.Focus = m.prevFocus
	m.snapFocusToLayout()
}

// snapFocusToLayout moves focus to the new-source pane when the current
// layout no longer shows the focused pane.
func (m *Model) snapFocusToLayout() {
	for _, p := range m.focusOrder() {
		if p == m.Focus {
			return
		}
	}
	m.Focus = PaneNewSource
}

func (m Model) focusOrder() []Pane {
	switch m.Layout {
	case LayoutNoPDF:
		return []Pane{PaneOutline, PaneOldSource, PaneNewSource}
	case LayoutSourcesPDF:
		return []Pane{PaneOldSource, PaneNewSource, PanePDF}
	case LayoutNewPDF:
		return []Pane{PaneNewSource, PanePDF}
	case LayoutPDFOnly:
		return []Pane{PanePDF}
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
		switch m.Layout {
		case LayoutNoPDF, LayoutPDFOnly:
			return false
		case LayoutStacked, LayoutSourcesPDF:
			// Top share shrinks when the focused bottom PDF grows.
			m.StackedTopFrac -= step
		default:
			m.PDFFrac += step
		}
	case PaneOldSource:
		m.SourceSplitFrac += step
	case PaneNewSource:
		if m.Layout == LayoutNewPDF {
			// Only two panes: growing the source shrinks the PDF share.
			m.PDFFrac -= step
		} else {
			m.SourceSplitFrac -= step
		}
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

// helpSection groups related bindings under a heading for the overlay.
type helpSection struct {
	title string
	rows  [][2]string
}

func helpSections(allowModifications bool) []helpSection {
	editTitle := "EDIT (new file)"
	if !allowModifications {
		editTitle = "EDIT (disabled — rerun with --allow-modifications)"
	}
	return []helpSection{
		{"NAVIGATE", [][2]string{
			{"j/k ↑/↓", "move by change pair (counts: 10j)"},
			{"J/K", "jump 10 down / 5 up pairs"},
			{"gg / G", "first / last pair"},
			{"{ / }", "previous / next section"},
			{"[ / ]", "previous / next source line (PDF anchor)"},
			{"z", "fold/unfold outline group"},
			{"h/l ←/→", "focus pane"},
		}},
		{"REVIEW", [][2]string{
			{"space", "mark reviewed"},
			{"a", "annotate pair"},
			{"ctrl+a", "edit annotation"},
			{"d", "delete annotation"},
			{"y", "copy selected change (side follows focus)"},
			{"f", "cycle filter"},
			{"m", "semantic / coalesced diff regime"},
		}},
		{editTitle, [][2]string{
			{"e", "inline single-line edit"},
			{"E", "$EDITOR at current line"},
			{"u / ctrl+r", "undo / redo in-place edits"},
		}},
		{"PDF", [][2]string{
			{"S", "Skim forward-search at line (never compiles)"},
			{"P", "open new PDF in Preview"},
			{"B", "re-diff source + rebuild/reload PDF"},
			{"C", "old vs new in external compare"},
		}},
		{"LAYOUT", [][2]string{
			{"\\", "cycle: full·side → full·below → sources+PDF → new+PDF → no PDF"},
			{"|", "PDF-only zoom (toggle)"},
			{"< / >", "resize focused pane / source split"},
		}},
		{"QUIT", [][2]string{
			{"q", "quit — save sidecar, emit annotations"},
			{"Q Q", "discard annotations/marks (file edits stay)"},
			{"?", "close help"},
		}},
	}
}

// renderHelpSection formats one section with an aligned key column.
func renderHelpSection(s helpSection, width int) string {
	keyW := 0
	for _, r := range s.rows {
		if w := lipgloss.Width(r[0]); w > keyW {
			keyW = w
		}
	}
	lines := []string{clipLine(s.title, width)}
	for _, r := range s.rows {
		key := r[0] + strings.Repeat(" ", keyW-lipgloss.Width(r[0]))
		lines = append(lines, clipLine("  "+key+"  "+r[1], width))
	}
	return strings.Join(lines, "\n")
}

// RenderHelpBody returns the sectioned help text: two balanced columns
// when the width allows, a single column otherwise.
func RenderHelpBody(width int, allowModifications bool) string {
	sections := helpSections(allowModifications)
	if width < 100 {
		parts := make([]string, 0, len(sections))
		for _, s := range sections {
			parts = append(parts, renderHelpSection(s, width))
		}
		return strings.Join(parts, "\n\n")
	}
	gap := 4
	colW := (width - gap) / 2
	half := (len(sections) + 1) / 2
	renderCol := func(ss []helpSection) string {
		parts := make([]string, 0, len(ss))
		for _, s := range ss {
			parts = append(parts, renderHelpSection(s, colW))
		}
		return strings.Join(parts, "\n\n")
	}
	left := renderCol(sections[:half])
	right := renderCol(sections[half:])
	// Pad the left column to a fixed width so the right column aligns.
	leftLines := strings.Split(left, "\n")
	for i := range leftLines {
		leftLines[i] = padANSIToWidth(leftLines[i], colW)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top,
		strings.Join(leftLines, "\n"),
		strings.Repeat(" ", gap)+"",
		right)
}

// renderHelpOverlay renders the full-screen help: a centered bordered box
// over a blank body, replacing the entire pane layout while open.
func (m Model) renderHelpOverlay(width, bodyHeight int) string {
	inner := width - 8
	if inner < 20 {
		inner = 20
	}
	body := "mrevdiff — keys\n\n" + RenderHelpBody(inner, m.AllowModifications)
	box := m.Styles.PaneFocused.
		Border(lipgloss.RoundedBorder()).
		Padding(1, 3).
		Render(body)
	return lipgloss.Place(width, bodyHeight, lipgloss.Center, lipgloss.Center, box)
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
