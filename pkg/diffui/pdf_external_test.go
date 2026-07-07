package diffui

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lenis2000/mrevdiff/pkg/diffreview"
)

func TestDiffSkimHotkeyUsesSelectedNewLine(t *testing.T) {
	oldRunner := runDiffSkimForwardSearch
	defer func() { runDiffSkimForwardSearch = oldRunner }()

	var gotTex, gotPDF string
	var gotLine int
	runDiffSkimForwardSearch = func(texPath, pdfPath string, line int) tea.Cmd {
		gotTex, gotPDF, gotLine = texPath, pdfPath, line
		return func() tea.Msg { return diffSkimOpenFinishedMsg{} }
	}

	dir := t.TempDir()
	texPath := filepath.Join(dir, "paper.tex")
	pdfPath := filepath.Join(dir, "paper.pdf")
	if err := os.WriteFile(texPath, []byte("\\section{A}\nAlpha\nnew beta\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pdfPath, []byte("%PDF-1.7\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	review := fixtureReview()
	review.New = diffreview.Endpoint{Kind: diffreview.WorkingFile, Path: texPath, Editable: true}
	m := New(review, Options{})
	m.Cursor = pairIndexByID(m.Review, "changed")

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})
	nm, ok := next.(Model)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}
	if cmd == nil {
		t.Fatalf("S should start a Skim command")
	}
	if nm.Status != "opening new PDF in Skim" {
		t.Fatalf("status = %q", nm.Status)
	}
	if gotTex != texPath || gotPDF != pdfPath || gotLine != 4 {
		t.Fatalf("Skim target = (%q, %q, %d), want (%q, %q, 4)", gotTex, gotPDF, gotLine, texPath, pdfPath)
	}

	next, _ = nm.Update(cmd())
	nm, ok = next.(Model)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}
	if nm.Status != "opened new PDF in Skim" {
		t.Fatalf("finish status = %q", nm.Status)
	}
}

func TestDiffSkimHotkeyRequiresNewSourceLine(t *testing.T) {
	m := New(fixtureReview(), Options{})
	m.Cursor = pairIndexByID(m.Review, "deleted")
	m.Review.New = diffreview.Endpoint{Kind: diffreview.WorkingFile, Path: filepath.Join(t.TempDir(), "paper.tex")}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})
	if cmd != nil {
		t.Fatalf("deleted pair should not start a Skim command")
	}
	nm, ok := next.(Model)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}
	if nm.Status != "S: cursor has no resolvable new source line" {
		t.Fatalf("status = %q", nm.Status)
	}
}
