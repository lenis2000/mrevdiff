package format

import (
	"bytes"
	"regexp"

	"github.com/lenis2000/mrevdiff/pkg/parser"
)

// registerSafeRules appends the Tier-1 rules to the registry.
// Called from registry.go's init() to guarantee ordering before PDFFix rules.
func registerSafeRules() {
	Registry = append(Registry,
		Rule{
			ID:    "space.trailing",
			Tier:  Safe,
			Doc:   "Strip trailing whitespace per line.",
			Apply: applyTrailing,
		},
		Rule{
			ID:    "space.blank-runs",
			Tier:  Safe,
			Doc:   "Collapse runs of 3+ consecutive blank lines to 2 (one blank line).",
			Apply: applyBlankRuns,
		},
		Rule{
			ID:    "space.tabs",
			Tier:  Safe,
			Doc:   "Replace tabs with 4 spaces outside protected regions.",
			Apply: applyTabs,
		},
		Rule{
			ID:    "display.style",
			Tier:  Safe,
			Doc:   "Replace $$...$$ with \\[...\\].",
			Apply: applyDisplayStyle,
		},
		Rule{
			ID:    "space.item-per-line",
			Tier:  Safe,
			Doc:   "Force every \\item inside a list env onto its own line.",
			Apply: applyItemPerLine,
		},
		Rule{
			ID:    "space.proof-delim-per-line",
			Tier:  Safe,
			Doc:   "Force \\begin{proof} and \\end{proof} onto their own lines.",
			Apply: applyProofDelimPerLine,
		},
		Rule{
			ID:    "space.display-delim-per-line",
			Tier:  Safe,
			Doc:   "Force \\[, \\], and display-math env delimiters onto their own lines.",
			Apply: applyDisplayDelimPerLine,
		},
	)
}

// applyTrailing strips trailing whitespace (spaces and tabs) from each line,
// skipping lines that fall entirely within a protected region.
func applyTrailing(ctx *Ctx) Result {
	var hits []Hit
	var out []byte

	lines := bytes.Split(ctx.Src, []byte{'\n'})
	offset := 0
	changed := false

	for i, line := range lines {
		lineStart := offset
		lineEnd := lineStart + len(line)

		trimmed := bytes.TrimRight(line, " \t")
		// Only check whether the trailing whitespace region itself is protected.
		trailStart := lineStart + len(trimmed)
		if len(trimmed) < len(line) && !parser.OverlapsProtected(trailStart, lineEnd, ctx.Protected) && !ctx.LineSkipped(i+1) {
			out = append(out, trimmed...)
			changed = true
			hits = append(hits, Hit{
				RuleID:  "space.trailing",
				Line:    i + 1,
				Excerpt: truncExcerpt(string(line)),
			})
		} else {
			out = append(out, line...)
		}

		// Add newline separator between lines (not after the last line).
		if i < len(lines)-1 {
			out = append(out, '\n')
		}
		offset = lineEnd + 1 // +1 for the newline consumed by Split
	}

	if !changed {
		return Result{Src: ctx.Src}
	}
	return Result{Src: out, Hits: hits}
}

// blankRunRe matches runs of 3+ consecutive newlines (\n\n\n+).
// LaTeX treats any blank-line run as one paragraph break, so collapsing
// \n{3,} to \n\n is safe.
var blankRunRe = regexp.MustCompile(`\n{3,}`)

// applyBlankRuns collapses runs of 3+ consecutive newlines to exactly 2.
func applyBlankRuns(ctx *Ctx) Result {
	var hits []Hit

	locs := blankRunRe.FindAllIndex(ctx.Src, -1)
	if len(locs) == 0 {
		return Result{Src: ctx.Src}
	}

	var out []byte
	prev := 0
	for _, loc := range locs {
		start, end := loc[0], loc[1]
		// Check if the entire blank-line run overlaps a protected region.
		if parser.OverlapsProtected(start, end, ctx.Protected) || ctx.RangeSkipped(start, end) {
			out = append(out, ctx.Src[prev:end]...)
			prev = end
			continue
		}
		out = append(out, ctx.Src[prev:start]...)
		out = append(out, '\n', '\n')
		lineNum := lineAt(ctx.Lines, start)
		hits = append(hits, Hit{
			RuleID:  "space.blank-runs",
			Line:    lineNum,
			Excerpt: "collapsed blank-line run",
		})
		prev = end
	}
	out = append(out, ctx.Src[prev:]...)

	if len(hits) == 0 {
		return Result{Src: ctx.Src}
	}
	return Result{Src: out, Hits: hits}
}

// applyTabs replaces tab characters with 4 spaces, skipping protected regions.
//
// When the indentation pass uses tabs (`indent_char = "tab"`), the leading
// tabs that space.indent will write are NOT user errors, so this rule
// becomes a no-op to avoid a tabs↔spaces fight on every run.
func applyTabs(ctx *Ctx) Result {
	if ctx.Indent.Enabled && ctx.Indent.UseTab {
		return Result{Src: ctx.Src}
	}
	if !bytes.ContainsRune(ctx.Src, '\t') {
		return Result{Src: ctx.Src}
	}

	var hits []Hit
	out := make([]byte, 0, len(ctx.Src))

	for i := 0; i < len(ctx.Src); i++ {
		if ctx.Src[i] == '\t' && !parser.OverlapsProtected(i, i+1, ctx.Protected) && !ctx.RangeSkipped(i, i+1) {
			out = append(out, "    "...)
			lineNum := lineAt(ctx.Lines, i)
			hits = append(hits, Hit{
				RuleID:  "space.tabs",
				Line:    lineNum,
				Excerpt: "tab replaced",
			})
		} else {
			out = append(out, ctx.Src[i])
		}
	}

	if len(hits) == 0 {
		return Result{Src: ctx.Src}
	}
	return Result{Src: out, Hits: dedupeTabHits(hits)}
}

// applyDisplayStyle replaces $$...$$ with \[...\] outside protected regions.
func applyDisplayStyle(ctx *Ctx) Result {
	src := ctx.Src
	var hits []Hit
	var out []byte
	prev := 0
	changed := false

	for i := 0; i < len(src)-1; i++ {
		if src[i] != '$' || src[i+1] != '$' {
			continue
		}
		// Found '$$' at position i. Check if inside a protected/skipped region.
		if parser.OverlapsProtected(i, i+2, ctx.Protected) || ctx.RangeSkipped(i, i+2) {
			i++ // skip the second $
			continue
		}

		// Find the matching closing '$$'.
		closePos := findClosingDollarDollar(src, i+2, ctx.Protected)
		if closePos < 0 {
			i++ // no match; skip
			continue
		}

		// Extract the content between $$...$$.
		content := src[i+2 : closePos]

		out = append(out, src[prev:i]...)
		out = append(out, `\[`...)
		out = append(out, content...)
		out = append(out, `\]`...)
		prev = closePos + 2
		changed = true

		lineNum := lineAt(ctx.Lines, i)
		hits = append(hits, Hit{
			RuleID:  "display.style",
			Line:    lineNum,
			Excerpt: truncExcerpt("$$" + string(content)),
		})

		i = closePos + 1 // advance past the closing $$
	}

	if !changed {
		return Result{Src: ctx.Src}
	}
	out = append(out, src[prev:]...)
	return Result{Src: out, Hits: hits}
}

// findClosingDollarDollar finds the position of the next '$$' after pos,
// skipping protected regions. Returns -1 if not found.
func findClosingDollarDollar(src []byte, start int, protected []parser.ProtectedSpan) int {
	for i := start; i < len(src)-1; i++ {
		if src[i] == '$' && src[i+1] == '$' {
			if !parser.OverlapsProtected(i, i+2, protected) {
				return i
			}
			i++ // skip
		}
	}
	return -1
}

// lineAt returns the 1-based line number for the given byte offset using
// the precomputed line-start offsets.
func lineAt(lines []int, offset int) int {
	// lines[0] = 0 (sentinel), lines[1] = start of line 1, etc.
	// Binary search for the last entry <= offset.
	lo, hi := 1, len(lines)-1
	for lo <= hi {
		mid := (lo + hi) / 2
		if lines[mid] <= offset {
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return hi
}

// truncExcerpt truncates s to at most 80 runes.
func truncExcerpt(s string) string {
	runes := []rune(s)
	if len(runes) <= 80 {
		return s
	}
	return string(runes[:77]) + "..."
}

// dedupeTabHits collapses consecutive tab hits on the same line into one.
func dedupeTabHits(hits []Hit) []Hit {
	if len(hits) == 0 {
		return hits
	}
	out := []Hit{hits[0]}
	for _, h := range hits[1:] {
		if h.Line == out[len(out)-1].Line {
			continue
		}
		out = append(out, h)
	}
	return out
}

// listEnvNames is the set of LaTeX list environments whose \items the
// space.item-per-line rule operates inside. Mirrors pkg/parser.listEnvs.
var listEnvNames = map[string]bool{
	"itemize":     true,
	"enumerate":   true,
	"description": true,
}

// displayMathEnvNames is the set of math environments whose \begin/\end
// delimiters space.display-delim-per-line forces onto their own lines.
var displayMathEnvNames = map[string]bool{
	"equation":    true,
	"equation*":   true,
	"align":       true,
	"align*":      true,
	"alignat":     true,
	"alignat*":    true,
	"gather":      true,
	"gather*":     true,
	"multline":    true,
	"multline*":   true,
	"eqnarray":    true,
	"eqnarray*":   true,
	"flalign":     true,
	"flalign*":    true,
	"displaymath": true,
}

// applyItemPerLine inserts a newline before any \item that shares a line
// with non-whitespace content, but only when the \item lies inside an
// itemize/enumerate/description env. Idempotent: after one pass every
// \item is the first non-whitespace token on its line, so the precondition
// is false on a second run.
func applyItemPerLine(ctx *Ctx) Result {
	src := ctx.Src
	depth := 0 // nesting depth inside list envs
	var inserts []newlineInsert

	for _, tk := range ctx.Tokens {
		switch tk.Kind {
		case parser.TokBeginEnv:
			if listEnvNames[tk.EnvName] {
				depth++
			}
		case parser.TokEndEnv:
			if listEnvNames[tk.EnvName] && depth > 0 {
				depth--
			}
		case parser.TokItem:
			if depth == 0 {
				continue
			}
			pos := byteOffsetOf(ctx, tk.Line, tk.Col)
			if pos < 0 {
				continue
			}
			lineStart := ctx.Lines[tk.Line]
			if !hasNonWhitespacePrefix(src[lineStart:pos]) {
				continue
			}
			if parser.OverlapsProtected(pos, pos+5, ctx.Protected) {
				continue
			}
			if ctx.LineSkipped(tk.Line) {
				continue
			}
			inserts = append(inserts, newlineInsert{pos: pos, line: tk.Line})
		}
	}

	if len(inserts) == 0 {
		return Result{Src: src}
	}

	out := insertNewlines(src, insertPositions(inserts))
	hits := make([]Hit, 0, len(inserts))
	for _, ins := range inserts {
		hits = append(hits, Hit{
			RuleID:  "space.item-per-line",
			Line:    ins.line,
			Excerpt: `\item`,
		})
	}
	return Result{Src: out, Hits: hits}
}

// applyProofDelimPerLine forces \begin{proof} (with any optional [arg])
// and \end{proof} onto their own source lines.
func applyProofDelimPerLine(ctx *Ctx) Result {
	return applyEnvDelimPerLine(ctx, "space.proof-delim-per-line", func(env string) bool {
		return env == "proof"
	})
}

// applyDisplayDelimPerLine forces \[ , \] and the begin/end of every
// displayed-math env onto their own lines.
func applyDisplayDelimPerLine(ctx *Ctx) Result {
	src := ctx.Src
	var inserts []newlineInsert

	addLeading := func(pos int, line int) {
		if pos < 0 {
			return
		}
		lineStart := ctx.Lines[line]
		if !hasNonWhitespacePrefix(src[lineStart:pos]) {
			return
		}
		if parser.OverlapsProtected(pos, pos+1, ctx.Protected) {
			return
		}
		if ctx.LineSkipped(line) {
			return
		}
		inserts = append(inserts, newlineInsert{pos: pos, line: line})
	}
	addTrailing := func(endPos int, line int) {
		if endPos < 0 || endPos >= len(src) {
			return
		}
		// Already at end-of-line (or only whitespace/comment until newline)?
		if isLineTailEmpty(src, endPos) {
			return
		}
		if parser.OverlapsProtected(endPos, endPos+1, ctx.Protected) {
			return
		}
		if ctx.LineSkipped(line) {
			return
		}
		inserts = append(inserts, newlineInsert{pos: endPos, line: line})
	}

	for _, tk := range ctx.Tokens {
		switch tk.Kind {
		case parser.TokDisplayOpen, parser.TokDisplayClose:
			pos := byteOffsetOf(ctx, tk.Line, tk.Col)
			if pos < 0 {
				continue
			}
			// Skip $$ — display.style converts those first; if any survive
			// (e.g. inside protected regions), we leave them alone.
			if pos+1 < len(src) && src[pos] == '$' {
				continue
			}
			addLeading(pos, tk.Line)
			addTrailing(pos+2, tk.Line) // \[ or \] is two bytes
		case parser.TokBeginEnv:
			if !displayMathEnvNames[tk.EnvName] {
				continue
			}
			pos := byteOffsetOf(ctx, tk.Line, tk.Col)
			if pos < 0 {
				continue
			}
			endPos := delimEndAfterBegin(src, pos)
			if endPos < 0 {
				continue
			}
			addLeading(pos, tk.Line)
			addTrailing(endPos, tk.Line)
		case parser.TokEndEnv:
			if !displayMathEnvNames[tk.EnvName] {
				continue
			}
			pos := byteOffsetOf(ctx, tk.Line, tk.Col)
			if pos < 0 {
				continue
			}
			endPos := delimEndAfterEnd(src, pos)
			if endPos < 0 {
				continue
			}
			addLeading(pos, tk.Line)
			addTrailing(endPos, tk.Line)
		}
	}

	if len(inserts) == 0 {
		return Result{Src: src}
	}
	out := insertNewlines(src, insertPositions(inserts))
	hits := make([]Hit, 0, len(inserts))
	for _, ins := range inserts {
		hits = append(hits, Hit{
			RuleID:  "space.display-delim-per-line",
			Line:    ins.line,
			Excerpt: "display delim",
		})
	}
	return Result{Src: out, Hits: hits}
}

// applyEnvDelimPerLine is the shared body of the proof- and display-delim
// rules: for every TokBeginEnv / TokEndEnv whose env name passes the
// predicate, force the entire `\begin{env}[opt]` / `\end{env}` token
// span onto its own line.
func applyEnvDelimPerLine(ctx *Ctx, ruleID string, match func(env string) bool) Result {
	src := ctx.Src
	var inserts []newlineInsert

	addBoth := func(startPos, endPos, line int) {
		if ctx.LineSkipped(line) {
			return
		}
		lineStart := ctx.Lines[line]
		if startPos > 0 && hasNonWhitespacePrefix(src[lineStart:startPos]) &&
			!parser.OverlapsProtected(startPos, startPos+1, ctx.Protected) {
			inserts = append(inserts, newlineInsert{pos: startPos, line: line})
		}
		if endPos > 0 && endPos < len(src) && !isLineTailEmpty(src, endPos) &&
			!parser.OverlapsProtected(endPos, endPos+1, ctx.Protected) {
			inserts = append(inserts, newlineInsert{pos: endPos, line: line})
		}
	}

	for _, tk := range ctx.Tokens {
		switch tk.Kind {
		case parser.TokBeginEnv:
			if !match(tk.EnvName) {
				continue
			}
			pos := byteOffsetOf(ctx, tk.Line, tk.Col)
			if pos < 0 {
				continue
			}
			addBoth(pos, delimEndAfterBegin(src, pos), tk.Line)
		case parser.TokEndEnv:
			if !match(tk.EnvName) {
				continue
			}
			pos := byteOffsetOf(ctx, tk.Line, tk.Col)
			if pos < 0 {
				continue
			}
			addBoth(pos, delimEndAfterEnd(src, pos), tk.Line)
		}
	}

	if len(inserts) == 0 {
		return Result{Src: src}
	}
	out := insertNewlines(src, insertPositions(inserts))
	hits := make([]Hit, 0, len(inserts))
	for _, ins := range inserts {
		hits = append(hits, Hit{
			RuleID:  ruleID,
			Line:    ins.line,
			Excerpt: "env delim",
		})
	}
	return Result{Src: out, Hits: hits}
}

// byteOffsetOf returns the byte offset of (line, col) in ctx.Src using the
// precomputed line-start table. Returns -1 if the position is out of range.
func byteOffsetOf(ctx *Ctx, line, col int) int {
	if line < 1 || line >= len(ctx.Lines) || col < 1 {
		return -1
	}
	pos := ctx.Lines[line] + col - 1
	if pos < 0 || pos >= len(ctx.Src) {
		return -1
	}
	return pos
}

// hasNonWhitespacePrefix reports whether b contains any non-whitespace byte.
// Whitespace is space, tab, carriage return.
func hasNonWhitespacePrefix(b []byte) bool {
	for _, c := range b {
		if c != ' ' && c != '\t' && c != '\r' {
			return true
		}
	}
	return false
}

// isLineTailEmpty reports whether the bytes from pos to the next newline
// contain only whitespace (and optionally a `%` comment which already
// terminates the line). Used to decide whether to insert a trailing newline.
func isLineTailEmpty(src []byte, pos int) bool {
	for i := pos; i < len(src); i++ {
		c := src[i]
		if c == '\n' {
			return true
		}
		if c == '%' {
			return true // comment runs to EOL — effectively empty
		}
		if c != ' ' && c != '\t' && c != '\r' {
			return false
		}
	}
	return true
}

// delimEndAfterBegin returns the byte position immediately after a
// `\begin{name}` (and any optional `[arg]`), starting from the position
// of the `\`. Returns -1 if the source isn't well-formed at that point.
func delimEndAfterBegin(src []byte, start int) int {
	// Skip the literal "\begin", then a balanced { ... }, then optionally
	// whitespace and a balanced [ ... ].
	pos := skipLiteral(src, start, `\begin`)
	if pos < 0 {
		return -1
	}
	pos = skipBalanced(src, pos, '{', '}')
	if pos < 0 {
		return -1
	}
	// Optional [arg] only if it begins on the same source line.
	saved := pos
	for pos < len(src) && (src[pos] == ' ' || src[pos] == '\t') {
		pos++
	}
	if pos < len(src) && src[pos] == '[' {
		end := skipBalanced(src, pos, '[', ']')
		if end > 0 {
			return end
		}
	}
	return saved
}

// delimEndAfterEnd returns the byte position immediately after `\end{name}`.
func delimEndAfterEnd(src []byte, start int) int {
	pos := skipLiteral(src, start, `\end`)
	if pos < 0 {
		return -1
	}
	return skipBalanced(src, pos, '{', '}')
}

// skipLiteral returns the position immediately after lit if src[start:]
// begins with lit (byte-equal), else -1.
func skipLiteral(src []byte, start int, lit string) int {
	if start+len(lit) > len(src) {
		return -1
	}
	for i := 0; i < len(lit); i++ {
		if src[start+i] != lit[i] {
			return -1
		}
	}
	return start + len(lit)
}

// skipBalanced returns the position immediately after a matching close
// bracket, given that src[start] == open. Returns -1 if no match found
// before end-of-line (we don't span multi-line braces here — math papers
// keep delimiter args single-line).
func skipBalanced(src []byte, start int, open, close byte) int {
	if start >= len(src) || src[start] != open {
		return -1
	}
	depth := 1
	for i := start + 1; i < len(src); i++ {
		c := src[i]
		if c == '\n' {
			return -1
		}
		if c == '\\' && i+1 < len(src) {
			i++
			continue
		}
		switch c {
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}

// newlineInsert is an insertion site: a byte position in the source where
// a `\n` should be added, plus the (1-based) source line number used for
// the rule's Hit metadata.
type newlineInsert struct {
	pos  int
	line int
}

// insertPositions returns just the positions, sorted ascending, deduped.
func insertPositions(inserts []newlineInsert) []int {
	if len(inserts) == 0 {
		return nil
	}
	seen := map[int]bool{}
	out := make([]int, 0, len(inserts))
	for _, ins := range inserts {
		if seen[ins.pos] {
			continue
		}
		seen[ins.pos] = true
		out = append(out, ins.pos)
	}
	sortAscInts(out)
	return out
}

// insertNewlines returns a new byte slice with '\n' inserted at each of
// the (sorted, ascending) positions in src.
func insertNewlines(src []byte, positions []int) []byte {
	if len(positions) == 0 {
		return src
	}
	out := make([]byte, 0, len(src)+len(positions))
	prev := 0
	for _, p := range positions {
		if p < prev || p > len(src) {
			continue
		}
		out = append(out, src[prev:p]...)
		out = append(out, '\n')
		prev = p
	}
	out = append(out, src[prev:]...)
	return out
}

func sortAscInts(a []int) {
	// Insertion sort is fine — call sites have a few dozen entries at most.
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}
