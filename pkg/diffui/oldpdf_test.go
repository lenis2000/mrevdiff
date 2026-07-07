package diffui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lenis2000/mrevdiff/pkg/diffreview"
)

// withStubOldPDFBuild replaces the latexmk invocation with a copy of the
// sample fixtures, mirroring how pdf_test.go fakes new-side builds.
func withStubOldPDFBuild(t *testing.T) {
	t.Helper()
	saved := oldPDFBuildCommand
	samplePDF, err := filepath.Abs(filepath.Join("..", "..", "testdata", "sample.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	sampleSyncTeX, err := filepath.Abs(filepath.Join("..", "..", "testdata", "sample.synctex.gz"))
	if err != nil {
		t.Fatal(err)
	}
	oldPDFBuildCommand = func(outDir, texPath string) string {
		base := strings.TrimSuffix(filepath.Base(texPath), filepath.Ext(texPath))
		return fmt.Sprintf("cp %s %s && cp %s %s",
			shellQuoteArg(samplePDF), shellQuoteArg(filepath.Join(outDir, base+".pdf")),
			shellQuoteArg(sampleSyncTeX), shellQuoteArg(filepath.Join(outDir, base+".synctex.gz")))
	}
	t.Cleanup(func() { oldPDFBuildCommand = saved })
}

func oldPDFFixtureModel(t *testing.T) Model {
	t.Helper()
	dir := t.TempDir()
	oldTex := filepath.Join(dir, "paper.tex")
	src := []byte("\\documentclass{article}\\begin{document}old\\end{document}\n")
	if err := os.WriteFile(oldTex, src, 0o600); err != nil {
		t.Fatal(err)
	}
	review := fixtureReview()
	review.Old = diffreview.Endpoint{
		Kind: diffreview.GitBlob, Label: "HEAD:paper.tex", Spec: "HEAD:paper.tex",
		Path: oldTex, Source: src, Materialized: true, RepoRoot: dir,
	}
	m := New(review, Options{})
	m.Width, m.Height = 120, 40
	return m
}

// TestBlinkComparatorBuildsOnceAndFlips pins the x contract: first press
// kicks off the (cached) old build, the ready message flips to the old
// side, and subsequent presses toggle instantly.
func TestBlinkComparatorBuildsOnceAndFlips(t *testing.T) {
	withStubOldPDFBuild(t)
	m := oldPDFFixtureModel(t)

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = next.(Model)
	if m.oldPDFState != oldPDFBuilding || cmd == nil {
		t.Fatalf("first x should start the old build, state=%d cmd=%v", m.oldPDFState, cmd)
	}

	// Run the build command synchronously (tests own the goroutine).
	msg := cmd()
	buildMsg, ok := msg.(diffOldPDFMsg)
	if !ok {
		t.Fatalf("expected diffOldPDFMsg, got %T", msg)
	}
	if !buildMsg.OK {
		t.Fatalf("stubbed old build should succeed, got status %q", buildMsg.Status)
	}
	next, _ = m.Update(buildMsg)
	m = next.(Model)
	if m.oldPDFState != oldPDFReady || m.OldPDF == nil || m.OldSynctex == nil {
		t.Fatalf("ready message should install the old artifacts, state=%d", m.oldPDFState)
	}
	if m.pdfSide != pdfSideOld {
		t.Fatalf("old PDF becoming ready should flip the pane to the old side")
	}
	t.Cleanup(func() { _ = m.OldPDF.Close() })

	m.Layout = LayoutThreeCol // fixture default hides the PDF pane
	view := m.View()
	if !strings.Contains(view, "PDF · OLD") {
		t.Fatalf("PDF pane title should indicate the old side:\n%s", view)
	}

	// Toggle back and forth — no rebuild involved.
	m = pressKey(t, m, "x")
	if m.pdfSide != pdfSideNew {
		t.Fatalf("x should flip back to the new side")
	}
	m = pressKey(t, m, "x")
	if m.pdfSide != pdfSideOld {
		t.Fatalf("x should flip to the ready old side without rebuilding")
	}

	// The artifacts are cached on disk: a fresh build call must reuse them
	// even when the build command would now fail.
	saved := oldPDFBuildCommand
	oldPDFBuildCommand = func(outDir, texPath string) string { return "false" }
	t.Cleanup(func() { oldPDFBuildCommand = saved })
	again := buildOldPDF(1, m.Review.Old, oldBuildWorkDir(m.Review))
	if !again.OK {
		t.Fatalf("cached old artifacts must be reused without rebuilding, got %q", again.Status)
	}
	_ = again.Doc.Close()
}

// TestBlinkComparatorAddedPairPlaceholder pins the old-side placeholder
// for pairs that have no old block.
func TestBlinkComparatorAddedPairPlaceholder(t *testing.T) {
	m := oldPDFFixtureModel(t)
	m.pdfSide = pdfSideOld
	// Move to the "added" pair (no old side).
	if idx := pairIndexByID(m.Review, "added"); idx >= 0 {
		m.Cursor = idx
	}
	body := m.pdfPaneBody()
	if !strings.Contains(body, addedPDFPlaceholder) {
		t.Fatalf("old side of an added pair should show the placeholder, got %q", body)
	}
}

// TestOldPDFRespectsNoBuild pins the --no-build guard.
func TestOldPDFRespectsNoBuild(t *testing.T) {
	m := oldPDFFixtureModel(t)
	m.NoBuild = true
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = next.(Model)
	if cmd != nil || m.oldPDFState != oldPDFNone {
		t.Fatalf("--no-build must prevent the old-side compile")
	}
	if !strings.Contains(m.Status, "--no-build") {
		t.Fatalf("status should explain the guard, got %q", m.Status)
	}
}
