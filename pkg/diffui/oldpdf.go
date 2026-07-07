package diffui

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lenis2000/mrevdiff/pkg/build"
	"github.com/lenis2000/mrevdiff/pkg/diffreview"
	"github.com/lenis2000/mrevdiff/pkg/pdf"
	"github.com/lenis2000/mrevdiff/pkg/synctex"
)

// PDF pane sides for the x blink comparator.
const (
	pdfSideNew = iota
	pdfSideOld
)

// Old-PDF build lifecycle.
const (
	oldPDFNone = iota
	oldPDFBuilding
	oldPDFReady
	oldPDFFailed
)

type diffOldPDFMsg struct {
	Generation int
	Doc        *pdf.Doc
	Index      *synctex.Index
	Status     string
	OK         bool
}

// oldPDFBuildCommand builds the latexmk invocation for the old endpoint.
// A test seam: tests replace it with a command that copies fixture
// artifacts instead of running LaTeX.
var oldPDFBuildCommand = func(outDir, texPath string) string {
	return fmt.Sprintf("latexmk -pdf -synctex=1 -interaction=nonstopmode -halt-on-error -file-line-error -outdir=%s %s",
		shellQuoteArg(outDir), shellQuoteArg(texPath))
}

// shellQuoteArg single-quotes s for sh -c, escaping embedded quotes.
func shellQuoteArg(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// toggleOldPDF implements the x blink comparator: flip the PDF pane
// between the new build and the old endpoint's build, kicking off a lazy,
// cached compile of the old side on first use. Flipping is instant once
// both sides exist — frames render at the same pane geometry and swap by
// image id, so a changed subscript or shifted equation pops out the way a
// blink comparator makes a moving star pop out.
func (m Model) toggleOldPDF() (tea.Model, tea.Cmd) {
	// Flipping sides re-syncs the full-page view to the pair's page on the
	// side now shown (page counts can differ between old and new builds).
	m.pdfPageView = 0
	m.pdfPageViewAnchor = ""
	if m.pdfSide == pdfSideOld {
		m.pdfSide = pdfSideNew
		m.Status = "PDF: new side"
		return m.withPDFRender()
	}
	switch m.oldPDFState {
	case oldPDFReady:
		m.pdfSide = pdfSideOld
		m.Status = "PDF: old side — " + m.Review.Old.Label + " (x flips back)"
		return m.withPDFRender()
	case oldPDFBuilding:
		m.Status = "old PDF still building…"
		return m, nil
	}
	// oldPDFNone or oldPDFFailed: (re)start the build.
	if m.Review == nil || m.Review.Old.Path == "" {
		m.Status = "x: old endpoint has no source file to build"
		return m, nil
	}
	if m.NoBuild {
		m.Status = "x: old PDF build disabled by --no-build"
		return m, nil
	}
	m.oldPDFState = oldPDFBuilding
	m.oldPDFGen++
	gen := m.oldPDFGen
	old := m.Review.Old
	workDir := oldBuildWorkDir(m.Review)
	m.Status = "building old PDF (" + old.Label + ")…"
	return m, func() tea.Msg {
		return buildOldPDF(gen, old, workDir)
	}
}

// oldBuildWorkDir picks the directory latexmk runs in. The materialized
// old .tex lives under .mrevdiff/, so relative \input/\includegraphics/.bib
// references only resolve when the build runs from the real paper
// directory.
func oldBuildWorkDir(review *diffreview.Review) string {
	if path, ok := newEndpointBuildPath(review); ok {
		return filepath.Dir(path)
	}
	if review != nil && review.Old.RepoRoot != "" {
		return review.Old.RepoRoot
	}
	if review != nil && review.Old.Path != "" {
		return filepath.Dir(review.Old.Path)
	}
	return "."
}

// buildOldPDF compiles the old endpoint into a content-addressed cache dir
// (.mrevdiff/oldpdf-<hash>/) and opens the PDF+SyncTeX pair. The cache is
// keyed by the old source's content hash, so re-reviewing against the same
// base skips the compile entirely — including across sessions.
func buildOldPDF(gen int, old diffreview.Endpoint, workDir string) diffOldPDFMsg {
	texPath := old.Path
	sum := sha256.Sum256(old.Source)
	hash := hex.EncodeToString(sum[:])[:12]
	root := old.RepoRoot
	if root == "" {
		root = workDir
	}
	outDir := filepath.Join(root, ".mrevdiff", "oldpdf-"+hash)
	base := strings.TrimSuffix(filepath.Base(texPath), filepath.Ext(texPath))
	pdfPath := filepath.Join(outDir, base+".pdf")
	synctexPath := filepath.Join(outDir, base+".synctex.gz")

	warned := ""
	if !fileExists(pdfPath) || !fileExists(synctexPath) {
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return diffOldPDFMsg{Generation: gen, Status: "old PDF: " + err.Error()}
		}
		_, err := build.RunWith(build.Options{
			TexPath:  texPath,
			BuildCmd: oldPDFBuildCommand(outDir, texPath),
			Dir:      workDir,
		})
		if !fileExists(pdfPath) || !fileExists(synctexPath) {
			status := "old PDF build failed"
			if err != nil {
				status += " — " + shortDiffBuildWarning(err)
			}
			return diffOldPDFMsg{Generation: gen, Status: status}
		}
		// Artifacts exist despite a scan error (the old snapshot may have
		// undefined refs/cites relative to its own state) — usable crops
		// beat a hard failure here.
		if err != nil {
			warned = " (with warnings)"
		}
	}
	doc, err := pdf.Open(pdfPath)
	if err != nil {
		return diffOldPDFMsg{Generation: gen, Status: "old PDF: " + err.Error()}
	}
	idx, err := synctex.Open(synctexPath)
	if err != nil {
		_ = doc.Close()
		return diffOldPDFMsg{Generation: gen, Status: "old PDF synctex: " + err.Error()}
	}
	return diffOldPDFMsg{
		Generation: gen,
		Doc:        doc,
		Index:      idx,
		Status:     "old PDF ready — x flips old/new" + warned,
		OK:         true,
	}
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir() && st.Size() > 0
}

func (m Model) applyOldPDF(msg diffOldPDFMsg) (Model, tea.Cmd) {
	if msg.Generation != m.oldPDFGen {
		if msg.Doc != nil {
			_ = msg.Doc.Close()
		}
		return m, nil
	}
	if !msg.OK {
		m.oldPDFState = oldPDFFailed
		m.Status = msg.Status
		return m, nil
	}
	if m.OldPDF != nil && m.OldPDF != msg.Doc {
		_ = m.OldPDF.Close()
	}
	m.OldPDF = msg.Doc
	m.OldSynctex = msg.Index
	m.oldPDFState = oldPDFReady
	if m.Review != nil {
		populateDocPDFRegions(m.Review.OldDoc, msg.Index)
	}
	m.pdfSide = pdfSideOld
	m.Status = msg.Status
	return m.withPDFRender()
}
