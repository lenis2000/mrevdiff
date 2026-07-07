package format

import (
	"strings"

	"github.com/lenis2000/mrevdiff/pkg/parser"
)

// contIndentEnvs is the set of equation environments where continuation
// indent applies. Multi-column envs (align, align*) are included but
// skipped when rows contain & (math.align-columns handles those).
var contIndentEnvs = map[string]bool{
	"equation":  true,
	"equation*": true,
	"gather":    true,
	"gather*":   true,
	"multline":  true,
	"multline*": true,
	"align":     true,
	"align*":    true,
}

// relationOps are the operators that serve as the "anchor" on the first
// row of an equation environment. The rule scans left-to-right at brace
// depth 0 and picks the first match.
var relationOps = []string{
	":=", // short token, check before plain =
	"\\equiv",
	"\\le",
	"\\ge",
	"\\leq",
	"\\geq",
	"=",
}

// binopPrefixes are tokens that, when a continuation row starts with one
// (after optional whitespace), mark that row as a candidate for indentation.
var binopPrefixes = []string{
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
	"+",
	"-",
	"<",
	">",
}

func registerMathContRule() {
	Registry = append(Registry, Rule{
		ID:    "math.continuation-indent",
		Tier:  Safe,
		Doc:   "Indent continuation lines in equation environments so binops align past the anchor operator.",
		Apply: applyMathCont,
	})
}

// applyMathCont implements the math.continuation-indent rule.
func applyMathCont(ctx *Ctx) Result {
	type envSpan struct {
		name      string
		bodyStart int // byte offset after \begin{name}[opt]
		bodyEnd   int // byte offset of \end{name}
		beginLine int // 1-based line number
	}

	var spans []envSpan
	depth := 0
	for _, tk := range ctx.Tokens {
		switch tk.Kind {
		case parser.TokBeginEnv:
			if contIndentEnvs[tk.EnvName] {
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
			if contIndentEnvs[tk.EnvName] {
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

	// Process in reverse so byte offsets stay valid.
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

		newBody, ok := contIndentBody(body)
		if ok && string(newBody) != string(body) {
			changed = true
			hits = append(hits, Hit{
				RuleID:  "math.continuation-indent",
				Line:    sp.beginLine,
				Excerpt: truncExcerpt("continuation-indent " + sp.name),
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

// contIndentBody processes the body of an equation environment and adjusts
// leading whitespace on continuation lines. It splits on newlines (not \\)
// so that it works for all equation-type environments.
//
// Returns the new body and true if changes were made, or the original body
// and false if the env should be skipped.
func contIndentBody(body []byte) ([]byte, bool) {
	s := string(body)

	// Split into lines (preserving the final newline state).
	lines := strings.Split(s, "\n")

	// Find the first non-empty content line as the anchor line.
	anchorIdx := -1
	for i, line := range lines {
		if strings.TrimSpace(line) != "" {
			anchorIdx = i
			break
		}
	}
	if anchorIdx < 0 {
		return body, false
	}

	// Need at least one line after the anchor.
	hasContentAfter := false
	for i := anchorIdx + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "" {
			hasContentAfter = true
			break
		}
	}
	if !hasContentAfter {
		return body, false
	}

	// If any line contains & at depth 0, this is a multi-column env
	// that align-columns should handle. Skip.
	for _, line := range lines {
		if containsAmpAtDepth0(line) {
			return body, false
		}
	}

	// Find the anchor: the column position of the first relation
	// operator on the anchor line (at brace depth 0).
	anchorLine := lines[anchorIdx]
	anchorContent := strings.TrimSpace(anchorLine)
	anchorCol := findRelationAnchor(anchorContent)
	if anchorCol < 0 {
		return body, false
	}

	// The target indentation for continuation binops: leading whitespace
	// width + anchor column + 1, so the binop visually sits just past
	// the relation operator on the anchor line.
	anchorLeadWS := anchorLine[:len(anchorLine)-len(strings.TrimLeft(anchorLine, " \t"))]
	targetIndent := visualWidth(anchorLeadWS) + anchorCol + 1

	// Process continuation lines.
	changed := false
	for i := anchorIdx + 1; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" {
			continue
		}

		// Check if this line starts with a binop.
		if !startsWithBinop(trimmed) {
			continue
		}

		// Compute desired leading whitespace (visual columns, not byte count).
		currentIndent := visualWidth(line[:len(line)-len(trimmed)])
		if currentIndent == targetIndent {
			continue
		}

		lines[i] = strings.Repeat(" ", targetIndent) + trimmed
		changed = true
	}

	if !changed {
		return body, false
	}

	return []byte(strings.Join(lines, "\n")), true
}

// findRelationAnchor returns the visual column of the first relation
// operator on a trimmed row at brace depth 0, or -1 if none found.
func findRelationAnchor(row string) int {
	depth := 0
	for i := 0; i < len(row); i++ {
		ch := row[i]
		switch {
		case ch == '{':
			depth++
		case ch == '}':
			if depth > 0 {
				depth--
			}
		case ch == '\\' && depth == 0:
			// Check for relation operator commands.
			for _, op := range relationOps {
				if op[0] != '\\' {
					continue
				}
				if strings.HasPrefix(row[i:], op) {
					// Ensure the command is complete (not a prefix of a longer command).
					endIdx := i + len(op)
					if endIdx < len(row) && isAlpha(row[endIdx]) {
						continue
					}
					return visualWidth(row[:i])
				}
			}
			// Skip other backslash sequences.
			if i+1 < len(row) && isAlpha(row[i+1]) {
				// Skip command name.
				j := i + 1
				for j < len(row) && isAlpha(row[j]) {
					j++
				}
				i = j - 1
			} else if i+1 < len(row) {
				i++ // skip escaped char
			}
		case depth == 0:
			// Check single-char relation operators.
			for _, op := range relationOps {
				if op[0] == '\\' {
					continue
				}
				if strings.HasPrefix(row[i:], op) {
					// For ":=" check that we match the full token.
					return visualWidth(row[:i])
				}
			}
		}
	}
	return -1
}

// containsAmpAtDepth0 reports whether s contains an & at brace depth 0.
func containsAmpAtDepth0(s string) bool {
	depth := 0
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch == '{':
			depth++
		case ch == '}':
			if depth > 0 {
				depth--
			}
		case ch == '&' && depth == 0:
			return true
		case ch == '\\' && i+1 < len(s):
			i++ // skip escaped char
		}
	}
	return false
}

// startsWithBinop reports whether trimmed (leading-whitespace-removed)
// content starts with a binary operator.
func startsWithBinop(trimmed string) bool {
	for _, op := range binopPrefixes {
		if strings.HasPrefix(trimmed, op) {
			if op[0] == '\\' {
				// For command operators, ensure complete match.
				endIdx := len(op)
				if endIdx < len(trimmed) && isAlpha(trimmed[endIdx]) {
					continue
				}
			}
			return true
		}
	}
	return false
}

// isAlpha is defined in rules_wrap.go and shared across the package.
