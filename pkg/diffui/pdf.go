package diffui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"mrevdiff/pkg/build"
	"mrevdiff/pkg/diffreview"
	"mrevdiff/pkg/parser"
	"mrevdiff/pkg/pdf"
	"mrevdiff/pkg/synctex"
	"mrevdiff/pkg/ui"
)

const (
	diffPDFRenderDebounce = 30 * time.Millisecond
	deletedPDFPlaceholder = "(deleted block — no new PDF location)"
)

// PDFOptions controls startup PDF preparation for diff mode.
type PDFOptions struct {
	NoBuild  bool
	Draft    bool
	BuildCmd string
	Stderr   io.Writer
	Ctx      context.Context
}

// PDFArtifacts contains the new-side PDF handles and startup status.
type PDFArtifacts struct {
	PDF        *pdf.Doc
	Synctex    *synctex.Index
	Status     string
	BuildStale bool
	Result     *build.Result
}

type diffPDFRenderMsg struct {
	Generation int
	Image      string
	Status     string
}

type diffPDFReloadMsg struct {
	Generation int
	NewPDF     *pdf.Doc
	NewSyncTeX *synctex.Index
	Status     string
	BuildStale bool
	OldPDF     *pdf.Doc
	Aux        map[string]parser.AuxEntry
	BBL        []parser.BibEntry
}

type diffPDFRenderInputs struct {
	Block        *parser.Block
	Doc          *parser.Document
	PDF          *pdf.Doc
	Index        *synctex.Index
	WidthCells   int
	HeightCells  int
	CellWidthPx  float64
	CellHeightPx float64
}

// PrepareNewPDF builds or opens the new endpoint's existing PDF artifacts.
// Diff mode only supports this for real filesystem new endpoints; git blob
// endpoints are materialized snapshots and are intentionally not built.
func PrepareNewPDF(review *diffreview.Review, opts PDFOptions) (*PDFArtifacts, error) {
	path, ok := newEndpointBuildPath(review)
	if !ok {
		return &PDFArtifacts{}, nil
	}
	res := build.ResolveBuildOutputsOnDisk(path)
	status := ""
	buildStale := false
	lmkfActive := ui.LmkfWatching(path)
	if !opts.NoBuild && !lmkfActive {
		buildCmd := opts.BuildCmd
		runRes, err := build.RunWith(build.Options{
			TexPath:  path,
			BuildCmd: buildCmd,
			Stderr:   opts.Stderr,
			Ctx:      opts.Ctx,
		})
		if runRes != nil {
			res = runRes
		}
		if err != nil {
			if !opts.Draft {
				return nil, err
			}
			status = "build: " + shortDiffBuildWarning(err)
			if diffStartupArtifactsStale(path, res.PDFPath, res.SyncTeXPath) {
				buildStale = true
			}
		}
	} else if lmkfActive {
		status = "lmkf is building this paper — skipped own latexmk"
		if diffStartupArtifactsStale(path, res.PDFPath, res.SyncTeXPath) {
			buildStale = true
		}
	}
	applyBuildMetadata(review, res)
	pdfDoc, idx, openStatus, openStale := openDiffPDFPair(res)
	if openStatus != "" && status == "" {
		status = openStatus
	}
	if openStale {
		buildStale = true
	}
	if idx != nil {
		populateNewPDFRegions(review, idx)
	}
	return &PDFArtifacts{
		PDF:        pdfDoc,
		Synctex:    idx,
		Status:     status,
		BuildStale: buildStale,
		Result:     res,
	}, nil
}

func (m Model) startPDFReload(runBuild bool) (Model, tea.Cmd) {
	path, ok := newEndpointBuildPath(m.Review)
	if !ok {
		m.Status = "B: new endpoint is not a filesystem file"
		return m, nil
	}
	m.pdfReloadGen++
	gen := m.pdfReloadGen
	oldPDF := m.PDF
	buildCmd := m.BuildCmd
	m.PDFImage = ""
	m.PDFStatus = "rebuilding new PDF…"
	m.BuildStale = true
	if runBuild {
		m.Status = "rebuilding new PDF…"
	} else {
		m.Status = "reloading new PDF artifacts…"
	}
	return m, func() tea.Msg {
		return performDiffPDFReload(path, gen, oldPDF, buildCmd, runBuild)
	}
}

func performDiffPDFReload(path string, gen int, oldPDF *pdf.Doc, buildCmd string, runBuild bool) diffPDFReloadMsg {
	res := build.ResolveBuildOutputsOnDisk(path)
	status := ""
	buildStale := false
	if runBuild {
		editTime := diffSourceMTime(path)
		if waitRes, lmkf := ui.AwaitLmkfRebuild(path, editTime, 2*time.Minute); lmkf.Status != ui.LmkfRebuildNotWatching {
			if waitRes != nil {
				res = waitRes
			}
			switch lmkf.Status {
			case ui.LmkfRebuildOK:
				status = "lmkf rebuild ok"
			case ui.LmkfRebuildError:
				status = "lmkf rebuild error — " + lmkf.ErrorLine
				buildStale = true
			default:
				status = "lmkf didn't finish in time (edit saved anyway)"
				buildStale = true
			}
		} else {
			runRes, err := build.RunWith(build.Options{
				TexPath:  path,
				BuildCmd: buildCmd,
			})
			if runRes != nil {
				res = runRes
			}
			if err != nil {
				status = "rebuild failed — " + shortDiffBuildWarning(err)
				if diffStartupArtifactsStale(path, res.PDFPath, res.SyncTeXPath) {
					buildStale = true
				}
			} else {
				status = "rebuilt new PDF"
			}
		}
	}
	var pdfDoc *pdf.Doc
	var idx *synctex.Index
	var aux map[string]parser.AuxEntry
	var bbl []parser.BibEntry
	if !buildStale {
		var openStatus string
		var openStale bool
		pdfDoc, idx, openStatus, openStale = openDiffPDFPair(res)
		if openStatus != "" {
			status = openStatus
		}
		if openStale {
			buildStale = true
		}
		if runBuild && !buildStale && pdfDoc == nil && idx == nil {
			status = "(new PDF not loaded)"
			buildStale = true
		}
		if !buildStale {
			aux, _ = parser.LoadAux(res.AuxPath)
			bbl, _ = parser.LoadBBL(res.BBLPath)
		}
	}
	return diffPDFReloadMsg{
		Generation: gen,
		NewPDF:     pdfDoc,
		NewSyncTeX: idx,
		Status:     status,
		BuildStale: buildStale,
		OldPDF:     oldPDF,
		Aux:        aux,
		BBL:        bbl,
	}
}

func (m Model) applyPDFReload(msg diffPDFReloadMsg) (Model, tea.Cmd) {
	if msg.Generation != m.pdfReloadGen {
		if msg.NewPDF != nil {
			_ = msg.NewPDF.Close()
		}
		return m, nil
	}
	if !msg.BuildStale && m.Review != nil && m.Review.NewDoc != nil {
		if msg.Aux != nil {
			parser.ApplyAux(m.Review.NewDoc, msg.Aux)
		}
		if msg.BBL != nil {
			parser.ApplyBBL(m.Review.NewDoc, msg.BBL)
		}
	}
	completePDFPair := msg.NewPDF != nil && msg.NewSyncTeX != nil
	if completePDFPair {
		if msg.OldPDF != nil && msg.OldPDF != msg.NewPDF {
			_ = msg.OldPDF.Close()
		}
		m.PDF = msg.NewPDF
		m.Synctex = msg.NewSyncTeX
		populateNewPDFRegions(m.Review, msg.NewSyncTeX)
	} else {
		if msg.NewPDF != nil {
			_ = msg.NewPDF.Close()
		}
		if !msg.BuildStale {
			oldPDF := msg.OldPDF
			if oldPDF == nil {
				oldPDF = m.PDF
			}
			if oldPDF != nil {
				_ = oldPDF.Close()
			}
			m.PDF = nil
			m.Synctex = nil
		}
	}
	m.BuildStale = msg.BuildStale
	m.PDFImage = ""
	m.PDFStatus = ""
	if msg.Status != "" {
		m.Status = msg.Status
	}
	if msg.BuildStale {
		if m.PDFStatus == "" {
			m.PDFStatus = "(new PDF needs rebuild)"
		}
		return m, nil
	}
	if m.PDF == nil || m.Synctex == nil {
		m.PDFStatus = "(new PDF not loaded)"
		return m, nil
	}
	return m.withPDFRender()
}

func (m Model) withPDFRender() (Model, tea.Cmd) {
	cmd := (&m).schedulePDFRender()
	return m, cmd
}

func (m *Model) schedulePDFRender() tea.Cmd {
	if m.BuildStale || m.PDF == nil || m.Synctex == nil || !m.KittyAvailable {
		return nil
	}
	pair := m.CurrentPair()
	if pair == nil || pair.New == nil {
		return nil
	}
	w, h := m.diffPDFPaneCells()
	if w <= 0 || h <= 0 {
		return nil
	}
	cellW, cellH := pdf.DetectCellPixelSize()
	m.pdfGen++
	gen := m.pdfGen
	inputs := diffPDFRenderInputs{
		Block:        pair.New,
		Doc:          m.Review.NewDoc,
		PDF:          m.PDF,
		Index:        m.Synctex,
		WidthCells:   w,
		HeightCells:  h,
		CellWidthPx:  cellW,
		CellHeightPx: cellH,
	}
	return tea.Tick(diffPDFRenderDebounce, func(time.Time) tea.Msg {
		image, status := renderDiffPDFForBlock(inputs)
		return diffPDFRenderMsg{Generation: gen, Image: image, Status: status}
	})
}

func (m Model) applyPDFRender(msg diffPDFRenderMsg) (Model, tea.Cmd) {
	if msg.Generation != m.pdfGen {
		return m, nil
	}
	m.PDFImage = msg.Image
	m.PDFStatus = msg.Status
	return m, nil
}

func renderDiffPDFForBlock(in diffPDFRenderInputs) (string, string) {
	if in.Block == nil || in.Block.StartLine < 1 {
		return "", pdf.NoRegionPlaceholder
	}
	file := in.Block.File
	if file == "" && in.Doc != nil {
		file = in.Doc.File
	}
	region := in.Index.RegionForLines(file, in.Block.StartLine, in.Block.EndLine)
	if region == nil || !pdf.HasExtent(*region) {
		return "", pdf.NoRegionPlaceholder
	}
	paneWPx := int(float64(in.WidthCells) * in.CellWidthPx)
	paneHPx := int(float64(in.HeightCells) * in.CellHeightPx)
	png, err := pdf.CropFitted(in.PDF, *region, pdf.FitOptions{
		PaneWidthPx:  paneWPx,
		PaneHeightPx: paneHPx,
	})
	if err != nil {
		return "", fmt.Sprintf("pdf: %v", err)
	}
	esc, err := pdf.RenderKitty(png, in.WidthCells, in.HeightCells)
	if err != nil {
		return "", fmt.Sprintf("pdf: %v", err)
	}
	return esc, ""
}

func (m Model) pdfPaneBody() string {
	pair := m.CurrentPair()
	if pair != nil && pair.New == nil {
		if m.KittyAvailable {
			return pdf.KittyDeleteAll + deletedPDFPlaceholder
		}
		return deletedPDFPlaceholder
	}
	if !m.KittyAvailable {
		return "(PDF pane requires kitty or ghostty terminal)"
	}
	if m.PDFImage != "" && !m.BuildStale {
		return m.PDFImage
	}
	if m.PDFStatus != "" {
		return pdf.KittyDeleteAll + m.PDFStatus
	}
	if m.BuildStale {
		return pdf.KittyDeleteAll + "(new PDF needs rebuild)"
	}
	if m.PDF == nil || m.Synctex == nil {
		return pdf.KittyDeleteAll + "(new PDF not loaded)"
	}
	return pdf.KittyDeleteAll + pdf.NoRegionPlaceholder
}

// diffPDFPaneCells keeps the original package-level geometry helper for tests
// that do not need resized/staked model state.
func diffPDFPaneCells(termW, termH int) (int, int) {
	return Model{Width: termW, Height: termH}.diffPDFPaneCells()
}

func (m Model) diffPDFPaneCells() (int, int) {
	if m.Width <= 0 || m.Height <= 0 {
		return 0, 0
	}
	paneH := m.Height - statusBarHeight
	if paneH < 1 {
		paneH = 1
	}
	var paneW int
	switch m.Layout {
	case LayoutNoPDF:
		return 0, 0
	case LayoutStacked:
		_, paneW = m.stackedWidths(m.Width)
		_, paneH = m.stackedHeights(paneH)
	default:
		_, _, paneW = m.paneWidths(m.Width)
	}
	innerW := paneW - 2
	if innerW < 1 {
		innerW = 1
	}
	innerH := paneH - 3
	if innerH < 1 {
		innerH = 1
	}
	return innerW, innerH
}

func newEndpointBuildPath(review *diffreview.Review) (string, bool) {
	if review == nil || review.New.Kind != diffreview.WorkingFile || review.New.Path == "" {
		return "", false
	}
	return review.New.Path, true
}

func applyBuildMetadata(review *diffreview.Review, res *build.Result) {
	if review == nil || review.NewDoc == nil || res == nil {
		return
	}
	if auxEntries, err := parser.LoadAux(res.AuxPath); err == nil {
		parser.ApplyAux(review.NewDoc, auxEntries)
	}
	if bibEntries, err := parser.LoadBBL(res.BBLPath); err == nil {
		parser.ApplyBBL(review.NewDoc, bibEntries)
	}
}

func openDiffPDFPair(res *build.Result) (*pdf.Doc, *synctex.Index, string, bool) {
	if res == nil {
		return nil, nil, "", false
	}
	pdfDoc, pdfErr := pdf.Open(res.PDFPath)
	idx, sxErr := synctex.Open(res.SyncTeXPath)
	if pdfErr == nil && sxErr == nil {
		return pdfDoc, idx, "", false
	}
	if pdfDoc != nil {
		_ = pdfDoc.Close()
	}
	if errors.Is(pdfErr, os.ErrNotExist) && errors.Is(sxErr, os.ErrNotExist) {
		return nil, nil, "", false
	}
	return nil, nil, "(new PDF not loaded)", true
}

func populateNewPDFRegions(review *diffreview.Review, idx *synctex.Index) {
	if review == nil || review.NewDoc == nil || idx == nil {
		return
	}
	doc := review.NewDoc
	for _, b := range doc.Blocks {
		if b == nil || b == doc.Root || b.StartLine == 0 {
			continue
		}
		file := b.File
		if file == "" {
			file = doc.File
		}
		r := idx.RegionForLines(file, b.StartLine, b.EndLine)
		if r == nil {
			continue
		}
		b.PDFRegion = &parser.Region{Page: r.Page, X: r.X, Y: r.Y, W: r.W, H: r.H}
	}
}

func diffSourceMTime(texPath string) time.Time {
	if st, err := os.Stat(texPath); err == nil {
		return st.ModTime()
	}
	return time.Now()
}

func diffStartupArtifactsStale(texPath, pdfPath, synctexPath string) bool {
	ti, err := os.Stat(texPath)
	if err != nil {
		return true
	}
	pi, perr := os.Stat(pdfPath)
	si, serr := os.Stat(synctexPath)
	if perr != nil && serr != nil {
		return true
	}
	if perr == nil && pi.ModTime().Before(ti.ModTime()) {
		return true
	}
	if serr == nil && si.ModTime().Before(ti.ModTime()) {
		return true
	}
	return false
}

func shortDiffBuildWarning(err error) string {
	msg := err.Error()
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		return msg[:i]
	}
	return msg
}
