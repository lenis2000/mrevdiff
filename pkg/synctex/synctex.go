// Package synctex parses SyncTeX v1 output produced by pdflatex/latexmk and
// exposes a line -> (page, bbox) index that callers can use to drive a PDF
// pane that follows the cursor inside a TUI.
//
// The file format is a gzipped text stream. The parser recognises only the
// records needed by mrevdiff: Input declarations in the preamble, page
// delimiters, and the positioned records that carry a (tag, line) reference
// (hbox/vbox begin markers `[` and `(`, glue `g`, kern `k`, text `t`, math
// `$`, and the explicit x/h/v point records).
package synctex

import (
	"bufio"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	spPerPt = 65536.0      // scaled points per TeX point
	ptToBP  = 72.0 / 72.27 // TeX point -> PDF big point
)

// Region is a bounding box on a rendered PDF page, expressed in PDF big points
// (1/72 inch). Y is measured from the top of the page (screen orientation),
// matching how downstream renderers crop the page.
type Region struct {
	Page       int
	X, Y, W, H float64
}

// Index is the parsed SyncTeX file: input-file table plus a per-file,
// per-line list of positioned records.
type Index struct {
	// Files maps SyncTeX input tags to the source file path as recorded in
	// the `Input:` records (paths are filepath.Clean'd on ingest).
	Files map[int]string
	// Lines maps cleaned file path -> source line number -> all regions
	// produced by records on that line.
	Lines map[string]map[int][]Region
	// sortedLines holds each file's mapped line numbers in ascending
	// order, built once at parse time so RegionForLines can binary-search
	// a [start,end] range instead of sweeping the whole per-file map on
	// every cursor move. May be nil for hand-built indexes; lookups then
	// fall back to the full sweep.
	sortedLines map[string][]int

	unit, xOff, yOff int64
	mag              int64

	// parseErrors counts records and header values rejected during ingest.
	// Surfaces via ParseErrors so callers can warn the user when a
	// truncated or partially corrupted .synctex.gz produces a sparse
	// index instead of failing silently.
	parseErrors int
}

// ParseErrors returns the number of records and header values rejected
// during parsing. A non-zero count usually means the .synctex.gz was
// truncated (e.g. by a killed latexmk) and the index is incomplete.
func (idx *Index) ParseErrors() int { return idx.parseErrors }

// Open decompresses and parses a .synctex.gz file.
func Open(path string) (*Index, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer func() { _ = gz.Close() }()
	return Parse(gz)
}

// Parse reads an uncompressed SyncTeX v1 stream. Unrecognised records are
// ignored; a malformed file yields whatever could be parsed plus the bufio
// error (if any).
func Parse(r io.Reader) (*Index, error) {
	idx := &Index{
		Files: map[int]string{},
		Lines: map[string]map[int][]Region{},
		unit:  1,
		mag:   1000,
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)

	inContent := false
	page := 0
	for sc.Scan() {
		line := sc.Text()
		if !inContent {
			if line == "Content:" {
				inContent = true
				continue
			}
			idx.handleHeader(line)
			continue
		}
		// Input: records can appear after the content section (for .aux etc.).
		if idx.handleHeader(line) {
			continue
		}
		if len(line) == 0 {
			continue
		}
		switch line[0] {
		case '{':
			page, _ = strconv.Atoi(line[1:])
			continue
		case '}':
			page = 0
			continue
		case '!':
			continue
		}
		if strings.HasPrefix(line, "Postamble:") ||
			strings.HasPrefix(line, "Post scriptum:") ||
			strings.HasPrefix(line, "Count:") {
			continue
		}
		if page == 0 {
			continue
		}
		rec, ok := parseRecord(line)
		if !ok {
			idx.parseErrors++
			continue
		}
		file, ok := idx.Files[rec.tag]
		if !ok {
			continue
		}
		reg := idx.toRegion(page, rec)
		m := idx.Lines[file]
		if m == nil {
			m = map[int][]Region{}
			idx.Lines[file] = m
		}
		m[rec.line] = append(m[rec.line], reg)
	}
	idx.buildSortedLines()
	if err := sc.Err(); err != nil {
		return idx, err
	}
	return idx, nil
}

func (idx *Index) buildSortedLines() {
	idx.sortedLines = make(map[string][]int, len(idx.Lines))
	for file, m := range idx.Lines {
		keys := make([]int, 0, len(m))
		for ln := range m {
			keys = append(keys, ln)
		}
		sort.Ints(keys)
		idx.sortedLines[file] = keys
	}
}

// handleHeader consumes a key:value preamble line. Returns true if the line
// was recognised and consumed. Malformed numeric values increment the
// Index's parseErrors counter and leave the existing default in place
// rather than zeroing it (a `Magnification: garbage` would otherwise set
// mag=0, collapsing every region's coordinate to (0,0)).
func (idx *Index) handleHeader(line string) bool {
	if strings.HasPrefix(line, "Input:") {
		rest := line[len("Input:"):]
		colon := strings.IndexByte(rest, ':')
		if colon < 0 {
			idx.parseErrors++
			return true
		}
		tag, err := strconv.Atoi(rest[:colon])
		if err != nil {
			idx.parseErrors++
			return true
		}
		path := strings.TrimSpace(rest[colon+1:])
		if path == "" {
			idx.parseErrors++
			return true
		}
		idx.Files[tag] = filepath.Clean(path)
		return true
	}
	if v, ok := strings.CutPrefix(line, "Unit:"); ok {
		idx.parseInt64Header(strings.TrimSpace(v), &idx.unit)
		return true
	}
	if v, ok := strings.CutPrefix(line, "Magnification:"); ok {
		idx.parseInt64Header(strings.TrimSpace(v), &idx.mag)
		return true
	}
	if v, ok := strings.CutPrefix(line, "X Offset:"); ok {
		idx.parseInt64Header(strings.TrimSpace(v), &idx.xOff)
		return true
	}
	if v, ok := strings.CutPrefix(line, "Y Offset:"); ok {
		idx.parseInt64Header(strings.TrimSpace(v), &idx.yOff)
		return true
	}
	return false
}

// parseInt64Header writes the parsed value into dst on success; on
// failure dst keeps its previous value and parseErrors goes up by one.
func (idx *Index) parseInt64Header(s string, dst *int64) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		idx.parseErrors++
		return
	}
	*dst = n
}

// rawRec holds the numeric fields pulled out of a single content record,
// still in raw scaled-points units.
type rawRec struct {
	tag, line int
	h, v      int64
	w, hh, d  int64
	hasDim    bool
}

// parseRecord decodes one content-section record of the form
//
//	<marker><tag>,<line>[,<col>]:<h>,<v>[:<W>,<H>,<D>]
//
// Returns ok=false for payload-less markers such as `]`, `)`, and for any
// line we cannot parse.
func parseRecord(s string) (rawRec, bool) {
	if len(s) < 4 {
		return rawRec{}, false
	}
	body := s[1:]
	colon := strings.IndexByte(body, ':')
	if colon < 0 {
		return rawRec{}, false
	}
	tagLine := body[:colon]
	rest := body[colon+1:]

	commas := strings.Split(tagLine, ",")
	if len(commas) < 2 {
		return rawRec{}, false
	}
	tag, err := strconv.Atoi(commas[0])
	if err != nil {
		return rawRec{}, false
	}
	line, err := strconv.Atoi(commas[1])
	if err != nil {
		return rawRec{}, false
	}

	hv := rest
	dim := ""
	if c2 := strings.IndexByte(rest, ':'); c2 >= 0 {
		hv = rest[:c2]
		dim = rest[c2+1:]
	}
	hvParts := strings.Split(hv, ",")
	if len(hvParts) < 2 {
		return rawRec{}, false
	}
	h, err := strconv.ParseInt(hvParts[0], 10, 64)
	if err != nil {
		return rawRec{}, false
	}
	v, err := strconv.ParseInt(hvParts[1], 10, 64)
	if err != nil {
		return rawRec{}, false
	}

	rec := rawRec{tag: tag, line: line, h: h, v: v}
	if dim != "" {
		dimParts := strings.Split(dim, ",")
		if len(dimParts) >= 3 {
			w, e1 := strconv.ParseInt(dimParts[0], 10, 64)
			hh, e2 := strconv.ParseInt(dimParts[1], 10, 64)
			d, e3 := strconv.ParseInt(dimParts[2], 10, 64)
			if e1 == nil && e2 == nil && e3 == nil {
				rec.w = w
				rec.hh = hh
				rec.d = d
				rec.hasDim = true
			}
		}
	}
	return rec, true
}

// toRegion converts a raw record into a PDF-big-point Region.
// SyncTeX stores h at the left edge and v at the baseline; when W/H/D are
// present we turn that into a bbox with top = v-H, height = H+D.
func (idx *Index) toRegion(page int, r rawRec) Region {
	toBP := func(raw int64) float64 {
		return float64(raw) * float64(idx.unit) / spPerPt * ptToBP * float64(idx.mag) / 1000.0
	}
	x := toBP(r.h) + toBP(idx.xOff)
	y := toBP(r.v) + toBP(idx.yOff)
	if !r.hasDim {
		return Region{Page: page, X: x, Y: y}
	}
	w := toBP(r.w)
	hh := toBP(r.hh)
	d := toBP(r.d)
	top := y - hh
	bottom := y + d
	return Region{Page: page, X: x, Y: top, W: w, H: bottom - top}
}

// RegionForLines returns the union bounding box of every record whose source
// line falls in [start, end] on the first page that contains any such record.
// nil if the file is unknown or no lines matched.
func (idx *Index) RegionForLines(file string, start, end int) *Region {
	key, lines := idx.linesEntryFor(file)
	if lines == nil {
		return nil
	}
	byPage := map[int][]Region{}
	if keys, ok := idx.sortedLines[key]; ok {
		lo := sort.SearchInts(keys, start)
		for i := lo; i < len(keys) && keys[i] <= end; i++ {
			for _, r := range lines[keys[i]] {
				byPage[r.Page] = append(byPage[r.Page], r)
			}
		}
	} else {
		for ln, regs := range lines {
			if ln < start || ln > end {
				continue
			}
			for _, r := range regs {
				byPage[r.Page] = append(byPage[r.Page], r)
			}
		}
	}
	if len(byPage) == 0 {
		return nil
	}
	pages := make([]int, 0, len(byPage))
	for p := range byPage {
		pages = append(pages, p)
	}
	sort.Ints(pages)
	p := pages[0]
	regs := byPage[p]
	first := regs[0]
	minX, minY := first.X, first.Y
	maxX, maxY := first.X+first.W, first.Y+first.H
	for _, r := range regs[1:] {
		if r.X < minX {
			minX = r.X
		}
		if r.Y < minY {
			minY = r.Y
		}
		if r.X+r.W > maxX {
			maxX = r.X + r.W
		}
		if r.Y+r.H > maxY {
			maxY = r.Y + r.H
		}
	}
	return &Region{Page: p, X: minX, Y: minY, W: maxX - minX, H: maxY - minY}
}

// linesFor looks up the per-line map for a file, trying exact and then
// suffix matching so callers don't need to worry about /tmp vs /private/tmp
// symlink differences.
//
// On a basename-only collision (two distinct .tex files in the project
// share the same filename, e.g. chapters/intro.tex and appendix/intro.tex),
// we prefer the entry that shares the longest path-component suffix with
// the requested path. If two entries tie on suffix length, the lookup
// returns nil and the caller treats this as "no region" — better than
// silently picking the wrong file based on Go's randomised map iteration.
func (idx *Index) linesFor(file string) map[int][]Region {
	_, m := idx.linesEntryFor(file)
	return m
}

// linesEntryFor is linesFor plus the resolved map key, so callers can
// reach the parallel sortedLines slice for the same file.
func (idx *Index) linesEntryFor(file string) (string, map[int][]Region) {
	clean := filepath.Clean(file)
	if m, ok := idx.Lines[clean]; ok {
		return clean, m
	}
	bestKey, bestN, tied := "", 0, false
	for k := range idx.Lines {
		n := commonSuffixComponents(clean, k)
		if n == 0 {
			continue
		}
		switch {
		case n > bestN:
			bestKey, bestN, tied = k, n, false
		case n == bestN:
			tied = true
		}
	}
	if bestN == 0 || tied {
		return "", nil
	}
	return bestKey, idx.Lines[bestKey]
}

// File returns the path recorded for a SyncTeX input tag, if any.
func (idx *Index) File(tag int) (string, bool) {
	s, ok := idx.Files[tag]
	return s, ok
}

// TagFor returns the SyncTeX input tag for a file path if known.
//
// As with linesFor, when no exact match is recorded we fall back to
// the input whose recorded path shares the longest suffix-component
// match with the requested path. Ties yield (0, false) — see linesFor
// for the rationale.
func (idx *Index) TagFor(path string) (int, bool) {
	clean := filepath.Clean(path)
	for t, p := range idx.Files {
		if p == clean {
			return t, true
		}
	}
	bestTag, bestN, tied := 0, 0, false
	for t, p := range idx.Files {
		n := commonSuffixComponents(clean, p)
		if n == 0 {
			continue
		}
		switch {
		case n > bestN:
			bestTag, bestN, tied = t, n, false
		case n == bestN:
			tied = true
		}
	}
	if bestN == 0 || tied {
		return 0, false
	}
	return bestTag, true
}

// commonSuffixComponents counts how many trailing path components are
// equal between a and b. Both arguments should already be filepath.Clean'd.
// Empty inputs return 0.
func commonSuffixComponents(a, b string) int {
	aParts := splitPathComponents(a)
	bParts := splitPathComponents(b)
	n := 0
	for n < len(aParts) && n < len(bParts) && aParts[len(aParts)-1-n] == bParts[len(bParts)-1-n] {
		n++
	}
	return n
}

// splitPathComponents returns the non-empty components of p, splitting
// on either path separator so the caller doesn't have to normalise to
// the platform-native form first.
func splitPathComponents(p string) []string {
	if p == "" {
		return nil
	}
	parts := strings.FieldsFunc(p, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	return parts
}

// Pages returns the sorted list of pages that contain any positioned record.
func (idx *Index) Pages() []int {
	set := map[int]struct{}{}
	for _, m := range idx.Lines {
		for _, regs := range m {
			for _, r := range regs {
				set[r.Page] = struct{}{}
			}
		}
	}
	out := make([]int, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Ints(out)
	return out
}
