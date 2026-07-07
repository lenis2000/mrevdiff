package diffui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lenis2000/mrevdiff/pkg/build"
)

const diffSkimDisplaylinePath = "/Applications/Skim.app/Contents/SharedSupport/displayline"

var runDiffPDFOpenProcess = func(cmd *exec.Cmd) tea.Cmd {
	return func() tea.Msg {
		if err := cmd.Start(); err != nil {
			return diffPDFOpenFinishedMsg{err: err}
		}
		go func() { _ = cmd.Wait() }()
		return diffPDFOpenFinishedMsg{}
	}
}

var runDiffSkimForwardSearch = func(texPath, pdfPath string, line int) tea.Cmd {
	return func() tea.Msg {
		return diffSkimOpenFinishedMsg{err: performDiffSkimForwardSearch(texPath, pdfPath, line)}
	}
}

type diffPDFOpenFinishedMsg struct {
	err error
}

type diffSkimOpenFinishedMsg struct {
	err error
}

func (m Model) openPreviewPDF() (tea.Model, tea.Cmd) {
	path, err := m.newPDFPath()
	if err != nil {
		m.Status = "P: " + err.Error()
		return m, nil
	}
	m.Status = "opening new PDF in Preview"
	return m, runDiffPDFOpenProcess(exec.Command("open", "-a", "Preview", path))
}

func (m Model) applyPDFOpenFinished(msg diffPDFOpenFinishedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.Status = "P: " + msg.err.Error()
		return m, nil
	}
	m.Status = "opened new PDF in Preview"
	return m, nil
}

func (m Model) openSkimAtLine() (tea.Model, tea.Cmd) {
	texPath, ok := newEndpointBuildPath(m.Review)
	if !ok {
		m.Status = "S: new endpoint is not a filesystem file"
		return m, nil
	}
	line := m.currentNewLine()
	if line < 1 {
		m.Status = "S: cursor has no resolvable new source line"
		return m, nil
	}
	pdfPath, err := m.newPDFPath()
	if err != nil {
		m.Status = "S: " + err.Error()
		return m, nil
	}
	m.Status = "opening new PDF in Skim"
	return m, runDiffSkimForwardSearch(texPath, pdfPath, line)
}

func (m Model) applySkimOpenFinished(msg diffSkimOpenFinishedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.Status = "S: " + msg.err.Error()
		return m, nil
	}
	m.Status = "opened new PDF in Skim"
	return m, nil
}

func (m Model) newPDFPath() (string, error) {
	if m.PDF != nil && m.PDF.Path() != "" {
		return m.PDF.Path(), nil
	}
	texPath, ok := newEndpointBuildPath(m.Review)
	if !ok {
		return "", errors.New("new endpoint is not a filesystem file")
	}
	res := build.ResolveBuildOutputsOnDisk(texPath)
	if res == nil || res.PDFPath == "" {
		return "", errors.New("new PDF path unavailable")
	}
	if _, err := os.Stat(res.PDFPath); err != nil {
		return "", fmt.Errorf("new PDF not found: %w", err)
	}
	return res.PDFPath, nil
}

func performDiffSkimForwardSearch(texPath, pdfPath string, line int) error {
	if line < 1 {
		return errors.New("source line unavailable")
	}
	pdfPath, err := filepath.Abs(pdfPath)
	if err != nil {
		return fmt.Errorf("resolve PDF path: %w", err)
	}
	if _, err := os.Stat(pdfPath); err != nil {
		return fmt.Errorf("new PDF not found: %w", err)
	}
	if texPath != "" {
		if abs, err := filepath.Abs(texPath); err == nil {
			texPath = abs
		}
	}
	if _, err := os.Stat(diffSkimDisplaylinePath); err != nil {
		return fmt.Errorf("Skim displayline not found: %w", err)
	}
	if err := reloadDiffSkimDocument(pdfPath); err != nil {
		if openErr := exec.Command("open", "-a", "Skim", pdfPath).Run(); openErr != nil {
			return fmt.Errorf("reload Skim: %w", err)
		}
	}
	args := []string{strconv.Itoa(line), pdfPath}
	if texPath != "" {
		args = append(args, texPath)
	}
	if err := exec.Command(diffSkimDisplaylinePath, args...).Run(); err != nil {
		return fmt.Errorf("displayline: %w", err)
	}
	_ = exec.Command("osascript", "-e", `tell application "Skim" to activate`).Run()
	return nil
}

func reloadDiffSkimDocument(pdfPath string) error {
	script := fmt.Sprintf(`set theFile to POSIX file %q
tell application "Skim"
  try
    set theDocs to documents whose path is (get POSIX path of theFile)
    repeat with d in theDocs
      revert d
    end repeat
  end try
  open theFile
end tell`, pdfPath)
	return exec.Command("osascript", "-e", script).Run()
}
