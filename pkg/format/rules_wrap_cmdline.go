package format

import (
	"bytes"
)

// computeCommandOnlyMask returns a 1-indexed mask of length nLines+2.
// mask[L] == true iff line L belongs to a "command-only paragraph" — a
// line at indent 0 whose content is one or more complete \name[opt]?{...}*
// invocations (possibly followed by a trailing comment), or a continuation
// line inside such an invocation's still-open brace.
//
// Math-environment lines are excluded from being a NEW command-only-paragraph
// start (math content is handled by the math rules). Continuation lines are
// still marked even if their interior opens a math environment, because the
// outer command (e.g. \caption{...}) is still hand-laid layout.
func computeCommandOnlyMask(ctx *Ctx, nLines int) []bool {
	if nLines < 1 {
		return nil
	}
	src := ctx.Src

	depthAtStart := make([]int, nLines+2)
	mathAtStart := make([]int, nLines+2)

	depth := 0
	math := 0
	line := 1
	depthAtStart[1] = 0
	mathAtStart[1] = 0

	spIdx := 0
	beginPrefix := []byte(`\begin{`)
	endPrefix := []byte(`\end{`)

	i := 0
	for i < len(src) {
		// Advance past any protected span we've entered.
		for spIdx < len(ctx.Protected) && ctx.Protected[spIdx].End <= i {
			spIdx++
		}
		if spIdx < len(ctx.Protected) {
			sp := ctx.Protected[spIdx]
			if sp.Start <= i && i < sp.End {
				// Walk to end of span, counting newlines so per-line state
				// remains accurate.
				for i < sp.End && i < len(src) {
					if src[i] == '\n' {
						line++
						if line <= nLines+1 {
							depthAtStart[line] = depth
							mathAtStart[line] = math
						}
					}
					i++
				}
				continue
			}
		}

		c := src[i]
		switch c {
		case '\n':
			line++
			if line <= nLines+1 {
				depthAtStart[line] = depth
				mathAtStart[line] = math
			}
			i++
		case '\\':
			if bytes.HasPrefix(src[i:], beginPrefix) {
				j := i + len(beginPrefix)
				end := -1
				for k := j; k < len(src); k++ {
					if src[k] == '}' {
						end = k
						break
					}
					if src[k] == '\n' {
						break
					}
				}
				if end >= 0 {
					if isMathEnvName(src[j:end]) {
						math++
					}
					i = end + 1
					continue
				}
			}
			if bytes.HasPrefix(src[i:], endPrefix) {
				j := i + len(endPrefix)
				end := -1
				for k := j; k < len(src); k++ {
					if src[k] == '}' {
						end = k
						break
					}
					if src[k] == '\n' {
						break
					}
				}
				if end >= 0 {
					if isMathEnvName(src[j:end]) && math > 0 {
						math--
					}
					i = end + 1
					continue
				}
			}
			// Generic escape: skip the backslash and the following byte
			// (but never cross a newline boundary).
			if i+1 < len(src) && src[i+1] != '\n' {
				i += 2
			} else {
				i++
			}
		case '%':
			// Comment to end of line. The newline itself is consumed by
			// the case '\n' branch on the next iteration.
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case '{':
			depth++
			i++
		case '}':
			depth--
			i++
		default:
			i++
		}
	}

	// Pass 2: walk lines, mark command-only-paragraph members.
	mask := make([]bool, nLines+2)
	inPara := false
	paraStartDepth := 0
	for L := 1; L <= nLines; L++ {
		if inPara {
			if depthAtStart[L] > paraStartDepth {
				mask[L] = true
				continue
			}
			inPara = false
		}
		if mathAtStart[L] > 0 {
			continue
		}
		if depthAtStart[L] != 0 {
			continue
		}
		body := lineBytes(ctx, L)
		leadLen, allWS := leadingWS(body)
		if allWS {
			continue
		}
		if leadLen != 0 {
			continue
		}
		ok, openDepth := parseCommandChainOnLine(string(body))
		if !ok {
			continue
		}
		mask[L] = true
		if openDepth > 0 {
			inPara = true
			paraStartDepth = 0
		}
	}
	return mask
}

// isMathEnvName reports whether name is one of the LaTeX environments whose
// body is math (and thus excluded from cmd-only-paragraph detection).
func isMathEnvName(name []byte) bool {
	switch string(name) {
	case "equation", "equation*",
		"align", "align*",
		"gather", "gather*",
		"multline", "multline*",
		"eqnarray", "eqnarray*",
		"alignat", "alignat*",
		"flalign", "flalign*",
		"split",
		"displaymath", "math",
		"dmath", "dmath*",
		"dgroup", "dgroup*":
		return true
	}
	return false
}

// parseCommandChainOnLine reports whether content (a single source line,
// without trailing newline) consists entirely of one or more complete
// "\name [opts]? {...}*" invocations, separated by whitespace, optionally
// followed by a trailing '%' comment. openDepth is the unclosed brace
// nesting at end-of-content (>0 means the final command's brace continues
// onto the next line — the line is still command-only with continuation).
func parseCommandChainOnLine(content string) (ok bool, openDepth int) {
	i := 0
	advanced := false
	for i < len(content) {
		// skip horizontal whitespace
		for i < len(content) && (content[i] == ' ' || content[i] == '\t') {
			i++
		}
		if i >= len(content) {
			break
		}
		// trailing comment ends the chain successfully
		if content[i] == '%' {
			return advanced, 0
		}
		// must begin a new command
		if content[i] != '\\' {
			return false, 0
		}
		i++
		nameStart := i
		for i < len(content) && isAlpha(content[i]) {
			i++
		}
		if i == nameStart {
			// not a name (e.g. \\, \{, \[, \%, \,, \-) — bail
			return false, 0
		}
		// optional [..] arguments — any number, balanced
		for i < len(content) && content[i] == '[' {
			d := 1
			i++
			for i < len(content) && d > 0 {
				switch content[i] {
				case '\\':
					if i+1 < len(content) {
						i += 2
						continue
					}
				case '[':
					d++
				case ']':
					d--
				}
				i++
			}
			if d > 0 {
				return false, 0
			}
		}
		// optional starred form: \name*
		if i < len(content) && content[i] == '*' {
			i++
		}
		// zero or more {..} arguments
		for i < len(content) && content[i] == '{' {
			d := 1
			i++
			for i < len(content) && d > 0 {
				switch content[i] {
				case '\\':
					if i+1 < len(content) {
						i += 2
						continue
					}
				case '{':
					d++
				case '}':
					d--
				case '%':
					// comment inside brace: rest of line is comment, brace
					// stays open across the newline.
					return true, d
				}
				i++
			}
			if d > 0 {
				return true, d
			}
		}
		advanced = true
	}
	if !advanced {
		return false, 0
	}
	return true, 0
}
