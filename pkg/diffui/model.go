package diffui

import (
	"fmt"
	"os"
	"sort"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"mrevdiff/pkg/diffreview"
	"mrevdiff/pkg/pdf"
	"mrevdiff/pkg/synctex"
	"mrevdiff/pkg/ui"
)

// Filter selects which semantic diff pairs are visible in the outline.
type Filter int

const (
	// FilterChanged is the default diff-review view.
	FilterChanged Filter = iota
	FilterAll
	FilterUnreviewed
	FilterAnnotated
	FilterIssues
)

// String returns the filter label shown in the status bar.
func (f Filter) String() string {
	switch f {
	case FilterAll:
		return "all"
	case FilterChanged:
		return "changed"
	case FilterUnreviewed:
		return "unreviewed"
	case FilterAnnotated:
		return "annotated"
	case FilterIssues:
		return "issues"
	default:
		return "changed"
	}
}

// CycleFilter rotates through the diff outline filters.
func CycleFilter(f Filter) Filter {
	switch f {
	case FilterAll:
		return FilterChanged
	case FilterChanged:
		return FilterUnreviewed
	case FilterUnreviewed:
		return FilterAnnotated
	case FilterAnnotated:
		return FilterIssues
	default:
		return FilterAll
	}
}

// DiffRegime selects how the diff outline groups semantic pairs.
type DiffRegime int

const (
	// DiffRegimeSemantic shows the raw semantic block pairs.
	DiffRegimeSemantic DiffRegime = iota
	// DiffRegimeCoalesced groups adjacent added/deleted prose into rewrite hunks.
	DiffRegimeCoalesced
)

func (r DiffRegime) String() string {
	switch r {
	case DiffRegimeCoalesced:
		return "coalesced"
	default:
		return "semantic"
	}
}

func CycleDiffRegime(r DiffRegime) DiffRegime {
	if r == DiffRegimeCoalesced {
		return DiffRegimeSemantic
	}
	return DiffRegimeCoalesced
}

// Pane identifies a focusable diff pane. Focus drives pane resizing with
// < and >, and is shown by a brighter border when styles provide one.
type Pane int

const (
	PaneOutline Pane = iota
	PaneOldSource
	PaneNewSource
	PanePDF
)

func (p Pane) String() string {
	switch p {
	case PaneOutline:
		return "outline"
	case PaneOldSource:
		return "old"
	case PaneNewSource:
		return "new"
	case PanePDF:
		return "pdf"
	default:
		return "source"
	}
}

// LayoutMode selects the top-level diff pane arrangement.
type LayoutMode int

const (
	// LayoutThreeCol renders outline | old | new | PDF.
	LayoutThreeCol LayoutMode = iota
	// LayoutStacked renders outline full-height on the left, with old/new source
	// above the new-side PDF on the right.
	LayoutStacked
	// LayoutNoPDF renders outline | old | new, while keeping build/reload behavior
	// unchanged; only the PDF pane is hidden.
	LayoutNoPDF
	// LayoutSourcesPDF drops the outline: old | new full-width on top, PDF
	// below. Maximum horizontal room for the diff itself.
	LayoutSourcesPDF
	// LayoutNewPDF drops the outline AND the old side: new source | PDF.
	// The reading/edit-in-place mode — current text next to its rendered
	// result.
	LayoutNewPDF
	// LayoutPDFOnly zooms the PDF pane to the whole terminal. Not part of
	// the `\` cycle — toggled directly with `|` and remembers the layout
	// it interrupted.
	LayoutPDFOnly
)

// Options configures a new diff TUI model.
type Options struct {
	Config             *ui.Config
	Styles             ui.Styles
	Filter             Filter
	DiffRegime         DiffRegime
	Sidecar            *diffreview.Sidecar
	SidecarBase        *diffreview.Sidecar
	Reviewed           map[string]bool
	Annotations        map[string]string
	Issues             map[string][]string
	AllowModifications bool
	RequestedAllowMods bool
	Status             string
	NoBuild            bool
	Draft              bool
	BuildCmd           string
	SidecarPath        string
	StdoutFormat       string
	OpenCompare        bool
	PDF                *pdf.Doc
	Synctex            *synctex.Index
	KittyAvailable     bool
	BuildStale         bool
	PDFStatus          string
	// KittyXferDir, when non-empty, enables kitty t=f file transmission:
	// rendered frames are written as PNGs under this directory and the
	// escape carries only the file path instead of base64 pixel data.
	// The caller owns the directory (created before the TUI starts,
	// removed after it exits).
	KittyXferDir string
}

// Model is the Bubble Tea state for the semantic diff-review skeleton.
type Model struct {
	Review *diffreview.Review
	Config *ui.Config

	Cursor     int
	Filter     Filter
	DiffRegime DiffRegime
	Collapsed  map[string]bool
	// SourceLineCursor is 1-based within the selected new block. The current
	// diff skeleton does not expose source-line navigation yet, so it defaults
	// to the first line and is kept here for edit anchoring.
	SourceLineCursor int

	Width, Height   int
	Status          string
	Styles          ui.Styles
	Layout          LayoutMode
	Focus           Pane
	OutlineFrac     float64
	PDFFrac         float64
	StackedTopFrac  float64
	SourceSplitFrac float64

	Sidecar            *diffreview.Sidecar
	SidecarBase        *diffreview.Sidecar
	Reviewed           map[string]bool
	Annotations        map[string]string
	Issues             map[string][]string
	AllowModifications bool
	RequestedAllowMods bool

	NoBuild        bool
	Draft          bool
	BuildCmd       string
	SidecarPath    string
	StdoutFormat   string
	OpenCompare    bool
	PDF            *pdf.Doc
	Synctex        *synctex.Index
	BuildStale     bool
	PDFImage       string
	PDFStatus      string
	pdfGen         int
	pdfReloadGen   int
	KittyAvailable bool
	// lastKittyID is the image id of the frame currently painted in the
	// PDF pane; the next frame deletes it *after* drawing (flicker-free
	// swap). 0 = nothing painted yet.
	lastKittyID uint32
	// escCache memoises rendered frames (and owns their t=f files).
	escCache *pdfEscCache
	// pageLayout memoises per-page column detection for column-aware crops.
	pageLayout *pageLayoutCache
	// prevLayout remembers the layout interrupted by the `|` PDF-only
	// zoom so the second `|` restores it.
	prevLayout LayoutMode

	ShowHelp bool
	// CountBuf accumulates digit prefixes for Vim-style diff motions (e.g. "10j").
	CountBuf string
	pendingG bool
	quitting bool

	// Discarded is set by the Q key (pressed twice): the caller must then
	// skip the sidecar save and the stdout emit, leaving on-disk state as
	// it was when the session started. In-place e/E file edits are NOT
	// rolled back — they were written to the .tex at submit time.
	Discarded bool
	// discardArmed is true after the first Q press; any other key or
	// mouse event disarms.
	discardArmed bool

	// mouseWheelEdge remembers the last no-op wheel event at a scroll edge.
	// cmd/mrevdiff's Bubble Tea filter uses it to drop repeated same-direction
	// mouse wheel messages before they force another full View recomputation.
	mouseWheelEdge mouseWheelEdgeState

	Popup    *AnnotationPopup
	LineEdit *LineEditPopup
	Pending  *PendingDelete

	EditUndo []EditSnapshot
	EditRedo []EditSnapshot
	OpSeq    int
}

// AnnotationPopup is the block-level diff annotation editor.
type AnnotationPopup struct {
	TA      textarea.Model
	PairID  string
	Editing bool
}

// PendingDelete records a pending annotation delete confirmation.
type PendingDelete struct {
	PairID string
}

// New constructs a diff TUI model with the changed filter selected by
// default and the cursor snapped to the first visible semantic pair.
func New(review *diffreview.Review, opts Options) Model {
	side := opts.Sidecar
	if side == nil {
		side = diffreview.NewSidecar(review)
	}
	sidecarBase := opts.SidecarBase
	if sidecarBase == nil {
		sidecarBase = side
	}
	reviewed := side.ReviewedSet()
	for id, v := range opts.Reviewed {
		reviewed[id] = v
	}
	annotations := side.AnnotationNotes()
	for id, note := range opts.Annotations {
		annotations[id] = note
	}
	m := Model{
		Review:             review,
		Config:             opts.Config,
		Filter:             opts.Filter,
		DiffRegime:         opts.DiffRegime,
		Collapsed:          map[string]bool{},
		Status:             opts.Status,
		Styles:             opts.Styles,
		Sidecar:            side,
		SidecarBase:        diffreview.CloneSidecar(sidecarBase),
		Reviewed:           reviewed,
		Annotations:        annotations,
		Issues:             copyIssueMap(opts.Issues),
		AllowModifications: opts.AllowModifications,
		RequestedAllowMods: opts.RequestedAllowMods,
		NoBuild:            opts.NoBuild,
		Draft:              opts.Draft,
		BuildCmd:           opts.BuildCmd,
		SidecarPath:        opts.SidecarPath,
		StdoutFormat:       opts.StdoutFormat,
		OpenCompare:        opts.OpenCompare,
		PDF:                opts.PDF,
		Synctex:            opts.Synctex,
		BuildStale:         opts.BuildStale,
		PDFStatus:          opts.PDFStatus,
		KittyAvailable:     opts.KittyAvailable,
		escCache:           newPDFEscCache(opts.KittyXferDir),
		pageLayout:         newPageLayoutCache(),
		SourceLineCursor:   1,
		Layout:             LayoutNoPDF,
		Focus:              PaneOutline,
		OutlineFrac:        defaultOutlineFrac,
		PDFFrac:            defaultPDFFrac,
		StackedTopFrac:     defaultStackedTopFrac,
		SourceSplitFrac:    defaultSourceSplitFrac,
	}
	if side.CursorPairID != "" {
		if idx := pairIndexByID(review, side.CursorPairID); idx >= 0 {
			m.Cursor = idx
		}
	}
	m.snapCursor()
	return m
}

// EditSnapshot captures a complete pre-edit copy of the new endpoint so
// diff-mode undo/redo can restore only that file.
type EditSnapshot struct {
	Path     string
	Bytes    []byte
	Label    string
	Sequence int
}

// LineEditPopup hosts the inline editor for one physical source line. It uses
// a textarea, not a textinput, so very long TeX lines soft-wrap while editing;
// submission still writes back one physical line.
type LineEditPopup struct {
	TA           textarea.Model
	AbsoluteLine int
	Original     string
	Indent       string
}

const maxEditUndo = 100

func (m *Model) pushEditSnapshot(label string) error {
	if m.Review == nil || m.Review.New.Path == "" {
		return fmt.Errorf("no new source file")
	}
	data, err := os.ReadFile(m.Review.New.Path)
	if err != nil {
		return err
	}
	m.OpSeq++
	m.EditUndo = append(m.EditUndo, EditSnapshot{
		Path:     m.Review.New.Path,
		Bytes:    data,
		Label:    label,
		Sequence: m.OpSeq,
	})
	if len(m.EditUndo) > maxEditUndo {
		m.EditUndo = m.EditUndo[len(m.EditUndo)-maxEditUndo:]
	}
	m.EditRedo = nil
	return nil
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	if m.OpenCompare {
		return m.compareEditorCmd()
	}
	return nil
}

// CurrentPair returns the selected semantic pair.
func (m Model) CurrentPair() *diffreview.Pair {
	if m.Review == nil || m.Cursor < 0 || m.Cursor >= len(m.Review.Pairs) {
		return nil
	}
	return &m.Review.Pairs[m.Cursor]
}

// FinalSidecar returns the sidecar with cursor/reviewed/annotation state synced
// from the current model.
func (m Model) FinalSidecar() *diffreview.Sidecar {
	side := m.Sidecar
	if side == nil {
		side = diffreview.NewSidecar(m.Review)
	}
	pair := m.CurrentPair()
	if pair != nil {
		side.CursorPairID = pair.ID
	}
	side.Reviewed = reviewedList(m.Reviewed)
	side.Pairs = diffreview.PairSummaries(m.Review)
	return side
}

type outlineTarget struct {
	PairIndex         int
	MemberPairIndices []int
	AnchorLine        int
}

func (m Model) visibleTargets() []outlineTarget {
	if m.Review == nil {
		return nil
	}
	rows := m.outlineRows()
	targets := make([]outlineTarget, 0, len(rows))
	for _, row := range rows {
		if row.PairIndex < 0 || (row.Group && !row.Collapsed) {
			continue
		}
		anchor := row.AnchorLine
		if anchor < 1 {
			anchor = 1
		}
		targets = append(targets, outlineTarget{PairIndex: row.PairIndex, MemberPairIndices: append([]int(nil), row.MemberPairIndices...), AnchorLine: anchor})
	}
	return targets
}

func (m Model) visibleIndices() []int {
	targets := m.visibleTargets()
	visible := make([]int, 0, len(targets))
	seen := make(map[int]bool, len(targets))
	for _, target := range targets {
		if seen[target.PairIndex] {
			continue
		}
		seen[target.PairIndex] = true
		visible = append(visible, target.PairIndex)
	}
	return visible
}

func (m Model) visibleTargetPosition(targets []outlineTarget) int {
	if len(targets) == 0 {
		return 0
	}
	curLine := m.SourceLineCursor
	if curLine < 1 {
		curLine = 1
	}
	fallback := -1
	best := -1
	bestAnchor := -1
	for i, target := range targets {
		if !outlineTargetContainsPair(target, m.Cursor) {
			continue
		}
		if fallback < 0 {
			fallback = i
		}
		anchor := target.AnchorLine
		if anchor < 1 {
			anchor = 1
		}
		if anchor <= curLine && anchor >= bestAnchor {
			best = i
			bestAnchor = anchor
		}
	}
	if best >= 0 {
		return best
	}
	if fallback >= 0 {
		return fallback
	}
	for i, target := range targets {
		if target.PairIndex >= m.Cursor {
			return i
		}
	}
	return len(targets) - 1
}

func outlineTargetContainsPair(target outlineTarget, pairIndex int) bool {
	if target.PairIndex == pairIndex {
		return true
	}
	for _, idx := range target.MemberPairIndices {
		if idx == pairIndex {
			return true
		}
	}
	return false
}

func (m *Model) snapCursor() {
	if m.Review == nil || len(m.Review.Pairs) == 0 {
		m.Cursor = 0
		return
	}
	targets := m.visibleTargets()
	if len(targets) == 0 {
		if m.Cursor < 0 {
			m.Cursor = 0
		}
		if m.Cursor >= len(m.Review.Pairs) {
			m.Cursor = len(m.Review.Pairs) - 1
		}
		return
	}
	target := targets[m.visibleTargetPosition(targets)]
	m.Cursor = target.PairIndex
	m.SourceLineCursor = target.AnchorLine
	m.snapSourceLine()
}

func (m Model) statusText() string {
	pair := m.CurrentPair()
	selected := "-"
	if pair != nil {
		selected = pair.ID
	}
	stats := reviewStats(m.Review)
	base := fmt.Sprintf(
		"filter:%s mode:%s pair:%s total:%d ~%d +%d -%d fmt%d %s%d",
		m.Filter.String(),
		m.DiffRegime.String(),
		selected,
		stats.Total,
		stats.Changed,
		stats.Added,
		stats.Deleted,
		stats.FormatOnly,
		StatusMarker(diffreview.Moved),
		stats.Moved,
	)
	if m.Status == "" {
		return base
	}
	return base + " | " + m.Status
}

func pairMatchesFilter(
	pair diffreview.Pair,
	filter Filter,
	reviewed map[string]bool,
	annotations map[string]string,
	issues map[string][]string,
) bool {
	switch filter {
	case FilterAll:
		return true
	case FilterChanged:
		return changedStatus(pair.Status)
	case FilterUnreviewed:
		return changedStatus(pair.Status) && !reviewed[pair.ID]
	case FilterAnnotated:
		return annotations[pair.ID] != ""
	case FilterIssues:
		return len(issues[pair.ID]) > 0
	default:
		return changedStatus(pair.Status)
	}
}

func changedStatus(status diffreview.PairStatus) bool {
	switch status {
	case diffreview.Changed, diffreview.Added, diffreview.Deleted, diffreview.Moved, diffreview.FormatOnly:
		return true
	default:
		return false
	}
}

func containsIndex(values []int, needle int) bool {
	for _, v := range values {
		if v == needle {
			return true
		}
	}
	return false
}

func copyIssueMap(in map[string][]string) map[string][]string {
	if len(in) == 0 {
		return map[string][]string{}
	}
	out := make(map[string][]string, len(in))
	for k, v := range in {
		out[k] = append([]string(nil), v...)
	}
	return out
}

func reviewedList(reviewed map[string]bool) []string {
	out := make([]string, 0, len(reviewed))
	for id, ok := range reviewed {
		if ok && id != "" {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func pairIndexByID(review *diffreview.Review, pairID string) int {
	if review == nil || pairID == "" {
		return -1
	}
	for i := range review.Pairs {
		if review.Pairs[i].ID == pairID {
			return i
		}
	}
	return -1
}
