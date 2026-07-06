package parser

import (
	"sort"
	"strings"
)

// ChunkBudgetMaxLines is the line span above which a KindParagraph is
// further split during applyChunkBudget. Matches the threshold used by
// the lint.block-too-long diagnostic so warnings and rewrites stay in sync.
var ChunkBudgetMaxLines = 40

// ChunkBudgetMergeThreshold is the maximum combined line span of a run of
// adjacent prose paragraphs that applyChunkBudget will fuse into a single
// block. Picked so that two- and three-line filler paragraphs that
// segmentLongParagraphs sliced off a longer prose flow get reabsorbed.
var ChunkBudgetMergeThreshold = 5

// applyChunkBudget post-processes the block tree to produce chunks of a
// roughly uniform, navigable size. It runs as the final segmentation pass
// — after segmentLongParagraphs has already done sentence-level slicing —
// and performs two operations in order: oversized paragraphs are split on
// hard then soft boundaries, and runs of tiny adjacent prose siblings are
// merged. Labels and outgoing references gate both operations so refable
// targets are never destroyed.
func (p *parser) applyChunkBudget() {
	hard := p.hardBoundaryLines()
	p.chunkSplitOversized(hard)
	p.chunkMergeTiny()
}

// hardBoundaryLines returns the set of source lines that mark "hard"
// structural breaks — lines where a section, theorem, proof, display,
// figure, or list \item begins. Used by chunk-budget split to prefer
// breaking on these positions before falling back to sentence ends.
func (p *parser) hardBoundaryLines() map[int]bool {
	out := map[int]bool{}
	for _, b := range p.doc.Blocks {
		if b == nil || b == p.doc.Root {
			continue
		}
		switch b.Kind {
		case KindSection, KindTheoremLike, KindProof, KindDisplay, KindFigure:
			if b.StartLine > 0 {
				out[b.StartLine] = true
			}
		}
	}
	for _, tk := range p.tokens {
		if tk.Kind == TokItem {
			out[tk.Line] = true
		}
	}
	return out
}

// chunkSplitOversized walks every leaf KindParagraph (no children of its
// own) and, when its line span exceeds ChunkBudgetMaxLines, splits it.
// Hard boundaries are tried first; if none lie strictly inside the block,
// the existing sentenceSpans walker is used as a soft fallback.
func (p *parser) chunkSplitOversized(hard map[int]bool) {
	blocks := append([]*Block(nil), p.doc.Blocks...)
	for _, b := range blocks {
		if b == nil || b == p.doc.Root || len(b.ChildIDs) > 0 {
			continue
		}
		if b.Kind != KindParagraph {
			continue
		}
		if b.EndLine-b.StartLine+1 <= ChunkBudgetMaxLines {
			continue
		}
		spans := p.chunkSplitSpans(b.StartLine, b.EndLine, hard)
		if len(spans) <= 1 {
			continue
		}
		labelLine := p.labelDeclarationLine(b)
		var newIDs []string
		for _, sp := range spans {
			child := &Block{
				ID:        p.newID(),
				Kind:      KindParagraph,
				StartLine: sp[0],
				EndLine:   sp[1],
				ParentID:  b.ID,
			}
			child.Source = p.extractSource(sp[0], sp[1])
			if labelLine > 0 && labelLine >= sp[0] && labelLine <= sp[1] {
				child.Label = b.Label
			}
			p.doc.Blocks = append(p.doc.Blocks, child)
			p.doc.ByID[child.ID] = child
			newIDs = append(newIDs, child.ID)
		}
		b.ChildIDs = append(b.ChildIDs, newIDs...)
	}
}

// chunkSplitSpans returns line ranges to split [startLine, endLine] into.
// Tries hard-boundary lines that fall strictly inside the range first;
// falls back to sentence-split if none are usable.
func (p *parser) chunkSplitSpans(startLine, endLine int, hard map[int]bool) [][2]int {
	var cuts []int
	for ln := startLine + 1; ln <= endLine; ln++ {
		if hard[ln] {
			cuts = append(cuts, ln)
		}
	}
	if len(cuts) > 0 {
		var spans [][2]int
		prev := startLine
		for _, c := range cuts {
			spans = append(spans, [2]int{prev, c - 1})
			prev = c
		}
		spans = append(spans, [2]int{prev, endLine})
		return spans
	}
	return p.sentenceSpans(startLine, endLine)
}

// labelDeclarationLine returns the source line on which b's \label{...}
// command sits, or 0 if the block carries no label or the label can't be
// located in the source range. Used by chunkSplitOversized to keep the
// label attached to the split that physically contains the declaration.
func (p *parser) labelDeclarationLine(b *Block) int {
	if b == nil || b.Label == "" {
		return 0
	}
	needle := `\label{` + b.Label + `}`
	for ln := b.StartLine; ln <= b.EndLine; ln++ {
		src := string(p.lineRaw(ln))
		if strings.Contains(src, needle) {
			return ln
		}
	}
	return 0
}

// chunkMergeTiny walks every container's ChildIDs and fuses runs of
// adjacent prose-only KindParagraph siblings whose combined line span
// stays at or below ChunkBudgetMergeThreshold. Refuses to merge across:
//   - any non-paragraph sibling (theorem, proof, display, figure, section, list);
//   - a blank-or-comment-only gap of more than 1 line between siblings;
//   - any block carrying a label or outgoing reference (refable targets).
//
// Containers that are themselves KindParagraph are skipped: their
// children came from segmentLongParagraphs (sentence-level splits of an
// oversized prose paragraph), and re-fusing those defeats the explicit
// finer-grained split the outline is meant to surface.
func (p *parser) chunkMergeTiny() {
	containers := []*Block{p.doc.Root}
	for _, b := range p.doc.Blocks {
		if b == nil || b == p.doc.Root {
			continue
		}
		if b.Kind == KindParagraph {
			continue
		}
		if len(b.ChildIDs) > 1 {
			containers = append(containers, b)
		}
	}

	for _, c := range containers {
		p.mergeTinyChildren(c)
	}
}

// mergeTinyChildren is the per-container body of chunkMergeTiny.
func (p *parser) mergeTinyChildren(c *Block) {
	if len(c.ChildIDs) < 2 {
		return
	}
	// Sort by StartLine so adjacency reflects source order.
	sort.SliceStable(c.ChildIDs, func(i, j int) bool {
		ai := p.doc.ByID[c.ChildIDs[i]]
		aj := p.doc.ByID[c.ChildIDs[j]]
		return ai.StartLine < aj.StartLine
	})

	out := make([]string, 0, len(c.ChildIDs))
	dropped := map[string]bool{}
	i := 0
	for i < len(c.ChildIDs) {
		head := p.doc.ByID[c.ChildIDs[i]]
		if !p.isMergeableProse(head) {
			out = append(out, head.ID)
			i++
			continue
		}
		// Greedily extend the run while the combined size fits the budget
		// and adjacency / mergeability hold.
		j := i + 1
		runEnd := head.EndLine
		for j < len(c.ChildIDs) {
			next := p.doc.ByID[c.ChildIDs[j]]
			if !p.isMergeableProse(next) {
				break
			}
			if blanks := p.blankLinesBetween(runEnd, next.StartLine); blanks > 1 {
				break
			}
			combined := next.EndLine - head.StartLine + 1
			if combined > ChunkBudgetMergeThreshold {
				break
			}
			runEnd = next.EndLine
			j++
		}
		if j-i <= 1 {
			out = append(out, head.ID)
			i++
			continue
		}
		// Fuse [i, j) into head, drop the rest.
		head.EndLine = runEnd
		head.Source = p.extractSource(head.StartLine, head.EndLine)
		for k := i + 1; k < j; k++ {
			dropped[c.ChildIDs[k]] = true
		}
		out = append(out, head.ID)
		i = j
	}
	c.ChildIDs = out
	if len(dropped) > 0 {
		// Remove dropped blocks from doc.Blocks and ByID so downstream
		// passes (assignStableIDs, resolveRefs) don't see stale entries.
		filtered := p.doc.Blocks[:0]
		for _, b := range p.doc.Blocks {
			if dropped[b.ID] {
				delete(p.doc.ByID, b.ID)
				continue
			}
			filtered = append(filtered, b)
		}
		p.doc.Blocks = filtered
	}
}

// isMergeableProse returns true when b is a prose paragraph that has no
// children, no label, no outgoing references, holds no \label{...}
// declaration in its source, and isn't a list-env wrapper. Those checks
// together guarantee that fusing b into a sibling destroys no addressable
// structure. The source-level \label scan catches labels that originally
// attached to the enclosing section (because they were declared before the
// container-gap pass split out the paragraph) and would still be a
// reference target after stable-ID assignment.
func (p *parser) isMergeableProse(b *Block) bool {
	if b == nil || b == p.doc.Root {
		return false
	}
	if b.Kind != KindParagraph {
		return false
	}
	if len(b.ChildIDs) > 0 {
		return false
	}
	if b.Label != "" {
		return false
	}
	if len(b.RefsOut) > 0 {
		return false
	}
	if b.EnvName != "" && listEnvs[b.EnvName] {
		return false
	}
	if strings.Contains(b.Source, `\label{`) {
		return false
	}
	return true
}

// blankLinesBetween counts how many of the source lines strictly between
// prevEnd and nextStart are blank or comment-only. Used by chunkMergeTiny
// to detect deliberate paragraph breaks the author left in the source.
func (p *parser) blankLinesBetween(prevEnd, nextStart int) int {
	if nextStart-prevEnd <= 1 {
		return 0
	}
	n := 0
	for ln := prevEnd + 1; ln < nextStart; ln++ {
		if p.lineIsBlank(ln) {
			n++
		}
	}
	return n
}

// lineRaw returns the raw bytes of source line ln (1-based), without the
// trailing newline. Returns nil for out-of-range lines.
func (p *parser) lineRaw(ln int) []byte {
	if ln < 1 || ln > p.totalLines {
		return nil
	}
	from := p.lineStarts[ln-1]
	var to int
	if ln >= p.totalLines {
		to = len(p.src)
	} else {
		to = p.lineStarts[ln] - 1
	}
	if to < from {
		to = from
	}
	return p.src[from:to]
}
