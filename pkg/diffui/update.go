package diffui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"mrevdiff/pkg/diffreview"
	"mrevdiff/pkg/parser"
)

const (
	diffJumpDownCount = 10
	diffJumpUpCount   = 5
)

// Update implements tea.Model. It delegates to updateInner and then
// reconciles the agterm session flag, which mirrors "this review needs
// attention" (annotations pending / rebuild failed) after any message
// that may have changed that state.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.updateInner(msg)
	nm, ok := next.(Model)
	if !ok {
		return next, cmd
	}
	if flagFn := (&nm).syncAgtermFlag(); flagFn != nil {
		return nm, tea.Batch(cmd, func() tea.Msg { flagFn(); return nil })
	}
	return nm, cmd
}

func (m Model) updateInner(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(tea.MouseMsg); !ok {
		m.mouseWheelEdge = mouseWheelEdgeState{}
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.Width = msg.Width
		m.Height = msg.Height
		return m.withPDFRender()
	case diffEditFinishedMsg:
		return m.applyEditFinished(msg)
	case diffCompareFinishedMsg:
		return m.applyCompareFinished(msg)
	case diffPDFRenderMsg:
		return m.applyPDFRender(msg)
	case diffPDFReloadMsg:
		return m.applyPDFReload(msg)
	case diffPDFOpenFinishedMsg:
		return m.applyPDFOpenFinished(msg)
	case diffOldPDFMsg:
		return m.applyOldPDF(msg)
	case diffSkimOpenFinishedMsg:
		return m.applySkimOpenFinished(msg)
	case tea.MouseMsg:
		// Mouse activity disarms a pending Q-discard just like any key:
		// mouse handlers overwrite the status line, so leaving the flag
		// armed would let a much-later Q discard without a visible warning.
		m.discardArmed = false
		if m.LineEdit != nil || m.Popup != nil {
			return m, nil
		}
		return m.handleMouse(msg)
	case tea.KeyMsg:
		if m.LineEdit != nil {
			return m.updateLineEditPopup(msg)
		}
		if m.Popup != nil {
			return m.updateAnnotationPopup(msg)
		}
		return m.updateKey(msg)
	default:
		return m, nil
	}
}

func (m Model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.Pending != nil {
		m.CountBuf = ""
		if key == "ctrl+c" {
			m.Pending = nil
			m.quitting = true
			return m, tea.Quit
		}
		return m.confirmDelete(key == "y" || key == "Y"), nil
	}
	// Modal overlays claim the keyboard before global bindings.
	if m.Search != nil && m.Search.Typing {
		m.discardArmed = false
		return m.updateSearchInput(msg)
	}
	if m.AnnList != nil {
		m.discardArmed = false
		return m.updateAnnList(key)
	}
	if m.ShowInfo {
		m.discardArmed = false
		m.ShowInfo = false
		m.Status = ""
		return m, nil
	}
	// ctrl+c is a hard quit and is never remappable (safety hatch).
	if key == "ctrl+c" {
		m.CountBuf = ""
		m.quitting = true
		return m, tea.Quit
	}

	action := m.Keymap.Lookup(key)

	if action == ActionQuit {
		m.CountBuf = ""
		m.quitting = true
		return m, tea.Quit
	}
	if action == ActionDiscard {
		m.CountBuf = ""
		if m.discardArmed {
			m.Discarded = true
			m.quitting = true
			return m, tea.Quit
		}
		m.discardArmed = true
		m.Status = "press again to quit discarding annotations/marks — in-place file edits stay (q keeps everything)"
		return m, nil
	}
	m.discardArmed = false
	if action == ActionHelp {
		m.ShowHelp = !m.ShowHelp
		m.CountBuf = ""
		m.pendingG = false
		return m, nil
	}
	if m.ShowHelp {
		m.CountBuf = ""
		return m, nil
	}

	// The gg leader and digit count prefix are vim motion mechanics, not
	// commands — handled literally, never remapped.
	if m.pendingG {
		m.pendingG = false
		if key == "g" {
			m.CountBuf = ""
			m.moveToFirst()
			return m.withPDFRender()
		}
	}
	if isDiffMotionDigit(key) && action == ActionNone {
		if key == "0" && m.CountBuf == "" {
			return m, nil
		}
		m.CountBuf += key
		return m, nil
	}
	count := parseDiffMotionCount(m.CountBuf)
	m.CountBuf = ""
	if key == "g" && action == ActionNone {
		m.pendingG = true
		return m, nil
	}

	switch action {
	case ActionFilterCycle:
		m.Filter = CycleFilter(m.Filter)
		m.snapCursor()
		return m.withPDFRender()
	case ActionRegimeToggle:
		m = m.toggleDiffRegime()
		return m.withPDFRender()
	case ActionFoldToggle:
		m.toggleOutlineFold()
		return m, nil
	case ActionReviewToggle:
		m = m.toggleReviewed()
		return m.withPDFRender()
	case ActionAnnotate:
		return m.startAnnotation(false)
	case ActionAnnotateEdit:
		return m.startAnnotation(true)
	case ActionAnnotateDelete:
		return m.beginDelete(), nil
	case ActionCopy:
		return m.copySelectedChunk()
	case ActionEditExternal:
		return m.editInExternalEditor()
	case ActionEditInline:
		return m.startLineEdit()
	case ActionCompare:
		return m.openCompareEditor()
	case ActionPreview:
		return m.openPreviewPDF()
	case ActionSkim:
		return m.openSkimAtLine()
	case ActionBlink:
		return m.toggleOldPDF()
	case ActionFullPage:
		m.pdfFullPage = !m.pdfFullPage
		m.PDFImage = ""
		if m.pdfFullPage {
			m.Status = "PDF: full page (toggle key crops to region)"
			if m.Layout == LayoutNoPDF {
				m.Status += " — press \\ or | to show the PDF pane"
			}
		} else {
			m.Status = "PDF: region crop"
		}
		return m.withPDFRender()
	case ActionSearch:
		return m.startSearch()
	case ActionSearchNext:
		return m.nextMatch(1)
	case ActionSearchPrev:
		return m.nextMatch(-1)
	case ActionAnnotationList:
		return m.openAnnList()
	case ActionInfo:
		m.ShowInfo = true
		return m, nil
	case ActionLayoutCycle:
		m.cycleLayout()
		m.PDFImage = ""
		return m.withPDFRender()
	case ActionPDFZoom:
		m.togglePDFOnly()
		m.PDFImage = ""
		return m.withPDFRender()
	case ActionFocusPrev:
		m.moveFocus(-1)
		return m, nil
	case ActionFocusNext:
		m.moveFocus(1)
		return m, nil
	case ActionResizeShrink:
		if m.resizeFocusedPane(-1) {
			m.PDFImage = ""
			m.Status = "resized " + m.Focus.String()
			return m.withPDFRender()
		}
		return m, nil
	case ActionResizeGrow:
		if m.resizeFocusedPane(1) {
			m.PDFImage = ""
			m.Status = "resized " + m.Focus.String()
			return m.withPDFRender()
		}
		return m, nil
	case ActionReload:
		m = m.reloadAfterEdit("source reloaded")
		if strings.HasPrefix(m.Status, "reload:") {
			return m, nil
		}
		return m.startPDFReload(true)
	case ActionUndo:
		return m.undoEdit()
	case ActionRedo:
		return m.redoEdit()
	case ActionSourceLinePrev:
		m.moveSourceLine(-count)
		return m.withPDFRender()
	case ActionSourceLineNext:
		m.moveSourceLine(count)
		return m.withPDFRender()
	case ActionNext:
		if m.Focus == PaneOldSource || m.Focus == PaneNewSource {
			m.moveSourceLine(count)
		} else {
			m.moveDiffChunkOrPairRepeat(1, count)
		}
		return m.withPDFRender()
	case ActionPrev:
		if m.Focus == PaneOldSource || m.Focus == PaneNewSource {
			m.moveSourceLine(-count)
		} else {
			m.moveDiffChunkOrPairRepeat(-1, count)
		}
		return m.withPDFRender()
	case ActionJumpDown:
		m.moveVisible(diffJumpDownCount * count)
		return m.withPDFRender()
	case ActionJumpUp:
		m.moveVisible(-diffJumpUpCount * count)
		return m.withPDFRender()
	case ActionFirst:
		m.moveToFirst()
		return m.withPDFRender()
	case ActionLast:
		m.moveToLast()
		return m.withPDFRender()
	case ActionSectionNext:
		m.moveSectionRepeat(1, count)
		return m.withPDFRender()
	case ActionSectionPrev:
		m.moveSectionRepeat(-1, count)
		return m.withPDFRender()
	}
	return m, nil
}

func isDiffMotionDigit(key string) bool {
	return len(key) == 1 && key[0] >= '0' && key[0] <= '9'
}

// parseDiffMotionCount turns a Vim-style digit prefix into a repeat count.
// Empty, malformed, and zero buffers all mean one motion.
func parseDiffMotionCount(buf string) int {
	if buf == "" {
		return 1
	}
	n, err := strconv.Atoi(buf)
	if err != nil || n < 1 {
		return 1
	}
	return n
}

func (m Model) toggleDiffRegime() Model {
	oldLine, newLine := m.sourceAnchorLines()
	preferOld := m.Focus == PaneOldSource
	oldRegime := m.DiffRegime
	members := m.currentMemberPairIndices()
	if oldRegime == DiffRegimeCoalesced {
		if idx := memberPairIndexForLine(m.Review, members, oldLine, newLine, preferOld); idx >= 0 {
			m.Cursor = idx
		}
	}
	m.DiffRegime = CycleDiffRegime(m.DiffRegime)
	if offset := displayPairOffsetForLine(m.CurrentDisplayPair(), oldLine, newLine, preferOld); offset > 0 {
		m.SourceLineCursor = offset
	}
	m.snapSourceLine()
	m.Status = "diff mode: " + m.DiffRegime.String()
	return m
}

func memberPairIndexForLine(review *diffreview.Review, members []int, oldLine, newLine int, preferOld bool) int {
	if review == nil {
		return -1
	}
	if preferOld {
		if idx := memberPairIndexForSideLine(review, members, oldLine, true); idx >= 0 {
			return idx
		}
	}
	if idx := memberPairIndexForSideLine(review, members, newLine, false); idx >= 0 {
		return idx
	}
	return memberPairIndexForSideLine(review, members, oldLine, true)
}

func memberPairIndexForSideLine(review *diffreview.Review, members []int, line int, oldSide bool) int {
	if line < 1 {
		return -1
	}
	for _, idx := range members {
		if idx < 0 || idx >= len(review.Pairs) {
			continue
		}
		block := review.Pairs[idx].New
		if oldSide {
			block = review.Pairs[idx].Old
		}
		if block != nil && block.StartLine <= line && (block.EndLine == 0 || line <= block.EndLine) {
			return idx
		}
	}
	return -1
}

func displayPairOffsetForLine(pair *diffreview.Pair, oldLine, newLine int, preferOld bool) int {
	if pair == nil {
		return 0
	}
	if preferOld {
		if offset := blockOffsetForLine(pair.Old, oldLine); offset > 0 {
			return offset
		}
	}
	if offset := blockOffsetForLine(pair.New, newLine); offset > 0 {
		return offset
	}
	if offset := blockOffsetForLine(pair.Old, oldLine); offset > 0 {
		return offset
	}
	return 0
}

func blockOffsetForLine(block *parser.Block, line int) int {
	if block == nil || line < 1 || block.StartLine < 1 {
		return 0
	}
	if line < block.StartLine {
		return 0
	}
	if block.EndLine > 0 && line > block.EndLine {
		return 0
	}
	offset := line - block.StartLine + 1
	if count := len(blockSourceLines(block)); count > 0 && offset > count {
		offset = count
	}
	return offset
}

func (m Model) toggleReviewed() Model {
	pair := m.CurrentPair()
	if pair == nil {
		m.Status = "no pair selected"
		return m
	}
	targetsBefore := m.visibleTargets()
	visibleBefore := make([]int, 0, len(targetsBefore))
	for _, target := range targetsBefore {
		visibleBefore = append(visibleBefore, target.PairIndex)
	}
	posBefore := m.visibleTargetPosition(targetsBefore)
	members := m.currentMemberPairIndices()
	if len(members) == 0 {
		members = []int{m.Cursor}
	}
	was := true
	for _, idx := range members {
		if idx < 0 || m.Review == nil || idx >= len(m.Review.Pairs) {
			continue
		}
		if !m.Reviewed[m.Review.Pairs[idx].ID] {
			was = false
			break
		}
	}
	now := !was
	for _, idx := range members {
		if idx < 0 || m.Review == nil || idx >= len(m.Review.Pairs) {
			continue
		}
		id := m.Review.Pairs[idx].ID
		m.Reviewed[id] = now
		m.ensureSidecar().SetReviewed(id, now)
	}
	m.Status = ""
	if now && (m.Filter == FilterChanged || m.Filter == FilterUnreviewed) {
		m.advanceAfterReviewed(visibleBefore, posBefore)
	}
	return m
}

func (m *Model) advanceAfterReviewed(visibleBefore []int, posBefore int) {
	if len(visibleBefore) == 0 {
		return
	}
	switch m.Filter {
	case FilterChanged:
		if posBefore+1 < len(visibleBefore) {
			m.Cursor = visibleBefore[posBefore+1]
			m.resetSourceLine()
		}
	case FilterUnreviewed:
		visibleAfter := m.visibleIndices()
		if len(visibleAfter) == 0 {
			m.Status = "all visible pairs reviewed"
			return
		}
		if posBefore >= len(visibleAfter) {
			posBefore = len(visibleAfter) - 1
		}
		m.Cursor = visibleAfter[posBefore]
		m.resetSourceLine()
	}
}

func (m Model) startAnnotation(editing bool) (tea.Model, tea.Cmd) {
	pair := m.CurrentPair()
	if pair == nil {
		m.Status = "no pair selected"
		return m, nil
	}
	initial := m.Annotations[pair.ID]
	if editing && strings.TrimSpace(initial) == "" {
		m.Status = "no annotation on current pair"
		return m, nil
	}
	popup, cmd := newAnnotationPopup(pair.ID, initial, editing)
	m.Popup = popup
	m.pendingG = false
	m.Status = ""
	return m, cmd
}

func newAnnotationPopup(pairID, initial string, editing bool) (*AnnotationPopup, tea.Cmd) {
	ta := textarea.New()
	ta.Prompt = "| "
	ta.ShowLineNumbers = false
	ta.CharLimit = 4000
	ta.SetWidth(60)
	ta.SetHeight(6)
	if initial != "" {
		ta.SetValue(initial)
	}
	cmd := ta.Focus()
	return &AnnotationPopup{TA: ta, PairID: pairID, Editing: editing}, cmd
}

func (m Model) updateAnnotationPopup(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		return m.submitAnnotation(), nil
	case tea.KeyEsc, tea.KeyCtrlC:
		m.Popup = nil
		m.Status = ""
		return m, nil
	}
	if msg.String() == "ctrl+s" {
		return m.submitAnnotation(), nil
	}
	var cmd tea.Cmd
	m.Popup.TA, cmd = m.Popup.TA.Update(msg)
	return m, cmd
}

func (m Model) submitAnnotation() Model {
	if m.Popup == nil {
		return m
	}
	pairID := m.Popup.PairID
	note := strings.TrimSpace(m.Popup.TA.Value())
	if note != "" {
		note = strings.Join(strings.Fields(note), " ")
	}
	m.Popup = nil
	if note == "" {
		m.Status = ""
		return m
	}
	pair := m.pairByID(pairID)
	if pair == nil {
		m.Status = "pair no longer exists"
		return m
	}
	m.ensureSidecar().UpsertAnnotation(diffreview.AnnotationForPair(m.Review, pair, note))
	m.Annotations[pairID] = note
	m.Status = ""
	return m
}

func (m Model) beginDelete() Model {
	pair := m.CurrentPair()
	if pair == nil {
		m.Status = "no pair selected"
		return m
	}
	if strings.TrimSpace(m.Annotations[pair.ID]) == "" {
		m.Status = "no annotation on current pair"
		return m
	}
	m.Pending = &PendingDelete{PairID: pair.ID}
	m.Status = "[y/N] delete annotation?"
	return m
}

func (m Model) confirmDelete(yes bool) Model {
	pending := m.Pending
	m.Pending = nil
	if pending == nil {
		return m
	}
	if !yes {
		m.Status = ""
		return m
	}
	m.ensureSidecar().DeleteAnnotation(pending.PairID)
	delete(m.Annotations, pending.PairID)
	m.Status = ""
	return m
}

func (m *Model) ensureSidecar() *diffreview.Sidecar {
	if m.Sidecar == nil {
		m.Sidecar = diffreview.NewSidecar(m.Review)
	}
	return m.Sidecar
}

func (m Model) pairByID(pairID string) *diffreview.Pair {
	if m.Review == nil || pairID == "" {
		return nil
	}
	if pair := m.Review.ByID[pairID]; pair != nil {
		return pair
	}
	for i := range m.Review.Pairs {
		if m.Review.Pairs[i].ID == pairID {
			return &m.Review.Pairs[i]
		}
	}
	return nil
}

func (m *Model) moveVisible(delta int) {
	targets := m.visibleTargets()
	if len(targets) == 0 {
		m.Status = "no pairs match filter"
		return
	}
	pos := m.visibleTargetPosition(targets)
	if delta > 0 && !m.visibleTargetContainsCurrent(targets) {
		pos--
	}
	pos += delta
	if pos < 0 {
		pos = 0
	}
	if pos >= len(targets) {
		pos = len(targets) - 1
	}
	target := targets[pos]
	m.Cursor = target.PairIndex
	m.SourceLineCursor = target.AnchorLine
	m.snapSourceLine()
	m.Status = ""
}

func (m Model) visibleTargetContainsCurrent(targets []outlineTarget) bool {
	for _, target := range targets {
		if outlineTargetContainsPair(target, m.Cursor) {
			return true
		}
	}
	return false
}

func (m *Model) moveDiffChunkOrPair(delta int) {
	if delta == 0 {
		return
	}
	m.moveVisible(delta)
}

func (m *Model) moveDiffChunkOrPairRepeat(delta, count int) {
	if count < 1 {
		count = 1
	}
	if delta < 0 {
		delta = -1
	} else {
		delta = 1
	}
	for i := 0; i < count; i++ {
		oldCursor, oldLine := m.Cursor, m.SourceLineCursor
		m.moveDiffChunkOrPair(delta)
		if m.Cursor == oldCursor && m.SourceLineCursor == oldLine {
			return
		}
	}
}

func (m *Model) moveToFirst() {
	targets := m.visibleTargets()
	if len(targets) == 0 {
		m.Status = "no pairs match filter"
		return
	}
	m.Cursor = targets[0].PairIndex
	m.SourceLineCursor = targets[0].AnchorLine
	m.snapSourceLine()
	m.Status = ""
}

func (m *Model) moveToLast() {
	targets := m.visibleTargets()
	if len(targets) == 0 {
		m.Status = "no pairs match filter"
		return
	}
	last := targets[len(targets)-1]
	m.Cursor = last.PairIndex
	m.SourceLineCursor = last.AnchorLine
	m.snapSourceLine()
	m.Status = ""
}

func (m Model) visiblePosition(visible []int) int {
	if len(visible) == 0 {
		return 0
	}
	for i, idx := range visible {
		if idx == m.Cursor {
			return i
		}
	}
	for i, idx := range visible {
		if idx > m.Cursor {
			return i
		}
	}
	return len(visible) - 1
}

func (m *Model) moveSection(direction int) {
	targets := m.visibleTargets()
	if len(targets) == 0 {
		m.Status = "no pairs match filter"
		return
	}
	if m.Review == nil {
		return
	}
	pos := m.visibleTargetPosition(targets)
	current := sectionKey(m.Review.Pairs[targets[pos].PairIndex])
	if current == "" {
		m.Status = "no section information for current pair"
		return
	}
	for i := pos + direction; i >= 0 && i < len(targets); i += direction {
		nextTarget := targets[i]
		next := sectionKey(m.Review.Pairs[nextTarget.PairIndex])
		if next != "" && next != current {
			m.Cursor = nextTarget.PairIndex
			m.SourceLineCursor = nextTarget.AnchorLine
			m.snapSourceLine()
			m.Status = ""
			return
		}
	}
	m.Status = "no more sections"
}

func (m *Model) moveSectionRepeat(direction, count int) {
	if count < 1 {
		count = 1
	}
	for i := 0; i < count; i++ {
		oldCursor, oldLine := m.Cursor, m.SourceLineCursor
		m.moveSection(direction)
		if m.Cursor == oldCursor && m.SourceLineCursor == oldLine {
			return
		}
	}
}

func (m *Model) moveSourceLine(delta int) {
	count, startLine, side := sourceLineTarget(m.CurrentDisplayPair(), m.Focus == PaneOldSource)
	if count == 0 {
		m.Status = "no source line for current pair"
		return
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
}

func (m *Model) snapSourceLine() {
	count, _, _ := sourceLineTarget(m.CurrentDisplayPair(), m.Focus == PaneOldSource)
	if count < 1 {
		m.SourceLineCursor = 1
		return
	}
	if m.SourceLineCursor < 1 {
		m.SourceLineCursor = 1
	}
	if m.SourceLineCursor > count {
		m.SourceLineCursor = count
	}
}

func (m *Model) resetSourceLine() {
	m.SourceLineCursor = 1
	m.snapSourceLine()
}

func sourceLineTarget(pair *diffreview.Pair, preferOld bool) (count int, startLine int, side string) {
	if pair == nil {
		return 0, 0, ""
	}
	if preferOld && pair.Old != nil {
		return len(blockSourceLines(pair.Old)), pair.Old.StartLine, "old"
	}
	if pair.New != nil {
		return len(blockSourceLines(pair.New)), pair.New.StartLine, "new"
	}
	if pair.Old != nil {
		return len(blockSourceLines(pair.Old)), pair.Old.StartLine, "old"
	}
	return 0, 0, ""
}

func fmtSourceLineStatus(side string, line int) string {
	if side == "" {
		side = "source"
	}
	if line < 1 {
		return "selected " + side + " source line"
	}
	return "selected " + side + " source line " + strconv.Itoa(line)
}

func sectionKey(pair diffreview.Pair) string {
	path := pair.SectionPathNew
	if len(path) == 0 {
		path = pair.SectionPathOld
	}
	if len(path) == 0 {
		return ""
	}
	out := ""
	for i, part := range path {
		if i > 0 {
			out += "\x00"
		}
		out += part
	}
	return out
}
