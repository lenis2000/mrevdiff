package format

import (
	"strings"

	"github.com/lenis2000/mrevdiff/pkg/parser"
)

// MathWrapOptions controls the math.wrap-at-break-op rule.
type MathWrapOptions struct {
	// Enabled gates the whole pass; when false, applyMathWrap is a no-op.
	// Off by default (opt-in).
	Enabled bool
	// Col is the target column width. Lines shorter than this are left alone.
	// Defaults to 80 if zero.
	Col int
}

// mathWrapEnvs is the set of equation environments where wrapping applies.
// Same set as contIndentEnvs; multi-column rows (containing &) are skipped.
var mathWrapEnvs = map[string]bool{
	"equation":  true,
	"equation*": true,
	"gather":    true,
	"gather*":   true,
	"multline":  true,
	"multline*": true,
	"align":     true,
	"align*":    true,
}

// breakOps is the ordered list of binary/relational operators that the
// wrap rule can split at. Longer tokens are checked first so that e.g.
// ":=" is preferred over "=".
var breakOps = []string{
	// multi-char tokens first
	":=",
	"\\equiv",
	"\\leq",
	"\\geq",
	"\\le",
	"\\ge",
	"\\pm",
	"\\mp",
	"\\cdot",
	"\\times",
	"\\cap",
	"\\cup",
	"\\oplus",
	"\\otimes",
	"\\wedge",
	"\\vee",
	// single-char tokens
	"=",
	"+",
	"-",
}

func registerMathWrapRule() {
	Registry = append(Registry, Rule{
		ID:    "math.wrap-at-break-op",
		Tier:  Safe,
		Doc:   "Wrap long equation rows at a break operator (opt-in).",
		Apply: applyMathWrap,
	})
}

// applyMathWrap implements the math.wrap-at-break-op rule.
func applyMathWrap(ctx *Ctx) Result {
	if !ctx.MathWrap.Enabled {
		return Result{Src: ctx.Src}
	}

	wrapCol := ctx.MathWrap.Col
	if wrapCol <= 0 {
		wrapCol = 80
	}

	type envSpan struct {
		name      string
		bodyStart int
		bodyEnd   int
		beginLine int
	}

	var spans []envSpan
	depth := 0
	for _, tk := range ctx.Tokens {
		switch tk.Kind {
		case parser.TokBeginEnv:
			if mathWrapEnvs[tk.EnvName] {
				if depth == 0 {
					pos := byteOffsetOf(ctx, tk.Line, tk.Col)
					if pos >= 0 && !parser.OverlapsProtected(pos, pos+1, ctx.Protected) && !ctx.LineSkipped(tk.Line) {
						if endPos := delimEndAfterBeginWithArgs(ctx.Src, pos); endPos >= 0 {
							spans = append(spans, envSpan{
								name:      tk.EnvName,
								bodyStart: endPos,
								beginLine: tk.Line,
							})
						}
					}
				}
				depth++
			}
		case parser.TokEndEnv:
			if mathWrapEnvs[tk.EnvName] {
				depth--
				if depth == 0 && len(spans) > 0 {
					last := &spans[len(spans)-1]
					if last.bodyEnd == 0 {
						pos := byteOffsetOf(ctx, tk.Line, tk.Col)
						if pos >= 0 {
							last.bodyEnd = pos
						}
					}
				}
				if depth < 0 {
					depth = 0
				}
			}
		}
	}

	if len(spans) == 0 {
		return Result{Src: ctx.Src}
	}

	changed := false
	var hits []Hit

	// Copy sub-slices of ctx.Src before using as append targets
	// to avoid corrupting ctx.Src through shared backing arrays.
	out := make([]byte, 0, len(ctx.Src))
	prev := len(ctx.Src)
	for i := len(spans) - 1; i >= 0; i-- {
		sp := spans[i]
		if sp.bodyEnd <= sp.bodyStart || sp.bodyEnd > len(ctx.Src) {
			continue
		}
		// Skip entire environment if any body line is masked by
		// % mrevdiff-fmt: off/on or skip directives.
		if ctx.RangeSkipped(sp.bodyStart, sp.bodyEnd) {
			tail := append([]byte(nil), ctx.Src[sp.bodyEnd:prev]...)
			out = append(tail, out...)
			bodyCP := append([]byte(nil), ctx.Src[sp.bodyStart:sp.bodyEnd]...)
			out = append(bodyCP, out...)
			prev = sp.bodyStart
			continue
		}
		body := ctx.Src[sp.bodyStart:sp.bodyEnd]

		newBody, ok := wrapBody(body, wrapCol)
		if ok && string(newBody) != string(body) {
			changed = true
			hits = append(hits, Hit{
				RuleID:  "math.wrap-at-break-op",
				Line:    sp.beginLine,
				Excerpt: truncExcerpt("wrap-at-break-op " + sp.name),
			})
			tail := append([]byte(nil), ctx.Src[sp.bodyEnd:prev]...)
			out = append(tail, out...)
			out = append(newBody, out...)
		} else {
			tail := append([]byte(nil), ctx.Src[sp.bodyEnd:prev]...)
			out = append(tail, out...)
			bodyCP := append([]byte(nil), body...)
			out = append(bodyCP, out...)
		}
		prev = sp.bodyStart
	}

	if !changed {
		return Result{Src: ctx.Src}
	}

	prefix := append([]byte(nil), ctx.Src[:prev]...)
	out = append(prefix, out...)
	return Result{Src: out, Hits: hits}
}

// wrapBody processes the body of an equation environment and wraps long
// lines at break operators. Returns the new body and true if changes were
// made.
func wrapBody(body []byte, wrapCol int) ([]byte, bool) {
	s := string(body)
	lines := strings.Split(s, "\n")

	changed := false
	var result []string

	for _, line := range lines {
		wrapped := wrapLine(line, wrapCol)
		if len(wrapped) > 1 {
			changed = true
		}
		result = append(result, wrapped...)
	}

	if !changed {
		return body, false
	}

	return []byte(strings.Join(result, "\n")), true
}

// wrapLine wraps a single line at the rightmost break operator within the
// column budget. If the line fits or no suitable break point is found,
// returns the line unchanged (single-element slice).
//
// The operator moves to the start of the new continuation line, indented
// to align one column past the relation anchor (matching continuation-indent
// behavior).
func wrapLine(line string, wrapCol int) []string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return []string{line}
	}

	// Skip lines with & at depth 0 (multi-column handled by align-columns).
	if containsAmpAtDepth0(line) {
		return []string{line}
	}

	// Measure the visual width of the line.
	if visualWidth(line) <= wrapCol {
		return []string{line}
	}

	// Determine leading whitespace for indentation calculations.
	leadingWS := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
	content := strings.TrimLeft(line, " \t")

	// Find the rightmost break operator at brace depth 0 that keeps the
	// first part within the column budget.
	splitIdx, opLen := findRightmostBreakOp(content, wrapCol-visualWidth(leadingWS))
	if splitIdx < 0 {
		return []string{line}
	}

	// The part before the operator (trimmed trailing whitespace).
	before := strings.TrimRight(content[:splitIdx], " \t")
	// The operator and everything after it.
	op := content[splitIdx : splitIdx+opLen]
	after := strings.TrimLeft(content[splitIdx+opLen:], " \t")

	// Compute continuation indent: try to match the continuation-indent rule
	// by aligning the operator one column past the relation anchor.
	contIndent := computeWrapIndent(content, leadingWS)

	firstLine := leadingWS + before
	secondLine := contIndent + op
	if after != "" {
		secondLine += " " + after
	}

	// Recursively wrap the second line if it's still too long.
	wrapped := wrapLine(secondLine, wrapCol)
	return append([]string{firstLine}, wrapped...)
}

// findRightmostBreakOp scans content for the rightmost break operator at
// brace depth 0 whose position (visual column) is within budget. Returns
// the byte index into content and the operator's byte length, or (-1, 0).
func findRightmostBreakOp(content string, budget int) (int, int) {
	bestIdx := -1
	bestLen := 0
	depth := 0

	for i := 0; i < len(content); i++ {
		ch := content[i]
		switch {
		case ch == '{':
			depth++
		case ch == '}':
			if depth > 0 {
				depth--
			}
		case ch == '\\' && depth == 0:
			// Check for command break operators.
			matchedLen := 0
			for _, op := range breakOps {
				if op[0] != '\\' {
					continue
				}
				if strings.HasPrefix(content[i:], op) {
					endIdx := i + len(op)
					if endIdx < len(content) && isAlpha(content[endIdx]) {
						continue
					}
					matchedLen = len(op)
					col := visualWidth(content[:i])
					if col > 0 && col <= budget {
						bestIdx = i
						bestLen = len(op)
					}
					break
				}
			}
			if matchedLen == 0 {
				// Skip other backslash sequences.
				if i+1 < len(content) && isAlpha(content[i+1]) {
					j := i + 1
					for j < len(content) && isAlpha(content[j]) {
						j++
					}
					i = j - 1
				} else if i+1 < len(content) {
					i++
				}
			} else {
				// Skip past the matched operator.
				i += matchedLen - 1
			}
		case depth == 0:
			// Check single-char break operators.
			for _, op := range breakOps {
				if op[0] == '\\' {
					continue
				}
				if strings.HasPrefix(content[i:], op) {
					col := visualWidth(content[:i])
					// Must not be at the very start (col > 0) and must fit.
					if col > 0 && col <= budget {
						bestIdx = i
						bestLen = len(op)
					}
					i += len(op) - 1 // skip past the full operator
					break
				}
			}
		}
	}

	return bestIdx, bestLen
}

// computeWrapIndent computes the leading whitespace for a continuation line
// after wrapping. It tries to find a relation anchor on the content line
// and indent one column past it (matching continuation-indent convention).
// Falls back to the original leading whitespace + 2 spaces.
func computeWrapIndent(content string, leadingWS string) string {
	trimmed := strings.TrimSpace(content)
	anchorCol := findRelationAnchor(trimmed)
	if anchorCol >= 0 {
		return strings.Repeat(" ", visualWidth(leadingWS)+anchorCol+1)
	}
	// Fallback: indent 2 spaces past the leading whitespace visual width.
	return strings.Repeat(" ", visualWidth(leadingWS)+2)
}
