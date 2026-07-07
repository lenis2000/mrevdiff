package format

import (
	"bytes"
	"regexp"
	"strconv"

	"github.com/lenis2000/mrevdiff/pkg/parser"
)

// registerDiagRules appends the Tier-3 diagnostic rules to the registry.
// Called from registry.go's init() to guarantee ordering after PDFFix rules.
func registerDiagRules() {
	Registry = append(Registry,
		Rule{
			ID:    "lint.ref-undefined",
			Tier:  DiagOnly,
			Doc:   "Flag \\ref{X} with no matching \\label{X}.",
			Apply: diagRefUndefined,
		},
		Rule{
			ID:    "lint.label-unused",
			Tier:  DiagOnly,
			Doc:   "Flag \\label{X} referenced nowhere.",
			Apply: diagLabelUnused,
		},
		Rule{
			ID:    "lint.label-duplicate",
			Tier:  DiagOnly,
			Doc:   "Flag same \\label{X} declared twice.",
			Apply: diagLabelDuplicate,
		},
		Rule{
			ID:    "lint.ref-should-eqref",
			Tier:  DiagOnly,
			Doc:   "Flag \\ref{X} where X labels a KindDisplay block; suggest \\eqref.",
			Apply: diagRefShouldEqref,
		},
		Rule{
			ID:    "lint.cite-undefined",
			Tier:  DiagOnly,
			Doc:   "Flag \\cite{X} with X not in .bbl.",
			Apply: diagCiteUndefined,
		},
		Rule{
			ID:    "lint.thm-unlabeled",
			Tier:  DiagOnly,
			Doc:   "Flag KindTheoremLike block with no \\label.",
			Apply: diagThmUnlabeled,
		},
		Rule{
			ID:    "lint.thm-orphan-proof",
			Tier:  DiagOnly,
			Doc:   "Flag KindProof not preceded by a theorem-like block.",
			Apply: diagThmOrphanProof,
		},
		Rule{
			ID:    "lint.thm-no-proof",
			Tier:  DiagOnly,
			Doc:   "Flag theorem stated with no proof block within next 5 outer-sibling blocks.",
			Apply: diagThmNoProof,
		},
		Rule{
			ID:    "lint.todo-marker",
			Tier:  DiagOnly,
			Doc:   "Flag \\colorbox{...}{\\parbox{...}{...}} TODO markers.",
			Apply: diagTodoMarker,
		},
		Rule{
			ID:    "lint.block-too-long",
			Tier:  DiagOnly,
			Doc:   "Flag KindParagraph blocks whose source span exceeds 40 lines.",
			Apply: diagBlockTooLong,
		},
	)
}

// ---------------------------------------------------------------------------
// lint.ref-undefined
// ---------------------------------------------------------------------------

func diagRefUndefined(ctx *Ctx) Result {
	if ctx.Doc == nil {
		return Result{Src: ctx.Src}
	}
	var diags []Diag
	for _, b := range ctx.Doc.Blocks {
		for _, ref := range b.RefsOut {
			if ref.Kind == "cite" {
				continue // cites are checked by lint.cite-undefined
			}
			if !ref.Resolved {
				diags = append(diags, Diag{
					RuleID:  "lint.ref-undefined",
					Line:    b.StartLine + ref.LineOffset,
					Message: `\` + ref.Kind + `{` + ref.Target + `} has no matching \label`,
				})
			}
		}
	}
	return Result{Src: ctx.Src, Diags: diags}
}

// ---------------------------------------------------------------------------
// lint.label-unused
// ---------------------------------------------------------------------------

func diagLabelUnused(ctx *Ctx) Result {
	if ctx.Doc == nil {
		return Result{Src: ctx.Src}
	}
	// Collect all referenced targets (excluding cites — those are bibliography
	// keys, not label references).
	referenced := make(map[string]bool)
	for _, b := range ctx.Doc.Blocks {
		for _, ref := range b.RefsOut {
			if ref.Kind == "cite" {
				continue
			}
			referenced[ref.Target] = true
		}
	}

	var diags []Diag
	for _, b := range ctx.Doc.Blocks {
		if b.Label == "" {
			continue
		}
		if !referenced[b.Label] {
			diags = append(diags, Diag{
				RuleID:  "lint.label-unused",
				Line:    b.StartLine,
				Message: `\label{` + b.Label + `} declared at L` + strconv.Itoa(b.StartLine) + `, never referenced`,
			})
		}
	}
	return Result{Src: ctx.Src, Diags: diags}
}

// ---------------------------------------------------------------------------
// lint.label-duplicate
// ---------------------------------------------------------------------------

func diagLabelDuplicate(ctx *Ctx) Result {
	if ctx.Doc == nil {
		return Result{Src: ctx.Src}
	}
	// Scan tokens for duplicate TokLabel declarations.
	seen := make(map[string]int) // label -> first line
	var diags []Diag
	for _, tok := range ctx.Tokens {
		if tok.Kind != parser.TokLabel {
			continue
		}
		if firstLine, ok := seen[tok.Target]; ok {
			diags = append(diags, Diag{
				RuleID:  "lint.label-duplicate",
				Line:    tok.Line,
				Message: `\label{` + tok.Target + `} already declared at L` + strconv.Itoa(firstLine),
			})
		} else {
			seen[tok.Target] = tok.Line
		}
	}
	return Result{Src: ctx.Src, Diags: diags}
}

// ---------------------------------------------------------------------------
// lint.ref-should-eqref
// ---------------------------------------------------------------------------

func diagRefShouldEqref(ctx *Ctx) Result {
	if ctx.Doc == nil {
		return Result{Src: ctx.Src}
	}
	var diags []Diag
	for _, b := range ctx.Doc.Blocks {
		for _, ref := range b.RefsOut {
			if ref.Kind != "ref" {
				continue // only plain \ref is suspicious
			}
			target, ok := ctx.Doc.ByLabel[ref.Target]
			if !ok {
				continue // unresolved; lint.ref-undefined handles it
			}
			if target.Kind == parser.KindDisplay {
				diags = append(diags, Diag{
					RuleID:  "lint.ref-should-eqref",
					Line:    b.StartLine + ref.LineOffset,
					Message: `\ref{` + ref.Target + `} targets display math; consider \eqref`,
				})
			}
		}
	}
	return Result{Src: ctx.Src, Diags: diags}
}

// ---------------------------------------------------------------------------
// lint.cite-undefined
// ---------------------------------------------------------------------------

func diagCiteUndefined(ctx *Ctx) Result {
	if ctx.Doc == nil {
		return Result{Src: ctx.Src}
	}
	// If no .bbl entries have been loaded, every cite ref has
	// Resolved==false (parser hardcodes this). Skip the rule entirely to
	// avoid a flood of false positives. The format pipeline does not
	// currently wire .bbl resolution; this guard prevents misleading output
	// until it does.
	if len(ctx.Doc.BibEntries) == 0 {
		return Result{Src: ctx.Src}
	}
	var diags []Diag
	for _, b := range ctx.Doc.Blocks {
		for _, ref := range b.RefsOut {
			if ref.Kind != "cite" {
				continue
			}
			if !ref.Resolved {
				diags = append(diags, Diag{
					RuleID:  "lint.cite-undefined",
					Line:    b.StartLine + ref.LineOffset,
					Message: `\cite{` + ref.Target + `} not found in .bbl`,
				})
			}
		}
	}
	return Result{Src: ctx.Src, Diags: diags}
}

// ---------------------------------------------------------------------------
// lint.thm-unlabeled
// ---------------------------------------------------------------------------

func diagThmUnlabeled(ctx *Ctx) Result {
	if ctx.Doc == nil {
		return Result{Src: ctx.Src}
	}
	var diags []Diag
	for _, b := range ctx.Doc.Blocks {
		if b.Kind == parser.KindTheoremLike && b.Label == "" {
			title := b.EnvName
			if te, ok := ctx.Doc.TheoremEnvs[b.EnvName]; ok {
				title = te.Title
			}
			diags = append(diags, Diag{
				RuleID:  "lint.thm-unlabeled",
				Line:    b.StartLine,
				Message: title + ` at L` + strconv.Itoa(b.StartLine) + ` has no \label`,
			})
		}
	}
	return Result{Src: ctx.Src, Diags: diags}
}

// ---------------------------------------------------------------------------
// lint.thm-orphan-proof
// ---------------------------------------------------------------------------

func diagThmOrphanProof(ctx *Ctx) Result {
	if ctx.Doc == nil {
		return Result{Src: ctx.Src}
	}
	var diags []Diag
	for _, b := range ctx.Doc.Blocks {
		if b.Kind != parser.KindProof {
			continue
		}
		prev := previousSiblingBlock(ctx.Doc, b)
		if prev == nil || prev.Kind != parser.KindTheoremLike {
			diags = append(diags, Diag{
				RuleID:  "lint.thm-orphan-proof",
				Line:    b.StartLine,
				Message: `Proof at L` + strconv.Itoa(b.StartLine) + ` not preceded by a theorem-like block`,
			})
		}
	}
	return Result{Src: ctx.Src, Diags: diags}
}

// previousSiblingBlock finds the previous sibling of b in its parent's child list.
func previousSiblingBlock(doc *parser.Document, b *parser.Block) *parser.Block {
	parent := doc.ByID[b.ParentID]
	if parent == nil {
		return nil
	}
	var prev *parser.Block
	for _, cid := range parent.ChildIDs {
		if cid == b.ID {
			return prev
		}
		prev = doc.ByID[cid]
	}
	return nil
}

// ---------------------------------------------------------------------------
// lint.thm-no-proof
// ---------------------------------------------------------------------------

func diagThmNoProof(ctx *Ctx) Result {
	if ctx.Doc == nil {
		return Result{Src: ctx.Src}
	}
	var diags []Diag
	for _, b := range ctx.Doc.Blocks {
		if b.Kind != parser.KindTheoremLike {
			continue
		}
		// Only check theorem-like envs that typically need proofs.
		// Definitions, conjectures, remarks, examples don't need proofs.
		if !envNeedsProof(b.EnvName) {
			continue
		}
		if !hasProofWithin5Siblings(ctx.Doc, b) {
			title := b.EnvName
			if te, ok := ctx.Doc.TheoremEnvs[b.EnvName]; ok {
				title = te.Title
			}
			label := ""
			if b.Label != "" {
				label = " (" + b.Label + ")"
			}
			diags = append(diags, Diag{
				RuleID:  "lint.thm-no-proof",
				Line:    b.StartLine,
				Message: title + label + ` at L` + strconv.Itoa(b.StartLine) + ` has no following proof in next 5 blocks`,
			})
		}
	}
	return Result{Src: ctx.Src, Diags: diags}
}

// envNeedsProof returns true for theorem-like envs that conventionally have proofs.
func envNeedsProof(envName string) bool {
	switch envName {
	case "theorem", "lemma", "proposition", "corollary", "claim":
		return true
	}
	return false
}

// hasProofWithin5Siblings checks whether there is a KindProof block within
// the next 5 outer-sibling blocks following b.
func hasProofWithin5Siblings(doc *parser.Document, b *parser.Block) bool {
	parent := doc.ByID[b.ParentID]
	if parent == nil {
		return false
	}
	// Find b's position in parent's children.
	idx := -1
	for i, cid := range parent.ChildIDs {
		if cid == b.ID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false
	}
	// Check the next 5 siblings (or fewer if we run out).
	limit := idx + 6 // up to 5 siblings after b
	if limit > len(parent.ChildIDs) {
		limit = len(parent.ChildIDs)
	}
	for i := idx + 1; i < limit; i++ {
		sibling := doc.ByID[parent.ChildIDs[i]]
		if sibling != nil && sibling.Kind == parser.KindProof {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// lint.todo-marker
// ---------------------------------------------------------------------------

// todoMarkerRe matches \colorbox{...}{\parbox{...}{...}} patterns.
// We use a simple regex to find the opening, then manually extract nested braces.
var todoMarkerRe = regexp.MustCompile(`\\colorbox\{`)

func diagTodoMarker(ctx *Ctx) Result {
	var diags []Diag
	src := ctx.Src

	for _, loc := range todoMarkerRe.FindAllIndex(src, -1) {
		start := loc[0]
		// Check if in protected region.
		if parser.OverlapsProtected(start, loc[1], ctx.Protected) {
			continue
		}

		// Parse: \colorbox{COLOR}{\parbox{WIDTH}{COMMENT}}
		pos := loc[1] // right after \colorbox{
		// Extract COLOR (first brace group)
		_, rest, ok := readBraceGroup(src, pos)
		if !ok {
			continue
		}
		// Skip whitespace.
		rest = skipWhitespace(src, rest)
		// Must start with {
		if rest >= len(src) || src[rest] != '{' {
			continue
		}
		// Look for \parbox inside this brace group.
		inner, innerEnd, ok := readBraceGroup(src, rest+1)
		if !ok {
			continue
		}
		_ = innerEnd

		// Check if inner starts with \parbox
		trimmed := bytes.TrimLeft(inner, " \t\n\r")
		if !bytes.HasPrefix(trimmed, []byte(`\parbox`)) {
			continue
		}
		// Extract the parbox content: \parbox{WIDTH}{COMMENT}
		cursor := len(`\parbox`)
		// Skip optional *
		if cursor < len(trimmed) && trimmed[cursor] == '*' {
			cursor++
		}
		// Skip optional [...]
		if cursor < len(trimmed) && trimmed[cursor] == '[' {
			_, cursor, ok = readBracketGroupBytes(trimmed, cursor+1)
			if !ok {
				continue
			}
		}
		// Read {WIDTH}
		if cursor >= len(trimmed) || trimmed[cursor] != '{' {
			continue
		}
		_, cursor, ok = readBraceGroup(trimmed, cursor+1)
		if !ok {
			continue
		}
		// Read {COMMENT}
		cursor = skipWhitespace(trimmed, cursor)
		if cursor >= len(trimmed) || trimmed[cursor] != '{' {
			continue
		}
		comment, _, ok := readBraceGroup(trimmed, cursor+1)
		if !ok {
			continue
		}

		line := lineAt(ctx.Lines, start)
		body := string(bytes.TrimSpace(comment))
		if body == "" {
			body = "(empty TODO)"
		}
		diags = append(diags, Diag{
			RuleID:  "lint.todo-marker",
			Line:    line,
			Message: "TODO: " + truncExcerpt(body),
		})
	}

	return Result{Src: ctx.Src, Diags: diags}
}

// readBraceGroup reads a brace-balanced group starting at src[pos], where src[pos-1]
// was the opening '{'. Returns the content bytes and the position after the closing '}'.
func readBraceGroup(src []byte, pos int) ([]byte, int, bool) {
	depth := 1
	start := pos
	for i := pos; i < len(src); i++ {
		switch src[i] {
		case '\\':
			i++ // skip escaped char
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[start:i], i + 1, true
			}
		}
	}
	return nil, pos, false
}

// readBracketGroupBytes reads a bracket-balanced [...] group starting at buf[pos],
// where buf[pos-1] was '['. Returns content and position after ']'.
func readBracketGroupBytes(buf []byte, pos int) ([]byte, int, bool) {
	depth := 1
	start := pos
	for i := pos; i < len(buf); i++ {
		switch buf[i] {
		case '\\':
			i++ // skip escaped char
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return buf[start:i], i + 1, true
			}
		}
	}
	return nil, pos, false
}

func skipWhitespace(src []byte, pos int) int {
	for pos < len(src) && (src[pos] == ' ' || src[pos] == '\t' || src[pos] == '\n' || src[pos] == '\r') {
		pos++
	}
	return pos
}

// ---------------------------------------------------------------------------
// lint.block-too-long
// ---------------------------------------------------------------------------

const longParagraphThreshold = 40

func diagBlockTooLong(ctx *Ctx) Result {
	if ctx.Doc == nil {
		return Result{Src: ctx.Src}
	}
	var diags []Diag
	for _, b := range ctx.Doc.Blocks {
		if b.Kind != parser.KindParagraph {
			continue
		}
		lineCount := b.EndLine - b.StartLine + 1
		if lineCount > longParagraphThreshold {
			diags = append(diags, Diag{
				RuleID:  "lint.block-too-long",
				Line:    b.StartLine,
				Message: "Paragraph block spans " + strconv.Itoa(lineCount) + " lines (threshold: " + strconv.Itoa(longParagraphThreshold) + ")",
			})
		}
	}
	return Result{Src: ctx.Src, Diags: diags}
}
