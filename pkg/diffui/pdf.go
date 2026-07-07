package diffui

import (
	"context"
	"errors"
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
	addedPDFPlaceholder   = "(added block — no old PDF location)"
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
	ImageID    uint32
	XferPath   string
	Status     string
}

type diffPDFReloadMsg struct {
	Generation int
	NewPDF     *pdf.Doc
	NewSyncTeX *synctex.Index
	Status     string
	BuildStale bool
	// Failed marks an actual rebuild failure (latexmk error, lmkf error or
	// timeout) as opposed to BuildStale's transient "rebuild in progress".
	Failed bool
	OldPDF *pdf.Doc
	Aux    map[string]parser.AuxEntry
	BBL    []parser.BibEntry
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
	Cache        *pdfEscCache
	PageLayout   *pageLayoutCache
	ReloadGen    int
	// SideKey namespaces frame-cache keys per PDF side ("n" new, "o" old):
	// an unchanged block has the same ID in both documents but renders
	// from different PDFs. Full-page frames add an "f" so they never
	// collide with region crops of the same block.
	SideKey string
	// FullPage renders the whole page (region marked) instead of a crop.
	FullPage bool
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
	// Keep the previous frame painted while the rebuild runs — blanking
	// the pane here is exactly the mid-recompile flicker the id-based
	// swap exists to avoid. The status line carries the progress.
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
	failed := false
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
				failed = true
			default:
				status = "lmkf didn't finish in time (edit saved anyway)"
				buildStale = true
				failed = true
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
				failed = true
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
		Failed:     failed,
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
	m.buildFailed = msg.Failed
	// The rebuilt document invalidates every cached frame; keep the last
	// painted frame on screen until the fresh render replaces it.
	m.escCache.clear()
	m.pageLayout.Invalidate()
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
	if !m.KittyAvailable {
		return nil
	}
	pair := m.CurrentPair()
	if pair == nil {
		return nil
	}
	// Side selection for the x blink comparator: identical geometry, only
	// the block/document/index swap.
	sideKey := "n"
	block := pair.New
	var doc *parser.Document
	if m.Review != nil {
		doc = m.Review.NewDoc
	}
	pdfDoc := m.PDF
	idx := m.Synctex
	if m.pdfSide == pdfSideOld && m.OldPDF != nil && m.OldSynctex != nil {
		sideKey = "o"
		block = pair.Old
		if m.Review != nil {
			doc = m.Review.OldDoc
		}
		pdfDoc = m.OldPDF
		idx = m.OldSynctex
	} else if m.BuildStale || m.PDF == nil || m.Synctex == nil {
		return nil
	}
	if block == nil {
		return nil
	}
	w, h := m.diffPDFPaneCells()
	if w <= 0 || h <= 0 {
		return nil
	}
	if m.pdfFullPage {
		// Full-page frames share the block key but must not collide with
		// the region crop of the same block.
		sideKey += "f"
	}
	// Resolve the page for the pane title (cheap map lookup; the frame
	// render resolves it again but this keeps the view pure).
	m.pdfPageShown = 0
	if block.StartLine >= 1 {
		file := block.File
		if file == "" && doc != nil {
			file = doc.File
		}
		if r := regionForBlockLines(idx, file, block.StartLine, block.EndLine); r != nil {
			m.pdfPageShown = r.Page
		}
	}
	cellW, cellH := pdf.DetectCellPixelSize()
	m.pdfGen++
	gen := m.pdfGen
	inputs := diffPDFRenderInputs{
		Block:        block,
		Doc:          doc,
		PDF:          pdfDoc,
		Index:        idx,
		WidthCells:   w,
		HeightCells:  h,
		CellWidthPx:  cellW,
		CellHeightPx: cellH,
		Cache:        m.escCache,
		PageLayout:   m.pageLayout,
		ReloadGen:    m.pdfReloadGen,
		SideKey:      sideKey,
		FullPage:     m.pdfFullPage,
	}
	key := diffPDFRenderKey(sideKey, block, m.pdfReloadGen, w, h, cellW, cellH)
	neighbors := m.neighborBlocks(pair.ID, sideKey == "o")
	// Register the in-flight render synchronously (Update goroutine) so a
	// quit that races the tick still waits for it in WaitPDFRenders.
	m.escCache.renderStarted()
	return tea.Tick(diffPDFRenderDebounce, func(time.Time) tea.Msg {
		defer inputs.Cache.renderDone()
		image, imageID, xferPath, status := renderDiffPDFFrame(inputs, key)
		if len(neighbors) > 0 {
			inputs.Cache.renderStarted()
			go warmNeighborFrames(inputs, neighbors)
		}
		return diffPDFRenderMsg{Generation: gen, Image: image, ImageID: imageID, XferPath: xferPath, Status: status}
	})
}

// WaitPDFRenders blocks until in-flight PDF render/prefetch goroutines
// finish (or the timeout elapses). The cmd layer calls this before
// removing the t=f transfer directory those goroutines write into.
func (m Model) WaitPDFRenders(timeout time.Duration) bool {
	return m.escCache.drainRenders(timeout)
}

// neighborBlocks returns the blocks (new side, or old side for the blink
// comparator) of up to two pairs on each side of pairID in review order —
// the prefetch set for j/k navigation.
func (m Model) neighborBlocks(pairID string, oldSide bool) []*parser.Block {
	if m.Review == nil {
		return nil
	}
	pairs := m.Review.Pairs
	cur := -1
	for i := range pairs {
		if pairs[i].ID == pairID {
			cur = i
			break
		}
	}
	if cur < 0 {
		return nil
	}
	sideBlock := func(p *diffreview.Pair) *parser.Block {
		if oldSide {
			return p.Old
		}
		return p.New
	}
	var out []*parser.Block
	appendFrom := func(start, step int) {
		taken := 0
		for i := start; i >= 0 && i < len(pairs) && taken < 2; i += step {
			if b := sideBlock(&pairs[i]); b != nil {
				out = append(out, b)
				taken++
			}
		}
	}
	appendFrom(cur+1, 1)
	appendFrom(cur-1, -1)
	return out
}

func (m Model) applyPDFRender(msg diffPDFRenderMsg) (Model, tea.Cmd) {
	if msg.Generation != m.pdfGen {
		return m, nil
	}
	image := msg.Image
	// Flicker-free swap: paint the new frame first, then retire the
	// previous image by id in the same write — the pane is never blank
	// between frames (the old delete-all-then-draw order blanked it).
	if image != "" && msg.ImageID != 0 && m.lastKittyID != 0 && m.lastKittyID != msg.ImageID {
		image += pdf.KittyDeleteByID(m.lastKittyID)
	}
	if msg.ImageID != 0 {
		m.lastKittyID = msg.ImageID
	}
	if image != "" {
		// Pin the transfer file behind the frame we are about to paint:
		// the terminal re-reads that path on every repaint, so the cache
		// must never unlink it while PDFImage references it.
		m.escCache.pin(msg.XferPath)
	}
	m.PDFImage = image
	m.PDFStatus = msg.Status
	return m, nil
}

func (m Model) pdfPaneBody() string {
	pair := m.CurrentPair()
	if m.pdfSide == pdfSideOld {
		if pair != nil && pair.Old == nil {
			if m.KittyAvailable {
				return m.kittyClear() + addedPDFPlaceholder
			}
			return addedPDFPlaceholder
		}
	} else if pair != nil && pair.New == nil {
		if m.KittyAvailable {
			return m.kittyClear() + deletedPDFPlaceholder
		}
		return deletedPDFPlaceholder
	}
	if !m.KittyAvailable {
		return "(PDF pane requires kitty or ghostty terminal)"
	}
	// A stale frame beats a blank pane: during rebuild/reload the last
	// rendered crop stays painted and the status line reports progress.
	if m.PDFImage != "" {
		return m.PDFImage
	}
	if m.PDFStatus != "" {
		return m.kittyClear() + m.PDFStatus
	}
	if m.BuildStale {
		return m.kittyClear() + "(new PDF needs rebuild)"
	}
	if m.PDF == nil || m.Synctex == nil {
		return m.kittyClear() + "(new PDF not loaded)"
	}
	return m.kittyClear() + pdf.NoRegionPlaceholder
}

// kittyClear retires the last painted frame before a text placeholder is
// shown. Targeted by id when one is known (cheap, leaves other kitty
// clients alone); delete-all otherwise.
func (m Model) kittyClear() string {
	if m.lastKittyID != 0 {
		return pdf.KittyDeleteByID(m.lastKittyID)
	}
	return pdf.KittyDeleteAll
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
	case LayoutSourcesPDF:
		paneW = m.Width
		_, paneH = m.stackedHeights(paneH)
	case LayoutNewPDF:
		_, paneW = m.newPDFWidths(m.Width)
	case LayoutPDFOnly:
		paneW = m.Width
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
	// Mid-recompile guard: latexmk rewrites the PDF in place, so a file
	// that exists but has no %%EOF trailer is still being written. Give
	// the writer a short window to finish, then refuse to open a torn
	// file — the caller keeps the previous document and marks the build
	// stale instead of feeding MuPDF a partial parse.
	if res.PDFPath != "" {
		if _, err := os.Stat(res.PDFPath); err == nil && !pdf.LooksComplete(res.PDFPath) {
			pdf.WaitStable(res.PDFPath, 2*time.Second)
			if !pdf.LooksComplete(res.PDFPath) {
				return nil, nil, "(new PDF is still being written — press B to retry)", true
			}
		}
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
	if review == nil {
		return
	}
	populateDocPDFRegions(review.NewDoc, idx)
}

func populateDocPDFRegions(doc *parser.Document, idx *synctex.Index) {
	if doc == nil || idx == nil {
		return
	}
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
