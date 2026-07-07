package diffui

import (
	"fmt"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/lenis2000/mrevdiff/pkg/parser"
)

var writeDiffClipboard = clipboard.WriteAll

func (m Model) copySelectedChunk() (tea.Model, tea.Cmd) {
	text, side, ok := m.selectedChunkSource()
	if !ok {
		m.Status = "y: no source chunk to copy"
		return m, nil
	}
	if err := writeDiffClipboard(text); err != nil {
		m.Status = "y: " + err.Error()
		return m, nil
	}
	lineCount := blockLineCount(text)
	m.Status = fmt.Sprintf("copied %s chunk (%d line%s)", side, lineCount, pluralS(lineCount))
	return m, nil
}

func (m Model) selectedChunkSource() (text string, side string, ok bool) {
	pair := m.CurrentPair()
	if pair == nil {
		return "", "", false
	}
	if m.Focus == PaneOldSource {
		if text, ok := blockSourceText(pair.Old); ok {
			return text, "old", true
		}
		if text, ok := blockSourceText(pair.New); ok {
			return text, "new", true
		}
		return "", "", false
	}
	if text, ok := blockSourceText(pair.New); ok {
		return text, "new", true
	}
	if text, ok := blockSourceText(pair.Old); ok {
		return text, "old", true
	}
	return "", "", false
}

func blockSourceText(block *parser.Block) (string, bool) {
	if block == nil || block.Source == "" {
		return "", false
	}
	return block.Source, true
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
