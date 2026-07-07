package diffui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lenis2000/mrevdiff/pkg/diffreview"
	"github.com/lenis2000/mrevdiff/pkg/parser"
	"github.com/lenis2000/mrevdiff/pkg/ui"
)

var compareEditorCandidates = []string{"opendiff", "zed"}

var runDiffCompareProcess = func(cmd *exec.Cmd) tea.Cmd {
	return func() tea.Msg {
		if err := cmd.Start(); err != nil {
			return diffCompareFinishedMsg{err: err}
		}
		go func() { _ = cmd.Wait() }()
		return diffCompareFinishedMsg{}
	}
}

type diffCompareFinishedMsg struct {
	err error
}

type compareTarget struct {
	OldPath string
	NewPath string
	OldLine int
	NewLine int
}

func (m Model) openCompareEditor() (tea.Model, tea.Cmd) {
	cmd, err := m.compareEditorExec()
	if err != nil {
		m.Status = compareStatus(err)
		return m, nil
	}
	m.Status = "opening external compare"
	return m, runDiffCompareProcess(cmd)
}

func (m Model) compareEditorCmd() tea.Cmd {
	cmd, err := m.compareEditorExec()
	if err != nil {
		return func() tea.Msg { return diffCompareFinishedMsg{err: err} }
	}
	return runDiffCompareProcess(cmd)
}

func (m Model) compareEditorExec() (*exec.Cmd, error) {
	head, userArgs, ok := resolveCompareEditor()
	if !ok {
		return nil, errors.New("no compare editor found (set MREVDIFF_COMPARE_EDITOR or install opendiff/zed)")
	}
	target, err := m.compareTarget()
	if err != nil {
		return nil, err
	}
	argv := buildCompareEditorArgv(head, userArgs, target)
	return exec.Command(head, argv...), nil
}

func (m Model) applyCompareFinished(msg diffCompareFinishedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.Status = compareStatus(msg.err)
		return m, nil
	}
	m.Status = "opened external compare"
	return m, nil
}

func compareStatus(err error) string {
	if err == nil {
		return ""
	}
	return "C: " + err.Error()
}

func resolveCompareEditor() (string, []string, bool) {
	if v := strings.TrimSpace(os.Getenv("MREVDIFF_COMPARE_EDITOR")); v != "" {
		if head, args, ok := resolveCompareEditorSpec(v); ok {
			return head, args, true
		}
	}
	for _, candidate := range compareEditorCandidates {
		if head, args, ok := resolveCompareEditorSpec(candidate); ok {
			return head, args, true
		}
	}
	return "", nil, false
}

func resolveCompareEditorSpec(spec string) (string, []string, bool) {
	tokens := ui.ParseShellArgs(spec)
	if len(tokens) == 0 {
		return "", nil, false
	}
	head, err := exec.LookPath(tokens[0])
	if err != nil {
		return "", nil, false
	}
	return head, tokens[1:], true
}

func buildCompareEditorArgv(editor string, userArgs []string, target compareTarget) []string {
	oldPath := target.OldPath
	newPath := target.NewPath
	if compareEditorUsesLineSuffix(editor) {
		oldPath = pathWithLine(target.OldPath, target.OldLine)
		newPath = pathWithLine(target.NewPath, target.NewLine)
	}
	argv := append([]string{}, userArgs...)
	argv = append(argv, oldPath, newPath)
	return argv
}

func compareEditorUsesLineSuffix(editor string) bool {
	return strings.EqualFold(filepath.Base(editor), "zed")
}

func pathWithLine(path string, line int) string {
	if line < 1 {
		return path
	}
	return fmt.Sprintf("%s:%d", path, line)
}

func (m Model) compareTarget() (compareTarget, error) {
	if m.Review == nil {
		return compareTarget{}, errors.New("no review loaded")
	}
	if m.Review.Old.Path == "" {
		return compareTarget{}, errors.New("old endpoint has no materialized path")
	}
	if m.Review.New.Path == "" {
		return compareTarget{}, errors.New("new endpoint has no file path")
	}
	pair := m.CurrentPair()
	if pair == nil {
		return compareTarget{}, errors.New("no pair selected")
	}
	return compareTarget{
		OldPath: m.Review.Old.Path,
		NewPath: m.Review.New.Path,
		OldLine: m.compareLine(pair, true),
		NewLine: m.compareLine(pair, false),
	}, nil
}

func (m Model) compareLine(pair *diffreview.Pair, oldSide bool) int {
	if pair == nil {
		return 1
	}
	var block *parser.Block
	if oldSide {
		block = pair.Old
	} else {
		block = pair.New
	}
	if block != nil {
		return blockLine(block, m.SourceLineCursor)
	}
	if oldSide {
		return nearestOldLine(m.Review, m.Cursor)
	}
	return nearestNewLine(m.Review, m.Cursor)
}

func blockLine(block *parser.Block, offset int) int {
	if block == nil || block.StartLine < 1 {
		return 1
	}
	if offset < 1 {
		offset = 1
	}
	line := block.StartLine + offset - 1
	if block.EndLine > 0 && line > block.EndLine {
		return block.EndLine
	}
	return line
}

func nearestOldLine(review *diffreview.Review, cursor int) int {
	if review == nil {
		return 1
	}
	for i := cursor - 1; i >= 0; i-- {
		if line := blockEndLine(review.Pairs[i].Old); line > 0 {
			return line
		}
	}
	for i := cursor + 1; i < len(review.Pairs); i++ {
		if line := blockStartLine(review.Pairs[i].Old); line > 0 {
			return line
		}
	}
	return 1
}

func nearestNewLine(review *diffreview.Review, cursor int) int {
	if review == nil {
		return 1
	}
	for i := cursor + 1; i < len(review.Pairs); i++ {
		if line := blockStartLine(review.Pairs[i].New); line > 0 {
			return line
		}
	}
	for i := cursor - 1; i >= 0; i-- {
		if line := blockEndLine(review.Pairs[i].New); line > 0 {
			return line
		}
	}
	return 1
}

func blockStartLine(block *parser.Block) int {
	if block == nil || block.StartLine < 1 {
		return 0
	}
	return block.StartLine
}

func blockEndLine(block *parser.Block) int {
	if block == nil {
		return 0
	}
	if block.EndLine >= block.StartLine && block.EndLine > 0 {
		return block.EndLine
	}
	return blockStartLine(block)
}
