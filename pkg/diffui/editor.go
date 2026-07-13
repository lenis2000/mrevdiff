package diffui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/lenis2000/mrevdiff/pkg/diffreview"
	"github.com/lenis2000/mrevdiff/pkg/persist"
	"github.com/lenis2000/mrevdiff/pkg/ui"
)

var diffEditorCandidates = []string{"nvim", "vim", "vi", "nano"}

var runDiffEditorProcess = func(cmd *exec.Cmd, done func(error) tea.Msg) tea.Cmd {
	return tea.ExecProcess(cmd, done)
}

type diffEditFinishedMsg struct {
	err error
}

func (m Model) editInExternalEditor() (tea.Model, tea.Cmd) {
	if status := m.editDisabledStatus(true); status != "" {
		m.Status = status
		return m, nil
	}
	head, userArgs, ok := resolveDiffEditor()
	if !ok {
		m.Status = "E: no editor found (set $EDITOR)"
		return m, nil
	}
	line := m.currentNewLine()
	if line < 1 {
		m.Status = "E: cursor has no resolvable new source line"
		return m, nil
	}
	if err := (&m).pushEditSnapshot("external editor"); err != nil {
		m.Status = "E: snapshot: " + err.Error()
		return m, nil
	}
	argv := append(append([]string{}, userArgs...), buildDiffEditorLineArgs(head, m.Review.New.Path, line)...)
	// Inside agterm, run the editor in an overlay on top of this session
	// instead of suspending the TUI: the kitty PDF frame stays painted and
	// the review is visible the moment the editor closes.
	if agtermAvailable() {
		parts := make([]string, 0, len(argv)+1)
		parts = append(parts, shellQuoteArg(head))
		for _, a := range argv {
			parts = append(parts, shellQuoteArg(a))
		}
		shellCmd := strings.Join(parts, " ")
		dir := filepath.Dir(m.Review.New.Path)
		m.Status = "editing in agterm overlay…"
		return m, func() tea.Msg {
			return diffEditFinishedMsg{err: agtermOverlayEdit(shellCmd, dir)}
		}
	}
	cmd := exec.Command(head, argv...)
	var ttyFile *os.File
	if tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
		ttyFile = tty
		cmd.Stdin = tty
		cmd.Stdout = tty
		cmd.Stderr = tty
	} else {
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	return m, runDiffEditorProcess(cmd, func(err error) tea.Msg {
		if ttyFile != nil {
			_ = ttyFile.Close()
		}
		return diffEditFinishedMsg{err: err}
	})
}

func resolveDiffEditor() (string, []string, bool) {
	if v := strings.TrimSpace(os.Getenv("EDITOR")); v != "" {
		tokens := ui.ParseShellArgs(v)
		if len(tokens) > 0 {
			if _, err := exec.LookPath(tokens[0]); err == nil {
				return tokens[0], tokens[1:], true
			}
		}
	}
	for _, candidate := range diffEditorCandidates {
		if _, err := exec.LookPath(candidate); err == nil {
			return candidate, nil, true
		}
	}
	return "", nil, false
}

func buildDiffEditorLineArgs(editor, path string, line int) []string {
	if line < 1 {
		line = 1
	}
	name := strings.ToLower(filepath.Base(editor))
	switch {
	case strings.Contains(name, "vim"),
		strings.Contains(name, "vi"),
		strings.Contains(name, "nano"),
		strings.Contains(name, "emacs"):
		return []string{fmt.Sprintf("+%d", line), path}
	default:
		return []string{path}
	}
}

func (m Model) startLineEdit() (tea.Model, tea.Cmd) {
	if status := m.editDisabledStatus(true); status != "" {
		m.Status = status
		return m, nil
	}
	line := m.currentNewLine()
	if line < 1 {
		m.Status = "e: cursor has no resolvable new source line"
		return m, nil
	}
	lines := strings.Split(string(m.Review.New.Source), "\n")
	if len(m.Review.New.Source) > 0 && m.Review.New.Source[len(m.Review.New.Source)-1] == '\n' {
		lines = lines[:len(lines)-1]
	}
	if line < 1 || line > len(lines) {
		m.Status = fmt.Sprintf("e: line %d out of range", line)
		return m, nil
	}
	full := lines[line-1]
	indent, body := splitLeadingIndent(full)
	ta := textarea.New()
	ta.SetValue(body)
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.CharLimit = 4000
	ta.SetWidth(120)
	ta.SetHeight(5)
	cmd := ta.Focus()
	m.LineEdit = &LineEditPopup{
		TA:           ta,
		AbsoluteLine: line,
		Original:     full,
		Indent:       indent,
	}
	m.Status = ""
	return m, cmd
}

func (m Model) updateLineEditPopup(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.LineEdit == nil {
		return m, nil
	}
	switch msg.Type {
	case tea.KeyEnter, tea.KeyCtrlS:
		return m.submitLineEdit()
	case tea.KeyEsc, tea.KeyCtrlC:
		m.LineEdit = nil
		m.Status = ""
		return m, nil
	}
	var cmd tea.Cmd
	m.LineEdit.TA, cmd = m.LineEdit.TA.Update(msg)
	return m, cmd
}

func (m Model) submitLineEdit() (tea.Model, tea.Cmd) {
	p := m.LineEdit
	if p == nil {
		return m, nil
	}
	newLine := p.Indent + onePhysicalLine(p.TA.Value())
	m.LineEdit = nil
	if newLine == p.Original {
		m.Status = "line edit: no change"
		return m, nil
	}
	if err := (&m).pushEditSnapshot(fmt.Sprintf("line %d", p.AbsoluteLine)); err != nil {
		m.Status = "line edit: snapshot: " + err.Error()
		return m, nil
	}
	if err := writeSourceLine(m.Review.New.Path, p.AbsoluteLine, newLine); err != nil {
		m.Status = "line edit: " + err.Error()
		return m, nil
	}
	m = m.reloadAfterEdit(fmt.Sprintf("line %d updated", p.AbsoluteLine))
	return m.afterSourceReload()
}

func onePhysicalLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.ReplaceAll(s, "\n", " ")
}

func (m Model) applyEditFinished(msg diffEditFinishedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.Status = "E: editor exited with error: " + msg.err.Error()
		return m, nil
	}
	m = m.reloadAfterEdit("external edit reloaded")
	return m.afterSourceReload()
}

func (m Model) undoEdit() (tea.Model, tea.Cmd) {
	if len(m.EditUndo) == 0 {
		m.Status = "u: nothing to undo"
		return m, nil
	}
	snap := m.EditUndo[len(m.EditUndo)-1]
	if !m.snapshotPathIsNew(snap.Path) {
		m.Status = "u: snapshot is not for the new endpoint"
		return m, nil
	}
	current, err := os.ReadFile(snap.Path)
	if err != nil {
		m.Status = "u: " + err.Error()
		return m, nil
	}
	if err := persist.WriteFileAtomic(snap.Path, snap.Bytes); err != nil {
		m.Status = "u: " + err.Error()
		return m, nil
	}
	m.EditUndo = m.EditUndo[:len(m.EditUndo)-1]
	m.EditRedo = append(m.EditRedo, EditSnapshot{
		Path:     snap.Path,
		Bytes:    current,
		Label:    snap.Label,
		Sequence: snap.Sequence,
	})
	if len(m.EditRedo) > maxEditUndo {
		m.EditRedo = m.EditRedo[len(m.EditRedo)-maxEditUndo:]
	}
	m = m.reloadAfterEdit("undid " + snap.Label)
	return m.afterSourceReload()
}

func (m Model) redoEdit() (tea.Model, tea.Cmd) {
	if len(m.EditRedo) == 0 {
		m.Status = "ctrl-r: nothing to redo"
		return m, nil
	}
	snap := m.EditRedo[len(m.EditRedo)-1]
	if !m.snapshotPathIsNew(snap.Path) {
		m.Status = "ctrl-r: snapshot is not for the new endpoint"
		return m, nil
	}
	current, err := os.ReadFile(snap.Path)
	if err != nil {
		m.Status = "ctrl-r: " + err.Error()
		return m, nil
	}
	if err := persist.WriteFileAtomic(snap.Path, snap.Bytes); err != nil {
		m.Status = "ctrl-r: " + err.Error()
		return m, nil
	}
	m.EditRedo = m.EditRedo[:len(m.EditRedo)-1]
	m.EditUndo = append(m.EditUndo, EditSnapshot{
		Path:     snap.Path,
		Bytes:    current,
		Label:    snap.Label,
		Sequence: snap.Sequence,
	})
	if len(m.EditUndo) > maxEditUndo {
		m.EditUndo = m.EditUndo[len(m.EditUndo)-maxEditUndo:]
	}
	m = m.reloadAfterEdit("redid " + snap.Label)
	return m.afterSourceReload()
}

func (m Model) reloadAfterEdit(status string) Model {
	anchorID := ""
	if pair := m.CurrentPair(); pair != nil {
		anchorID = pair.ID
	}
	newSource, err := os.ReadFile(m.Review.New.Path)
	if err != nil {
		m.Status = "reload: " + err.Error()
		return m
	}
	oldEndpoint := m.Review.Old
	newEndpoint := m.Review.New
	newEndpoint.Source = newSource
	review, err := diffreview.RebuildWithOldDoc(m.Review.OldDoc, oldEndpoint, newEndpoint)
	if err != nil {
		m.Status = "reload: " + err.Error()
		return m
	}
	resetRenderMemos()
	side := diffreview.RemapSidecar(m.FinalSidecar(), review)
	sidecarBase := diffreview.RemapSidecar(m.SidecarBase, review)
	m.Review = review
	m.Sidecar = side
	m.SidecarBase = sidecarBase
	m.Reviewed = side.ReviewedSet()
	m.Annotations = side.AnnotationNotes()
	if idx := pairIndexByID(review, anchorID); idx >= 0 {
		m.Cursor = idx
	} else if idx := pairIndexByID(review, side.CursorPairID); idx >= 0 {
		m.Cursor = idx
	}
	m.SourceLineCursor = 1
	m.snapCursor()
	m.Status = status
	return m
}

func (m Model) afterSourceReload() (tea.Model, tea.Cmd) {
	if strings.HasPrefix(m.Status, "reload:") {
		return m, nil
	}
	if m.NoBuild {
		m.BuildStale = true
		m.PDFImage = ""
		m.PDFStatus = "PDF build skipped by --no-build; press B to rebuild"
		return m, nil
	}
	return m.startPDFReload(true)
}

func (m Model) editDisabledStatus(requireNewBlock bool) string {
	if m.Review == nil {
		return "no review loaded"
	}
	if !m.RequestedAllowMods {
		return "edit disabled; rerun with --allow-modifications"
	}
	pair := m.CurrentDisplayPair()
	if requireNewBlock && (pair == nil || pair.New == nil) {
		return "deleted block has no new source to edit"
	}
	if !m.Review.New.Editable || !m.AllowModifications {
		return "new endpoint is read-only; use --base REV path.tex from the branch you want to edit"
	}
	if m.Review.New.Path == "" {
		return "new endpoint is read-only; use --base REV path.tex from the branch you want to edit"
	}
	return ""
}

func (m Model) currentNewLine() int {
	pair := m.CurrentDisplayPair()
	if pair == nil || pair.New == nil || pair.New.StartLine < 1 {
		return 0
	}
	offset := m.SourceLineCursor
	if offset < 1 {
		offset = 1
	}
	line := pair.New.StartLine + offset - 1
	if pair.New.EndLine > 0 && line > pair.New.EndLine {
		line = pair.New.EndLine
	}
	return line
}

func (m Model) snapshotPathIsNew(path string) bool {
	if m.Review == nil {
		return false
	}
	return path != "" && path == m.Review.New.Path
}

func splitLeadingIndent(s string) (string, string) {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return s[:i], s[i:]
}

func writeSourceLine(path string, n int, newContent string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	hadTrailingNL := len(data) > 0 && data[len(data)-1] == '\n'
	lines := strings.Split(string(data), "\n")
	if hadTrailingNL {
		lines = lines[:len(lines)-1]
	}
	if n < 1 || n > len(lines) {
		return fmt.Errorf("line %d out of range (1..%d)", n, len(lines))
	}
	lines[n-1] = newContent
	out := strings.Join(lines, "\n")
	if hadTrailingNL {
		out += "\n"
	}
	return persist.WriteFileAtomic(path, []byte(out))
}
