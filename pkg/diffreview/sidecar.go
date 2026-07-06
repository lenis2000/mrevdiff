package diffreview

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"mrevdiff/pkg/persist"
)

// Annotation is one diff-mode note attached to a semantic old/new pair.
type Annotation struct {
	PairID      string `json:"pair_id" yaml:"pair_id"`
	Status      string `json:"status" yaml:"status,omitempty"`
	Side        string `json:"side" yaml:"side,omitempty"`
	File        string `json:"file" yaml:"file,omitempty"`
	StartLine   int    `json:"start_line" yaml:"start_line,omitempty"`
	EndLine     int    `json:"end_line" yaml:"end_line,omitempty"`
	SourceQuote string `json:"source_quote" yaml:"source_quote,omitempty"`
	Note        string `json:"note" yaml:"note,omitempty"`
}

// PairSummary records the stable ID and status for one semantic pair.
type PairSummary struct {
	ID     string `json:"id" yaml:"id"`
	Status string `json:"status" yaml:"status"`
}

// Sidecar is the persisted state for one semantic diff-review session.
type Sidecar struct {
	OldSpec      string        `json:"old_spec" yaml:"old_spec,omitempty"`
	OldLabel     string        `json:"old_label" yaml:"old_label,omitempty"`
	OldSHA       string        `json:"old_sha,omitempty" yaml:"old_sha,omitempty"`
	NewSpec      string        `json:"new_spec" yaml:"new_spec,omitempty"`
	NewPath      string        `json:"new_path" yaml:"new_path,omitempty"`
	CursorPairID string        `json:"cursor_pair_id" yaml:"cursor_pair_id,omitempty"`
	Reviewed     []string      `json:"reviewed" yaml:"reviewed,omitempty"`
	Pairs        []PairSummary `json:"pairs" yaml:"pairs,omitempty"`
	Annotations  []Annotation  `json:"annotations" yaml:"annotations,omitempty"`
	Detached     []Annotation  `json:"detached" yaml:"detached,omitempty"`
}

// StdoutFormat selects the quit-time diff summary emission shape.
type StdoutFormat int

const (
	StdoutNone StdoutFormat = iota
	StdoutMarkdown
	StdoutJSON
)

// LoadSidecar reads a diff sidecar from disk. Missing files return an empty
// sidecar so first-time reviews can proceed without a separate existence check.
func LoadSidecar(path string) (*Sidecar, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Sidecar{}, nil
		}
		return nil, err
	}
	return ParseSidecar(data)
}

// ParseSidecar decodes the YAML frontmatter from a markdown diff sidecar.
func ParseSidecar(data []byte) (*Sidecar, error) {
	text := strings.TrimLeft(string(data), "\ufeff")
	if strings.TrimSpace(text) == "" {
		return &Sidecar{}, nil
	}
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return &Sidecar{}, nil
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return nil, fmt.Errorf("diffreview: unterminated sidecar frontmatter")
	}
	var side Sidecar
	if err := yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &side); err != nil {
		return nil, fmt.Errorf("diffreview: sidecar yaml: %w", err)
	}
	return &side, nil
}

// SaveSidecar writes a diff sidecar atomically.
func SaveSidecar(path string, side *Sidecar) error {
	out, err := MarshalSidecar(side)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return persist.WriteFileAtomic(path, out)
}

// SaveSidecarMerging writes mem to path, merging in concurrent on-disk edits
// when the sidecar changed since loadedModTime was observed.
func SaveSidecarMerging(path string, base *Sidecar, loadedModTime time.Time, mem *Sidecar) error {
	if !sidecarChangedOnDisk(path, loadedModTime) {
		return SaveSidecar(path, mem)
	}
	disk, err := LoadSidecar(path)
	if err != nil {
		return err
	}
	return SaveSidecar(path, MergeSidecars(base, disk, mem))
}

func sidecarChangedOnDisk(path string, loadedModTime time.Time) bool {
	info, err := os.Stat(path)
	if err != nil {
		return !loadedModTime.IsZero()
	}
	if loadedModTime.IsZero() {
		return true
	}
	return !info.ModTime().Equal(loadedModTime)
}

// MergeSidecars applies the in-memory changes since base on top of disk.
// Annotations are keyed by pair ID; reviewed state is merged as set arithmetic:
// (disk ∪ user-added) - user-removed.
func MergeSidecars(base, disk, mem *Sidecar) *Sidecar {
	if base == nil {
		base = &Sidecar{}
	}
	if disk == nil {
		disk = &Sidecar{}
	}
	if mem == nil {
		mem = &Sidecar{}
	}
	out := cloneSidecar(mem)
	out.Reviewed = mergeReviewedIDs(base.Reviewed, disk.Reviewed, mem.Reviewed)
	out.Annotations = mergeDiffAnnotations(base.Annotations, disk.Annotations, mem.Annotations)
	out.Detached = mergeDiffAnnotations(base.Detached, disk.Detached, mem.Detached)
	return out
}

// MarshalSidecar serializes a sidecar as markdown with YAML frontmatter.
func MarshalSidecar(side *Sidecar) ([]byte, error) {
	if side == nil {
		side = &Sidecar{}
	}
	copySide := *side
	copySide.Reviewed = append([]string(nil), side.Reviewed...)
	copySide.Pairs = append([]PairSummary(nil), side.Pairs...)
	copySide.Annotations = append([]Annotation(nil), side.Annotations...)
	copySide.Detached = append([]Annotation(nil), side.Detached...)
	sort.Strings(copySide.Reviewed)
	yml, err := yaml.Marshal(&copySide)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(yml)
	b.WriteString("---\n\n")
	writeSidecarMarkdown(&b, &copySide, nil, false)
	return []byte(b.String()), nil
}

// NewSidecar returns the current metadata shell for review.
func NewSidecar(review *Review) *Sidecar {
	if review == nil {
		return &Sidecar{}
	}
	return &Sidecar{
		OldSpec:  review.Old.Spec,
		OldLabel: review.Old.Label,
		OldSHA:   review.Old.ResolvedSHA,
		NewSpec:  review.New.Spec,
		NewPath:  endpointDisplayPath(review.New),
		Pairs:    PairSummaries(review),
	}
}

// RemapSidecar carries a previously loaded sidecar onto the current review.
// Annotations and detached notes whose pair IDs no longer exist remain
// detached instead of being dropped.
func RemapSidecar(loaded *Sidecar, review *Review) *Sidecar {
	if loaded == nil {
		loaded = &Sidecar{}
	}
	out := NewSidecar(review)
	if review == nil {
		out.Reviewed = append([]string(nil), loaded.Reviewed...)
		out.Pairs = append([]PairSummary(nil), loaded.Pairs...)
		out.Annotations = append([]Annotation(nil), loaded.Annotations...)
		out.Detached = append([]Annotation(nil), loaded.Detached...)
		out.CursorPairID = loaded.CursorPairID
		return out
	}

	if _, ok := review.ByID[loaded.CursorPairID]; ok {
		out.CursorPairID = loaded.CursorPairID
	}

	seenReviewed := map[string]bool{}
	for _, id := range loaded.Reviewed {
		if id == "" || seenReviewed[id] {
			continue
		}
		if _, ok := review.ByID[id]; !ok {
			continue
		}
		seenReviewed[id] = true
		out.Reviewed = append(out.Reviewed, id)
	}

	tryAttach := func(a Annotation) {
		pair := review.ByID[a.PairID]
		if pair == nil {
			out.Detached = append(out.Detached, a)
			return
		}
		note := strings.TrimSpace(a.Note)
		if note == "" {
			return
		}
		out.UpsertAnnotation(AnnotationForPair(review, pair, note))
	}
	for _, a := range loaded.Annotations {
		tryAttach(a)
	}
	for _, a := range loaded.Detached {
		tryAttach(a)
	}
	return out
}

// DefaultSidecarPath returns the default sidecar path for a diff review.
func DefaultSidecarPath(review *Review) string {
	if review == nil {
		return "mrevdiff.md"
	}
	newPath := defaultSidecarNewPath(review.New)
	if newPath == "" {
		newPath = "mrevdiff"
	}
	base := review.Old.Spec
	if rev, _, ok := splitGitEndpoint(review.Old.Spec); ok {
		base = rev
	} else if base != "" {
		base = filepath.Base(base)
	}
	base = safePathComponent(base)
	if base == "" {
		base = "old"
	}
	return newPath + ".mrevdiff." + base + ".md"
}

func defaultSidecarNewPath(endpoint Endpoint) string {
	if endpoint.Kind == WorkingFile && !endpoint.Materialized && endpoint.Path != "" {
		return endpoint.Path
	}
	if endpoint.Spec != "" {
		return safePathComponent(endpoint.Spec)
	}
	if endpoint.RelPath != "" {
		return safePathComponent(filepath.Base(endpoint.RelPath))
	}
	return ""
}

// AnnotationForPair builds a block-level annotation record for pair.
func AnnotationForPair(review *Review, pair *Pair, note string) Annotation {
	if pair == nil {
		return Annotation{Note: strings.TrimSpace(note)}
	}
	side := "new"
	block := pair.New
	endpoint := Endpoint{}
	if review != nil {
		endpoint = review.New
	}
	if block == nil {
		side = "old"
		block = pair.Old
		if review != nil {
			endpoint = review.Old
		}
	}
	a := Annotation{
		PairID: pair.ID,
		Status: pair.Status.String(),
		Side:   side,
		Note:   strings.TrimSpace(note),
	}
	if block != nil {
		a.File = block.File
		if a.File == "" {
			a.File = endpointDisplayPath(endpoint)
		}
		a.StartLine = block.StartLine
		a.EndLine = block.EndLine
		a.SourceQuote = block.Source
	}
	return a
}

// ReviewedSet returns the sidecar reviewed IDs as a map.
func (s *Sidecar) ReviewedSet() map[string]bool {
	out := map[string]bool{}
	if s == nil {
		return out
	}
	for _, id := range s.Reviewed {
		if id != "" {
			out[id] = true
		}
	}
	return out
}

// AnnotationNotes returns attached annotations keyed by pair ID.
func (s *Sidecar) AnnotationNotes() map[string]string {
	out := map[string]string{}
	if s == nil {
		return out
	}
	for _, a := range s.Annotations {
		if a.PairID != "" && a.Note != "" {
			out[a.PairID] = a.Note
		}
	}
	return out
}

// SetReviewed updates one pair's reviewed state.
func (s *Sidecar) SetReviewed(pairID string, reviewed bool) {
	if s == nil || pairID == "" {
		return
	}
	out := s.Reviewed[:0]
	for _, id := range s.Reviewed {
		if id == pairID {
			continue
		}
		out = append(out, id)
	}
	if reviewed {
		out = append(out, pairID)
	}
	s.Reviewed = out
}

// UpsertAnnotation inserts or replaces one attached annotation.
func (s *Sidecar) UpsertAnnotation(a Annotation) {
	if s == nil || a.PairID == "" {
		return
	}
	for i := range s.Annotations {
		if s.Annotations[i].PairID == a.PairID {
			s.Annotations[i] = a
			return
		}
	}
	s.Annotations = append(s.Annotations, a)
}

// DeleteAnnotation removes an attached annotation by pair ID.
func (s *Sidecar) DeleteAnnotation(pairID string) bool {
	if s == nil || pairID == "" {
		return false
	}
	for i := range s.Annotations {
		if s.Annotations[i].PairID == pairID {
			s.Annotations = append(s.Annotations[:i], s.Annotations[i+1:]...)
			return true
		}
	}
	return false
}

func cloneSidecar(s *Sidecar) *Sidecar {
	if s == nil {
		return &Sidecar{}
	}
	out := *s
	out.Reviewed = append([]string(nil), s.Reviewed...)
	out.Pairs = append([]PairSummary(nil), s.Pairs...)
	out.Annotations = append([]Annotation(nil), s.Annotations...)
	out.Detached = append([]Annotation(nil), s.Detached...)
	return &out
}

// CloneSidecar returns a detached copy of the sidecar suitable for use as a
// later merge base.
func CloneSidecar(s *Sidecar) *Sidecar {
	return cloneSidecar(s)
}

func mergeReviewedIDs(base, disk, mem []string) []string {
	baseSet := idSet(base)
	memSet := idSet(mem)
	out := idSet(disk)
	for id := range memSet {
		if _, ok := baseSet[id]; !ok {
			out[id] = struct{}{}
		}
	}
	for id := range baseSet {
		if _, ok := memSet[id]; !ok {
			delete(out, id)
		}
	}
	ids := make([]string, 0, len(out))
	for id := range out {
		if id != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func mergeDiffAnnotations(base, disk, mem []Annotation) []Annotation {
	baseByID := indexDiffAnnotations(base)
	memByID := indexDiffAnnotations(mem)
	out := append([]Annotation(nil), disk...)
	pos := indexDiffAnnotationPositions(out)
	upsert := func(a Annotation) {
		if a.PairID == "" {
			return
		}
		if i, ok := pos[a.PairID]; ok {
			out[i] = a
			return
		}
		pos[a.PairID] = len(out)
		out = append(out, a)
	}
	remove := func(pairID string) {
		i, ok := pos[pairID]
		if !ok {
			return
		}
		out = append(out[:i], out[i+1:]...)
		pos = indexDiffAnnotationPositions(out)
	}
	for _, a := range mem {
		baseA, hadBase := baseByID[a.PairID]
		if !hadBase || !sameAnnotation(a, baseA) {
			upsert(a)
		}
	}
	for pairID := range baseByID {
		if _, stillInMem := memByID[pairID]; !stillInMem {
			remove(pairID)
		}
	}
	return out
}

func idSet(ids []string) map[string]struct{} {
	out := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id != "" {
			out[id] = struct{}{}
		}
	}
	return out
}

func indexDiffAnnotations(xs []Annotation) map[string]Annotation {
	out := make(map[string]Annotation, len(xs))
	for _, a := range xs {
		if a.PairID != "" {
			out[a.PairID] = a
		}
	}
	return out
}

func indexDiffAnnotationPositions(xs []Annotation) map[string]int {
	out := make(map[string]int, len(xs))
	for i, a := range xs {
		if a.PairID != "" {
			out[a.PairID] = i
		}
	}
	return out
}

func sameAnnotation(a, b Annotation) bool {
	return a.PairID == b.PairID &&
		a.Status == b.Status &&
		a.Side == b.Side &&
		a.File == b.File &&
		a.StartLine == b.StartLine &&
		a.EndLine == b.EndLine &&
		a.SourceQuote == b.SourceQuote &&
		a.Note == b.Note
}

// ParseStdoutFormat maps the CLI stdout flag to a diff stdout format.
func ParseStdoutFormat(s string) (StdoutFormat, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "md", "markdown":
		return StdoutMarkdown, nil
	case "json":
		return StdoutJSON, nil
	case "none", "off":
		return StdoutNone, nil
	default:
		return StdoutNone, fmt.Errorf("diffreview: unknown stdout format %q", s)
	}
}

// Emit writes the quit-time diff review summary in the selected format.
func Emit(w io.Writer, side *Sidecar, review *Review, format StdoutFormat) error {
	switch format {
	case StdoutNone:
		return nil
	case StdoutMarkdown:
		return EmitMarkdown(w, side, review)
	case StdoutJSON:
		return EmitJSON(w, side, review)
	default:
		return nil
	}
}

// EmitMarkdown writes a concise markdown summary and annotations.
func EmitMarkdown(w io.Writer, side *Sidecar, review *Review) error {
	if side == nil {
		side = NewSidecar(review)
	}
	var b strings.Builder
	writeSidecarMarkdown(&b, side, review, true)
	_, err := io.WriteString(w, b.String())
	return err
}

// EmitJSON writes a machine-readable diff review summary.
func EmitJSON(w io.Writer, side *Sidecar, review *Review) error {
	if side == nil {
		side = NewSidecar(review)
	}
	payload := struct {
		OldSpec      string        `json:"old_spec"`
		OldLabel     string        `json:"old_label,omitempty"`
		OldSHA       string        `json:"old_sha,omitempty"`
		NewSpec      string        `json:"new_spec"`
		NewPath      string        `json:"new_path,omitempty"`
		CursorPairID string        `json:"cursor_pair_id,omitempty"`
		Reviewed     []string      `json:"reviewed,omitempty"`
		Pairs        []PairSummary `json:"pairs,omitempty"`
		Annotations  []Annotation  `json:"annotations,omitempty"`
		Detached     []Annotation  `json:"detached,omitempty"`
	}{
		OldSpec:      side.OldSpec,
		OldLabel:     side.OldLabel,
		OldSHA:       side.OldSHA,
		NewSpec:      side.NewSpec,
		NewPath:      side.NewPath,
		CursorPairID: side.CursorPairID,
		Reviewed:     append([]string(nil), side.Reviewed...),
		Pairs:        pairSummariesForEmit(side, review),
		Annotations:  append([]Annotation(nil), side.Annotations...),
		Detached:     append([]Annotation(nil), side.Detached...),
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}

func writeSidecarMarkdown(b *strings.Builder, side *Sidecar, review *Review, includeAll bool) {
	b.WriteString("# mrevdiff review\n\n")
	fmt.Fprintf(b, "Old: %s", side.OldSpec)
	if side.OldLabel != "" && side.OldLabel != side.OldSpec {
		fmt.Fprintf(b, " (%s)", side.OldLabel)
	}
	if side.OldSHA != "" {
		// Pin the symbolic rev: HEAD moves in an agent loop, so a history
		// file saying just "HEAD" is ambiguous within the hour.
		sha := side.OldSHA
		if len(sha) > 12 {
			sha = sha[:12]
		}
		fmt.Fprintf(b, " @ %s", sha)
	}
	b.WriteString("\n")
	fmt.Fprintf(b, "New: %s", side.NewSpec)
	if side.NewPath != "" && side.NewPath != side.NewSpec {
		fmt.Fprintf(b, " (%s)", side.NewPath)
	}
	b.WriteString("\n\n")

	if includeAll || review != nil {
		b.WriteString("## Pair statuses\n\n")
		pairs := pairSummariesForEmit(side, review)
		if len(pairs) == 0 {
			b.WriteString("(none)\n\n")
		} else {
			for _, pair := range pairs {
				fmt.Fprintf(b, "- %s %s\n", pair.Status, pair.ID)
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("## Annotations\n\n")
	if len(side.Annotations) == 0 {
		b.WriteString("(none)\n")
	} else {
		for _, a := range side.Annotations {
			writeAnnotationMarkdown(b, a)
		}
	}
	if len(side.Detached) > 0 {
		b.WriteString("\n## Detached\n\n")
		for _, a := range side.Detached {
			writeAnnotationMarkdown(b, a)
		}
	}
}

func writeAnnotationMarkdown(b *strings.Builder, a Annotation) {
	fmt.Fprintf(b, "### %s", a.PairID)
	if a.Status != "" {
		fmt.Fprintf(b, " [%s]", a.Status)
	}
	b.WriteString("\n\n")
	if a.Side != "" {
		fmt.Fprintf(b, "Side: %s\n", a.Side)
	}
	if a.File != "" {
		fmt.Fprintf(b, "Source: %s:L%d-L%d\n", a.File, a.StartLine, a.EndLine)
	}
	if a.Side != "" || a.File != "" {
		b.WriteString("\n")
	}
	writeQuote(b, a.SourceQuote)
	note := strings.TrimSpace(a.Note)
	if note != "" {
		b.WriteString(note)
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

func writeQuote(b *strings.Builder, quote string) {
	quote = strings.TrimRight(quote, "\n")
	if quote == "" {
		return
	}
	for _, line := range truncateQuoteLines(quote, 6) {
		if line == "" {
			b.WriteString(">\n")
		} else {
			b.WriteString("> ")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
}

func truncateQuoteLines(src string, maxLines int) []string {
	if maxLines <= 0 {
		return nil
	}
	lines := strings.Split(src, "\n")
	if len(lines) <= maxLines {
		return lines
	}
	head := maxLines - 1 - (maxLines-1)/2
	tail := maxLines - 1 - head
	out := make([]string, 0, maxLines)
	out = append(out, lines[:head]...)
	out = append(out, "...")
	out = append(out, lines[len(lines)-tail:]...)
	return out
}

func pairSummariesForEmit(side *Sidecar, review *Review) []PairSummary {
	pairs := PairSummaries(review)
	if len(pairs) != 0 || review != nil || side == nil {
		return pairs
	}
	return append([]PairSummary(nil), side.Pairs...)
}

// PairSummaries returns the persisted pair summary list for a review.
func PairSummaries(review *Review) []PairSummary {
	if review == nil {
		return nil
	}
	out := make([]PairSummary, 0, len(review.Pairs))
	for _, pair := range review.Pairs {
		out = append(out, PairSummary{ID: pair.ID, Status: pair.Status.String()})
	}
	return out
}

func endpointDisplayPath(endpoint Endpoint) string {
	switch {
	case endpoint.Path != "":
		return endpoint.Path
	case endpoint.RelPath != "":
		return endpoint.RelPath
	default:
		return endpoint.Spec
	}
}
