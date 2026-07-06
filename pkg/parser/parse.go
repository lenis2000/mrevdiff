package parser

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
)

// TheoremEnv describes a theorem-like environment — either discovered via
// \newtheorem in the source, or supplied as a built-in default.
type TheoremEnv struct {
	Env     string
	Title   string
	Chain   string
	Starred bool
}

// Document is the result of Parse: a flat list of blocks plus a synthetic
// Root whose ChildIDs name the top-level blocks.
type Document struct {
	Source      []byte
	File        string
	Blocks      []*Block
	ByID        map[string]*Block
	ByLabel     map[string]*Block
	Root        *Block
	TheoremEnvs map[string]TheoremEnv
	BibEntries  map[string]*BibEntry
}

// Parse tokenizes src and builds the block tree.
// Only structural errors would be returned; the current implementation is
// best-effort and always returns a non-nil Document with a nil error.
func Parse(src []byte) (*Document, error) {
	p := newParser(src)
	p.collectTheoremEnvs()
	p.buildTree()
	p.segmentContainerGaps()
	p.segmentItemEnvs()
	p.segmentProofs()
	p.segmentLeafProse()
	p.segmentLongParagraphs()
	p.applyChunkBudget()
	p.sortBlocksByLine()
	p.assignStableIDs()
	p.resolveRefs()
	return p.doc, nil
}

// sortBlocksByLine reorders doc.Blocks so the slice reads in document
// order. The segmentation passes (proof, list, paragraph) append derived
// blocks to the end, which leaves doc.Blocks out of source order and
// causes consumers that treat slice position as document position
// (search index, annotation list, advanceAfterReview) to misorder
// derived blocks. The sort is stable on StartLine so a parent block
// and its sub-blocks that share the same starting line retain their
// parent-before-child relationship. The synthetic root stays at [0].
func (p *parser) sortBlocksByLine() {
	blocks := p.doc.Blocks
	if len(blocks) < 2 {
		return
	}
	// Root is pinned at index 0 by newParser; keep it there and sort the
	// remainder so we don't accidentally displace it by StartLine (it has
	// StartLine == 0).
	rest := blocks[1:]
	sort.SliceStable(rest, func(i, j int) bool {
		return rest[i].StartLine < rest[j].StartLine
	})
}

// builtinTheoremEnvs is merged with \newtheorem declarations so callers can
// rely on a reasonable set of names even if the author omitted declarations.
var builtinTheoremEnvs = map[string]TheoremEnv{
	"theorem":     {Env: "theorem", Title: "Theorem"},
	"lemma":       {Env: "lemma", Title: "Lemma"},
	"proposition": {Env: "proposition", Title: "Proposition"},
	"corollary":   {Env: "corollary", Title: "Corollary"},
	"definition":  {Env: "definition", Title: "Definition"},
	"conjecture":  {Env: "conjecture", Title: "Conjecture"},
	"remark":      {Env: "remark", Title: "Remark"},
	"example":     {Env: "example", Title: "Example"},
	"claim":       {Env: "claim", Title: "Claim"},
}

var figureEnvs = map[string]bool{
	"figure":  true,
	"figure*": true,
	"table":   true,
	"table*":  true,
}

var displayMathEnvs = map[string]bool{
	"equation":    true,
	"equation*":   true,
	"align":       true,
	"align*":      true,
	"gather":      true,
	"gather*":     true,
	"multline":    true,
	"multline*":   true,
	"eqnarray":    true,
	"eqnarray*":   true,
	"displaymath": true,
	"alignat":     true,
	"alignat*":    true,
	"flalign":     true,
	"flalign*":    true,
}

var bibEnvs = map[string]bool{
	"thebibliography": true,
}

// transparentEnvs are consumed by the parser without creating a block; their
// children become direct children of the enclosing container.
var transparentEnvs = map[string]bool{
	"document": true,
}

type parser struct {
	src           []byte
	tokens        []Token
	lineStarts    []int // byte offset of the start of line i (1-based: lineStarts[i-1])
	totalLines    int
	doc           *Document
	stack         []*Block
	nextID        int
	sectionLevels map[string]int // sectionID -> level
}

func newParser(src []byte) *parser {
	tokens := Tokenize(src)
	lineStarts := []int{0}
	for i, b := range src {
		if b == '\n' {
			lineStarts = append(lineStarts, i+1)
		}
	}
	root := &Block{Kind: KindOther, ID: "root"}
	doc := &Document{
		Source:      src,
		Blocks:      []*Block{root},
		ByID:        map[string]*Block{"root": root},
		ByLabel:     map[string]*Block{},
		Root:        root,
		TheoremEnvs: copyTheoremEnvs(builtinTheoremEnvs),
		BibEntries:  map[string]*BibEntry{},
	}
	return &parser{
		src:           src,
		tokens:        tokens,
		lineStarts:    lineStarts,
		totalLines:    len(lineStarts),
		doc:           doc,
		stack:         []*Block{root},
		sectionLevels: map[string]int{},
	}
}

func copyTheoremEnvs(m map[string]TheoremEnv) map[string]TheoremEnv {
	out := make(map[string]TheoremEnv, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (p *parser) collectTheoremEnvs() {
	for _, tk := range p.tokens {
		if tk.Kind == TokNewTheorem {
			p.doc.TheoremEnvs[tk.EnvName] = TheoremEnv{
				Env: tk.EnvName, Title: tk.Title, Chain: tk.Chain, Starred: tk.Starred,
			}
		}
	}
}

func (p *parser) newID() string {
	id := fmt.Sprintf("b%d", p.nextID)
	p.nextID++
	return id
}

func (p *parser) top() *Block { return p.stack[len(p.stack)-1] }

func (p *parser) pushBlock(b *Block) {
	parent := p.top()
	b.ParentID = parent.ID
	parent.ChildIDs = append(parent.ChildIDs, b.ID)
	p.doc.Blocks = append(p.doc.Blocks, b)
	p.doc.ByID[b.ID] = b
	p.stack = append(p.stack, b)
}

// popBlock pops the top block, finalises its EndLine and Source.
func (p *parser) popBlock(endLine int) *Block {
	b := p.top()
	if endLine < b.StartLine {
		endLine = b.StartLine
	}
	b.EndLine = endLine
	b.Source = p.extractSource(b.StartLine, b.EndLine)
	p.stack = p.stack[:len(p.stack)-1]
	return b
}

// extractSource returns src[startLine..endLine] (inclusive, 1-based lines)
// with any trailing newline trimmed.
func (p *parser) extractSource(startLine, endLine int) string {
	if startLine < 1 || endLine < startLine || startLine > p.totalLines {
		return ""
	}
	from := p.lineStarts[startLine-1]
	var to int
	if endLine >= p.totalLines {
		to = len(p.src)
	} else {
		to = p.lineStarts[endLine]
		if to > 0 && p.src[to-1] == '\n' {
			to--
		}
	}
	if to > len(p.src) {
		to = len(p.src)
	}
	return string(p.src[from:to])
}

// buildTree walks the token stream and constructs the block hierarchy.
// Proof-step segmentation is performed in a separate pass (segmentProofs).
func (p *parser) buildTree() {
	for _, tk := range p.tokens {
		switch tk.Kind {
		case TokSection:
			p.closeSectionsAtLevel(tk.Level, tk.Line)
			b := &Block{
				ID:        p.newID(),
				Kind:      KindSection,
				Title:     tk.Title,
				StartLine: tk.Line,
			}
			p.pushBlock(b)
			p.sectionLevels[b.ID] = tk.Level
		case TokBeginEnv:
			p.handleBeginEnv(tk)
		case TokEndEnv:
			p.handleEndEnv(tk)
		case TokDisplayOpen:
			b := &Block{
				ID:        p.newID(),
				Kind:      KindDisplay,
				StartLine: tk.Line,
			}
			p.pushBlock(b)
		case TokDisplayClose:
			if p.top().Kind == KindDisplay && p.top().EnvName == "" {
				p.popBlock(tk.Line)
			}
		case TokLabel:
			p.attachLabel(tk.Target)
		case TokBlankLine, TokCommentLine, TokNewTheorem, TokTheoremStyle, TokRef, TokItem:
			// Not used for tree structure; \item is consumed by segmentItemEnvs
			// and segmentProof; refs/newtheorem are handled by other passes.
		}
	}
	// Close any remaining blocks at end of document (usually an outer section).
	for len(p.stack) > 1 {
		p.popBlock(p.totalLines)
	}
}

func (p *parser) closeSectionsAtLevel(newLevel, line int) {
	// Only sections auto-close on a new section; envs must close via TokEndEnv.
	for len(p.stack) > 1 {
		top := p.top()
		if top.Kind != KindSection {
			break
		}
		lvl, ok := p.sectionLevels[top.ID]
		if !ok || lvl < newLevel {
			break
		}
		endLine := line - 1
		if endLine < top.StartLine {
			endLine = top.StartLine
		}
		p.popBlock(endLine)
	}
}

func (p *parser) handleBeginEnv(tk Token) {
	env := tk.EnvName
	if transparentEnvs[env] {
		return
	}
	b := &Block{
		ID:        p.newID(),
		Kind:      p.envKind(env),
		EnvName:   env,
		StartLine: tk.Line,
	}
	p.pushBlock(b)
}

func (p *parser) handleEndEnv(tk Token) {
	env := tk.EnvName
	if transparentEnvs[env] {
		return
	}
	// Walk down looking for the matching env. Pop any mismatched intermediaries
	// defensively — malformed input shouldn't wedge the parser.
	for i := len(p.stack) - 1; i > 0; i-- {
		b := p.stack[i]
		if b.EnvName == env {
			for len(p.stack)-1 >= i {
				p.popBlock(tk.Line)
			}
			return
		}
	}
}

func (p *parser) envKind(env string) Kind {
	switch env {
	case "proof":
		return KindProof
	case "abstract":
		return KindAbstract
	}
	if figureEnvs[env] {
		return KindFigure
	}
	if displayMathEnvs[env] {
		return KindDisplay
	}
	if bibEnvs[env] {
		return KindBibliography
	}
	if _, ok := p.doc.TheoremEnvs[env]; ok {
		return KindTheoremLike
	}
	return KindOther
}

func (p *parser) attachLabel(target string) {
	if target == "" {
		return
	}
	b := p.top()
	if b == p.doc.Root {
		return
	}
	if b.Label == "" {
		b.Label = target
	}
	p.doc.ByLabel[target] = b
}

// segmentContainerGaps walks every container that owns a structural body
// (the document Root and every KindSection block) and emits a KindParagraph
// child for each prose-bearing gap between, before, or after that
// container's existing structural children. It generalises the original
// root-only `segmentRootProse` so prose that sits inside a section between
// two theorems no longer disappears as its own chunk.
//
// For Root, the body bounds come from the \begin{document} / \end{document}
// envelope (or the whole file if the document env is absent). For sections,
// the bounds are [StartLine + 1, EndLine] — StartLine itself holds the
// \section{...} command and is not prose.
//
// Spans whose lines are all TeX metadata commands (\title, \maketitle,
// \medskip, …) are skipped so the outline doesn't fill up with title-block
// noise.
func (p *parser) segmentContainerGaps() {
	containers := []*Block{p.doc.Root}
	// Snapshot Blocks so appends during the loop don't grow the iteration.
	snapshot := append([]*Block(nil), p.doc.Blocks...)
	for _, b := range snapshot {
		if b.Kind == KindSection {
			containers = append(containers, b)
		}
	}
	for _, c := range containers {
		p.extractContainerGaps(c)
	}
}

// extractContainerGaps emits KindParagraph children for the prose-bearing
// gaps in container c. ChildIDs are re-sorted by StartLine after insertion
// so downstream consumers see siblings in document order.
func (p *parser) extractContainerGaps(c *Block) {
	start, end := p.containerBodyBounds(c)
	if end < start {
		return
	}

	// Sort existing children by StartLine for the cursor walk.
	kids := append([]string(nil), c.ChildIDs...)
	sort.SliceStable(kids, func(i, j int) bool {
		return p.doc.ByID[kids[i]].StartLine < p.doc.ByID[kids[j]].StartLine
	})

	type gap struct{ s, e int }
	var gaps []gap
	cursor := start
	for _, cid := range kids {
		ch := p.doc.ByID[cid]
		if ch.StartLine > cursor {
			gaps = append(gaps, gap{cursor, ch.StartLine - 1})
		}
		if ch.EndLine+1 > cursor {
			cursor = ch.EndLine + 1
		}
	}
	if cursor <= end {
		gaps = append(gaps, gap{cursor, end})
	}

	added := false
	for _, g := range gaps {
		for _, sp := range p.paragraphSpans(g.s, g.e) {
			if !p.spanHasProse(sp[0], sp[1]) {
				continue
			}
			b := &Block{
				ID:        p.newID(),
				Kind:      KindParagraph,
				StartLine: sp[0],
				EndLine:   sp[1],
				ParentID:  c.ID,
			}
			b.Source = p.extractSource(sp[0], sp[1])
			p.doc.Blocks = append(p.doc.Blocks, b)
			p.doc.ByID[b.ID] = b
			c.ChildIDs = append(c.ChildIDs, b.ID)
			added = true
		}
	}
	if added {
		sort.SliceStable(c.ChildIDs, func(i, j int) bool {
			return p.doc.ByID[c.ChildIDs[i]].StartLine < p.doc.ByID[c.ChildIDs[j]].StartLine
		})
	}
}

// containerBodyBounds returns the [start, end] line range that holds the
// "body" of c — the lines inside which a prose gap may live. Root uses the
// document env; sections start one line past the \section command itself
// and are clipped to docEnd so a trailing \end{document} can't end up in a
// gap when the section auto-closes at end-of-file.
func (p *parser) containerBodyBounds(c *Block) (int, int) {
	docStart, docEnd := p.documentBodyRange()
	if c == p.doc.Root {
		return docStart, docEnd
	}
	start := c.StartLine + 1
	end := c.EndLine
	if end > docEnd {
		end = docEnd
	}
	return start, end
}

// documentBodyRange returns [start, end] of the lines strictly inside the
// document env (or [1, totalLines] when no \begin{document} is present).
func (p *parser) documentBodyRange() (int, int) {
	var docStart, docEnd int
	for _, tk := range p.tokens {
		if tk.Kind == TokBeginEnv && tk.EnvName == "document" && docStart == 0 {
			docStart = tk.Line + 1
		}
		if tk.Kind == TokEndEnv && tk.EnvName == "document" {
			docEnd = tk.Line - 1
		}
	}
	if docStart == 0 {
		docStart = 1
	}
	if docEnd == 0 {
		docEnd = p.totalLines
	}
	return docStart, docEnd
}

// rootMetadataCommands are the TeX commands that we treat as pure metadata
// at the root of the document — spans consisting only of these don't become
// outline entries.
var rootMetadataCommands = []string{
	"title", "author", "date", "maketitle", "thanks", "address", "email",
	"medskip", "smallskip", "bigskip",
	"vspace", "hspace", "vfill", "hfill",
	"newpage", "pagebreak", "clearpage",
	"tableofcontents", "bibliographystyle", "bibliography",
	"noindent", "indent", "par",
	"label",
}

// spanHasProse returns true if [s,e] contains prose content — i.e. text that
// isn't just TeX metadata command invocations. Lines are joined before the
// check so that a multi-line argument like "\title{a\\\nb}" is consumed as
// one command.
func (p *parser) spanHasProse(s, e int) bool {
	var buf strings.Builder
	for ln := s; ln <= e; ln++ {
		if ln > s {
			buf.WriteByte('\n')
		}
		buf.WriteString(p.lineText(ln))
	}
	return !textIsRootMetadataOnly(buf.String())
}

// lineText returns the raw source of a single line without trailing newline.
func (p *parser) lineText(line int) string {
	if line < 1 || line > p.totalLines {
		return ""
	}
	from := p.lineStarts[line-1]
	var to int
	if line >= p.totalLines {
		to = len(p.src)
	} else {
		to = p.lineStarts[line]
	}
	raw := p.src[from:to]
	raw = bytes.TrimRight(raw, "\r\n")
	return string(raw)
}

// textIsRootMetadataOnly reports whether s consists entirely of TeX metadata
// commands from rootMetadataCommands (plus their possibly multi-line args).
// "\noindent Dear Alex," has "Dear Alex," left after stripping and returns
// false; "\title{a\\\nb}\n\maketitle" returns true.
func textIsRootMetadataOnly(s string) bool {
	for {
		s = trimTexSpace(s)
		if s == "" {
			return true
		}
		if s[0] != '\\' {
			return false
		}
		j := 1
		for j < len(s) && isAsciiLetter(s[j]) {
			j++
		}
		if j == 1 {
			return false
		}
		if !isRootMetadataCommand(s[1:j]) {
			return false
		}
		s = s[j:]
		for {
			s = trimTexSpace(s)
			if strings.HasPrefix(s, "*") {
				s = s[1:]
				continue
			}
			if strings.HasPrefix(s, "[") {
				rest, ok := stripBalanced(s, '[', ']')
				if !ok {
					return true
				}
				s = rest
				continue
			}
			if strings.HasPrefix(s, "{") {
				rest, ok := stripBalanced(s, '{', '}')
				if !ok {
					return true
				}
				s = rest
				continue
			}
			break
		}
	}
}

func trimTexSpace(s string) string {
	return strings.TrimLeft(s, " \t\n\r")
}

func isAsciiLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func isRootMetadataCommand(name string) bool {
	for _, c := range rootMetadataCommands {
		if c == name {
			return true
		}
	}
	return false
}

// stripBalanced consumes a balanced bracketed group starting at s[0] and
// returns the remainder. Braces inside are honoured (single level of nesting
// handled correctly via depth counter).
func stripBalanced(s string, open, close byte) (string, bool) {
	if len(s) == 0 || s[0] != open {
		return s, false
	}
	depth := 1
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			if i+1 < len(s) {
				i++
			}
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return s[i+1:], true
			}
		}
	}
	return s, false
}

// segmentProofs turns each KindProof block's flat child list into a tree of
// ProofStep blocks. Each maximal run of non-blank source lines inside the
// proof becomes one ProofStep; any pre-existing children (e.g. a KindDisplay
// from an align environment) are reparented to the step containing their
// start line.
func (p *parser) segmentProofs() {
	for _, b := range p.doc.Blocks {
		if b.Kind == KindProof {
			p.segmentProof(b)
		}
	}
}

func (p *parser) segmentProof(proof *Block) {
	if proof.StartLine == 0 || proof.EndLine == 0 {
		return
	}
	startLine := proof.StartLine + 1
	endLine := proof.EndLine - 1
	if endLine < startLine {
		return
	}

	// Forced-boundary lines: any pre-existing child of the proof (display,
	// theorem-like, list block) starts a fresh step, and so does any \item
	// token that fell inside the proof body but isn't yet wrapped in a
	// list block (defensive — the well-formed case is already covered by
	// list children).
	forced := map[int]bool{}
	for _, cid := range proof.ChildIDs {
		c := p.doc.ByID[cid]
		if c != nil && c.StartLine >= startLine && c.StartLine <= endLine {
			forced[c.StartLine] = true
		}
	}
	for _, tk := range p.tokens {
		if tk.Kind == TokItem && tk.Line >= startLine && tk.Line <= endLine {
			forced[tk.Line] = true
		}
	}

	var spans [][2]int
	i := startLine
	for i <= endLine {
		for i <= endLine && p.lineIsBlank(i) {
			i++
		}
		if i > endLine {
			break
		}
		s := i
		i++
		for i <= endLine && !p.lineIsBlank(i) && !forced[i] {
			i++
		}
		spans = append(spans, [2]int{s, i - 1})
	}
	if len(spans) == 0 {
		return
	}

	oldChildIDs := proof.ChildIDs
	proof.ChildIDs = nil

	type stepInfo struct {
		block *Block
		start int
		end   int
	}
	steps := make([]stepInfo, 0, len(spans))
	for _, sp := range spans {
		step := &Block{
			ID:        p.newID(),
			Kind:      KindProofStep,
			StartLine: sp[0],
			EndLine:   sp[1],
			ParentID:  proof.ID,
		}
		step.Source = p.extractSource(step.StartLine, step.EndLine)
		p.doc.Blocks = append(p.doc.Blocks, step)
		p.doc.ByID[step.ID] = step
		proof.ChildIDs = append(proof.ChildIDs, step.ID)
		steps = append(steps, stepInfo{step, sp[0], sp[1]})
	}

	for _, cid := range oldChildIDs {
		c := p.doc.ByID[cid]
		target := proof
		for idx := range steps {
			si := &steps[idx]
			if c.StartLine >= si.start && c.StartLine <= si.end {
				target = si.block
				if c.EndLine > target.EndLine {
					target.EndLine = c.EndLine
					target.Source = p.extractSource(target.StartLine, target.EndLine)
				}
				break
			}
		}
		c.ParentID = target.ID
		target.ChildIDs = append(target.ChildIDs, cid)
	}
}

func (p *parser) lineIsBlank(line int) bool {
	if line < 1 || line > p.totalLines {
		return true
	}
	from := p.lineStarts[line-1]
	var to int
	if line >= p.totalLines {
		to = len(p.src)
	} else {
		to = p.lineStarts[line]
	}
	raw := p.src[from:to]
	raw = bytes.TrimRight(raw, "\r\n")
	trimmed := bytes.TrimLeft(raw, " \t")
	if len(trimmed) == 0 {
		return true
	}
	if trimmed[0] == '%' {
		return true
	}
	return false
}

// listEnvs are environments whose direct children are \item-delimited
// entries; segmentItemEnvs splits each into KindParagraph sub-blocks.
var listEnvs = map[string]bool{
	"itemize":     true,
	"enumerate":   true,
	"description": true,
}

// segmentItemEnvs walks every itemize/enumerate/description leaf block and
// turns each \item into a paragraph sub-block, so list entries become
// individually navigable. Skips lists that already have structural children
// (e.g. a nested theorem inside an item) — those are rare and the existing
// tree is good enough.
func (p *parser) segmentItemEnvs() {
	blocks := append([]*Block(nil), p.doc.Blocks...)
	for _, b := range blocks {
		if b == p.doc.Root || len(b.ChildIDs) > 0 {
			continue
		}
		if b.EnvName == "" || !listEnvs[b.EnvName] {
			continue
		}
		startLine := b.StartLine + 1
		endLine := b.EndLine - 1
		if endLine < startLine {
			continue
		}
		// Token-driven: a single \item somewhere on a body line opens a new
		// entry. Multiple TokItems on the same source line collapse to one
		// entry (the line itself is the boundary; we don't subdivide within
		// a line because the rendered list still has one entry per \item).
		var itemStarts []int
		var lastLine int
		for _, tk := range p.tokens {
			if tk.Kind != TokItem {
				continue
			}
			if tk.Line < startLine || tk.Line > endLine {
				continue
			}
			if tk.Line == lastLine {
				continue
			}
			itemStarts = append(itemStarts, tk.Line)
			lastLine = tk.Line
		}
		if len(itemStarts) <= 1 {
			continue
		}
		for i, s := range itemStarts {
			e := endLine
			if i+1 < len(itemStarts) {
				e = itemStarts[i+1] - 1
			}
			child := &Block{
				ID:        p.newID(),
				Kind:      KindParagraph,
				StartLine: s,
				EndLine:   e,
				ParentID:  b.ID,
			}
			child.Source = p.extractSource(s, e)
			p.doc.Blocks = append(p.doc.Blocks, child)
			p.doc.ByID[child.ID] = child
			b.ChildIDs = append(b.ChildIDs, child.ID)
		}
	}
}

// proseSplittableKinds picks block kinds whose source is plain prose and
// therefore safe to chop on blank lines without losing semantic structure.
// Theorems and proofs intentionally stay whole — splitting them would
// fragment a single mathematical statement.
var proseSplittableKinds = map[Kind]bool{
	KindAbstract: true,
	KindOther:    true,
}

// segmentLeafProse splits any leaf block of a prose-y kind into
// blank-line-separated paragraphs. Leaves single-paragraph blocks alone so
// short notes don't grow noisy structure. Excludes list envs (handled by
// segmentItemEnvs) and figures/displays (no paragraph semantics).
func (p *parser) segmentLeafProse() {
	blocks := append([]*Block(nil), p.doc.Blocks...)
	for _, b := range blocks {
		if b == p.doc.Root || len(b.ChildIDs) > 0 {
			continue
		}
		if b.StartLine == 0 || b.EndLine == 0 {
			continue
		}
		if !proseSplittableKinds[b.Kind] {
			continue
		}
		if b.EnvName != "" && listEnvs[b.EnvName] {
			continue
		}
		startLine := b.StartLine
		endLine := b.EndLine
		if b.EnvName != "" {
			startLine++
			endLine--
		}
		if endLine < startLine {
			continue
		}
		spans := p.paragraphSpans(startLine, endLine)
		if len(spans) <= 1 {
			continue
		}
		for _, sp := range spans {
			child := &Block{
				ID:        p.newID(),
				Kind:      KindParagraph,
				StartLine: sp[0],
				EndLine:   sp[1],
				ParentID:  b.ID,
			}
			child.Source = p.extractSource(sp[0], sp[1])
			p.doc.Blocks = append(p.doc.Blocks, child)
			p.doc.ByID[child.ID] = child
			b.ChildIDs = append(b.ChildIDs, child.ID)
		}
	}
}

// paragraphSpans returns blank-line-separated [start, end] spans within
// [startLine, endLine]. Blank lines and comment-only lines are treated as
// separators (matches lineIsBlank).
func (p *parser) paragraphSpans(startLine, endLine int) [][2]int {
	var spans [][2]int
	i := startLine
	for i <= endLine {
		for i <= endLine && p.lineIsBlank(i) {
			i++
		}
		if i > endLine {
			break
		}
		s := i
		for i <= endLine && !p.lineIsBlank(i) {
			i++
		}
		spans = append(spans, [2]int{s, i - 1})
	}
	return spans
}

// longParagraphLineThreshold is the line count above which a paragraph is
// further chopped on sentence boundaries. Picked so that ~6 wrapped rows
// fit comfortably in the source pane without scrolling.
const longParagraphLineThreshold = 6

// segmentLongParagraphs walks every leaf prose-y block and, when its line
// count exceeds longParagraphLineThreshold, splits it on sentence
// boundaries. Acts on KindParagraph (the post-blank-line slices) as well
// as the original prose kinds (KindAbstract, KindOther) so that a
// single-paragraph abstract — which segmentLeafProse leaves alone because
// it has no blank-line breaks — still becomes navigable. Sub-blocks are
// always KindParagraph so the outline renders them uniformly.
func (p *parser) segmentLongParagraphs() {
	blocks := append([]*Block(nil), p.doc.Blocks...)
	for _, b := range blocks {
		if b == p.doc.Root || len(b.ChildIDs) > 0 {
			continue
		}
		if b.Kind != KindParagraph && !proseSplittableKinds[b.Kind] {
			continue
		}
		if b.EnvName != "" && listEnvs[b.EnvName] {
			continue
		}
		startLine := b.StartLine
		endLine := b.EndLine
		// Strip the env's `\begin{...}` and `\end{...}` framing lines so
		// the sentence walker doesn't try to split across them.
		if b.EnvName != "" {
			startLine++
			endLine--
		}
		if endLine-startLine+1 <= longParagraphLineThreshold {
			continue
		}
		spans := p.sentenceSpans(startLine, endLine)
		if len(spans) <= 1 {
			continue
		}
		for _, sp := range spans {
			child := &Block{
				ID:        p.newID(),
				Kind:      KindParagraph,
				StartLine: sp[0],
				EndLine:   sp[1],
				ParentID:  b.ID,
			}
			child.Source = p.extractSource(sp[0], sp[1])
			p.doc.Blocks = append(p.doc.Blocks, child)
			p.doc.ByID[child.ID] = child
			b.ChildIDs = append(b.ChildIDs, child.ID)
		}
	}
}

// sentenceSpans walks lines [startLine, endLine] and returns line ranges
// such that each range ends on a sentence-final line (line whose trimmed
// content ends with `.`, `?`, or `!`). The final span absorbs whatever's
// left if the paragraph doesn't end in a sentence terminator.
//
// We split at end-of-line, not mid-line, because the source pane renders
// rows by source line and a sub-block boundary that doesn't align with a
// line edge would be confusing in the outline.
func (p *parser) sentenceSpans(startLine, endLine int) [][2]int {
	var spans [][2]int
	s := startLine
	for ln := startLine; ln <= endLine; ln++ {
		if p.lineEndsSentence(ln) {
			spans = append(spans, [2]int{s, ln})
			s = ln + 1
		}
	}
	if s <= endLine {
		spans = append(spans, [2]int{s, endLine})
	}
	return spans
}

// lineEndsSentence reports whether the trimmed content of line ends with a
// sentence-terminating punctuation. Strips trailing whitespace, a trailing
// percent comment, and any run of trailing close-brackets/braces/parens
// so a sentence-ending period that's followed by `\footnote{…}` /
// parenthetical / bracketed closure on the same line (`text.})`) still
// registers. Without the bracket strip, a paragraph whose only sentence
// boundary sits inside such a closure looks unsplittable to the outline.
func (p *parser) lineEndsSentence(line int) bool {
	if line < 1 || line > p.totalLines {
		return false
	}
	from := p.lineStarts[line-1]
	var to int
	if line >= p.totalLines {
		to = len(p.src)
	} else {
		to = p.lineStarts[line]
	}
	raw := bytes.TrimRight(p.src[from:to], " \t\r\n")
	// Drop a trailing %-comment.
	if i := bytes.IndexByte(raw, '%'); i >= 0 {
		// Don't break on an escaped `\%`.
		if i == 0 || raw[i-1] != '\\' {
			raw = bytes.TrimRight(raw[:i], " \t")
		}
	}
	// Strip trailing close-brackets and any whitespace they expose.
	for {
		raw = bytes.TrimRight(raw, " \t")
		n := len(raw)
		if n == 0 {
			break
		}
		c := raw[n-1]
		if c != ')' && c != '}' && c != ']' {
			break
		}
		raw = raw[:n-1]
	}
	if len(raw) == 0 {
		return false
	}
	switch raw[len(raw)-1] {
	case '.', '?', '!':
		return true
	}
	return false
}
