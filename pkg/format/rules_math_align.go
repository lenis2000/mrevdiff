package format

import (
	"bytes"
	"strings"

	"github.com/lenis2000/mrevdiff/pkg/parser"
)

// MathAlignOptions controls the math.align-columns rule.
type MathAlignOptions struct {
	// Enabled gates the whole pass; when false, applyMathAlign is a no-op.
	Enabled bool
	// Envs is the set of environment names to align. If empty, the default
	// set (defaultAlignEnvs) is used.
	Envs []string
	// Skip is a set of environment names to never align, even if they
	// appear in Envs. Useful for user overrides.
	Skip []string
}

// defaultAlignEnvs is the built-in list of environments whose & columns
// are aligned by the math.align-columns rule.
var defaultAlignEnvs = map[string]bool{
	"align":    true,
	"align*":   true,
	"alignat":  true,
	"alignat*": true,
	"aligned":  true,
	"array":    true,
	"matrix":   true,
	"pmatrix":  true,
	"bmatrix":  true,
	"vmatrix":  true,
	"Vmatrix":  true,
	"Bmatrix":  true,
	"cases":    true,
	"tabular":  true,
	"tabular*": true,
	"tabularx": true,
}

func registerMathAlignRule() {
	Registry = append(Registry, Rule{
		ID:    "math.align-columns",
		Tier:  Safe,
		Doc:   "Align & columns in math/tabular environments.",
		Apply: applyMathAlign,
	})
}

// applyMathAlign finds aligned environments (align, matrix, tabular, etc.)
// and pads cells so that & columns line up across rows.
func applyMathAlign(ctx *Ctx) Result {
	if !ctx.MathAlign.Enabled {
		return Result{Src: ctx.Src}
	}

	envSet := buildEnvSet(ctx.MathAlign)
	skipSet := make(map[string]bool, len(ctx.MathAlign.Skip))
	for _, s := range ctx.MathAlign.Skip {
		skipSet[s] = true
	}

	// Find all top-level aligned environments by scanning tokens.
	type envSpan struct {
		name      string
		bodyStart int // byte offset of first byte after \begin{name}[opt]
		bodyEnd   int // byte offset of first byte of \end{name}
		beginLine int // 1-based line of \begin
	}

	var spans []envSpan
	// Track nesting to avoid processing nested aligned envs.
	// We only process top-level (depth==0) occurrences.
	depth := 0
	for _, tk := range ctx.Tokens {
		switch tk.Kind {
		case parser.TokBeginEnv:
			if envSet[tk.EnvName] && !skipSet[tk.EnvName] {
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
			if envSet[tk.EnvName] && !skipSet[tk.EnvName] {
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

	// Process spans in reverse order so byte offsets remain valid.
	// We copy sub-slices of ctx.Src before using them as append targets
	// to avoid corrupting ctx.Src through shared backing arrays.
	out := make([]byte, 0, len(ctx.Src))
	changed := false
	var hits []Hit
	var diags []Diag

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

		aligned, diag, ok := alignBody(body, sp.name, envSet)
		if !ok {
			// Refusal case: emit as Tier-3 note via a Diag.
			if diag != "" {
				diags = append(diags, Diag{
					RuleID:  "math.align-columns",
					Line:    sp.beginLine,
					Message: diag,
				})
			}
			// Prepend unchanged span + tail.
			tail := append([]byte(nil), ctx.Src[sp.bodyEnd:prev]...)
			out = append(tail, out...)
			bodyCP := append([]byte(nil), body...)
			out = append(bodyCP, out...)
			prev = sp.bodyStart
			continue
		}

		if string(aligned) != string(body) {
			changed = true
			hits = append(hits, Hit{
				RuleID:  "math.align-columns",
				Line:    sp.beginLine,
				Excerpt: truncExcerpt("aligned " + sp.name),
			})
			tail := append([]byte(nil), ctx.Src[sp.bodyEnd:prev]...)
			out = append(tail, out...)
			out = append(aligned, out...)
		} else {
			tail := append([]byte(nil), ctx.Src[sp.bodyEnd:prev]...)
			out = append(tail, out...)
			bodyCP := append([]byte(nil), body...)
			out = append(bodyCP, out...)
		}
		prev = sp.bodyStart
	}

	if !changed {
		return Result{Src: ctx.Src, Diags: diags}
	}

	// Prepend everything before the first span.
	prefix := append([]byte(nil), ctx.Src[:prev]...)
	out = append(prefix, out...)
	return Result{Src: out, Hits: hits, Diags: diags}
}

// buildEnvSet constructs the set of env names to align from MathAlignOptions.
func buildEnvSet(opts MathAlignOptions) map[string]bool {
	if len(opts.Envs) > 0 {
		m := make(map[string]bool, len(opts.Envs))
		for _, e := range opts.Envs {
			m[e] = true
		}
		return m
	}
	return defaultAlignEnvs
}

// alignBody attempts to align the & columns in the body of a math/tabular
// environment. Returns the aligned body, a diagnostic message (for refusal),
// and ok=true if alignment succeeded.
//
// Refusal cases: body contains % line comments, nested aligned envs, or
// rows have unequal cell counts.
func alignBody(body []byte, envName string, envSet map[string]bool) ([]byte, string, bool) {
	// Check for nested aligned envs.
	if containsNestedAlignedEnv(body, envSet) {
		return nil, "skip: nested aligned env", false
	}

	// Strip leading/trailing whitespace (typically a newline after
	// \begin{...} and before \end{...}). We'll restore them.
	// The suffix may include indentation whitespace before \end{...}
	// (added by the space.indent rule which runs earlier in the pipeline).
	s := string(body)
	prefix := ""
	if len(s) > 0 && s[0] == '\n' {
		prefix = "\n"
		s = s[1:]
	}
	// Strip trailing whitespace + newline. The body may end with
	// "\n<indent>" where <indent> is the whitespace before \end{env}.
	suffix := ""
	trimmedRight := strings.TrimRight(s, " \t")
	if len(trimmedRight) < len(s) && len(trimmedRight) > 0 && trimmedRight[len(trimmedRight)-1] == '\n' {
		// Body ends with \n followed by spaces/tabs (indent before \end).
		suffix = s[len(trimmedRight)-1:] // capture \n + trailing ws
		s = trimmedRight[:len(trimmedRight)-1]
	} else if len(s) > 0 && s[len(s)-1] == '\n' {
		suffix = "\n"
		s = s[:len(s)-1]
	}

	// Parse rows by splitting on \\ at brace depth 0.
	rows := splitRows([]byte(s))
	if len(rows) == 0 {
		return body, "", true
	}

	// Check for % line comments (outside braces) in any row.
	for _, row := range rows {
		if hasLineComment(row.content) {
			return nil, "skip: line comment in row", false
		}
	}

	// Parse cells per row by splitting on & at brace depth 0.
	type parsedRow struct {
		cells  []string
		suffix string // the \\ and anything after it (e.g., [2pt])
	}
	var parsed []parsedRow
	maxCols := 0
	rowsWithAmp := 0

	for _, row := range rows {
		cells := splitCells(row.content)
		if len(cells) > maxCols {
			maxCols = len(cells)
		}
		if len(cells) > 1 {
			rowsWithAmp++
		}
		parsed = append(parsed, parsedRow{
			cells:  cells,
			suffix: row.suffix,
		})
	}

	if maxCols <= 1 {
		// No & to align.
		return body, "", true
	}

	if rowsWithAmp < 2 {
		// Only one row has &; nothing to align across rows.
		return body, "", true
	}

	// Check for unequal cell counts across rows that have cells.
	for _, pr := range parsed {
		if len(pr.cells) > 1 && len(pr.cells) != maxCols {
			return nil, "skip: unequal cell counts", false
		}
	}

	// Compute the common leading whitespace across rows with & columns.
	// This preserves the indentation set by the space.indent rule (which
	// runs before math.align-columns in the pipeline).
	commonIndent := ""
	first := true
	for _, pr := range parsed {
		if len(pr.cells) <= 1 {
			continue
		}
		// The first cell's leading whitespace represents the row indent.
		cell0 := pr.cells[0]
		ws := cell0[:len(cell0)-len(strings.TrimLeft(cell0, " \t"))]
		if first || len(ws) < len(commonIndent) {
			commonIndent = ws
			first = false
		}
	}

	// Compute column widths. For each column except the last, measure
	// the trimmed cell content width. The alignment convention is:
	//   {cell0_trimmed}{spaces} &{cell1_content}
	// where the total width of cell0_trimmed + spaces equals maxWidth,
	// then a space before & (included in the padding), then &, then
	// the next cell content.
	//
	// For LaTeX align environments, the convention is:
	//   a   &= b \\
	//   foo &= bar
	// Cell 0 is right-padded to the max width of column 0, then " &",
	// then cell 1 content preserving its leading character (typically =).
	colWidths := make([]int, maxCols)
	for _, pr := range parsed {
		for c := 0; c < len(pr.cells); c++ {
			trimmed := strings.TrimSpace(pr.cells[c])
			w := visualWidth(trimmed)
			if w > colWidths[c] {
				colWidths[c] = w
			}
		}
	}

	// Rebuild the body with aligned cells.
	var buf bytes.Buffer
	for _, pr := range parsed {
		if len(pr.cells) <= 1 {
			// Row without &; emit as-is.
			buf.WriteString(pr.cells[0])
			buf.WriteString(pr.suffix)
			continue
		}
		// Prepend the common indent preserved from the original rows.
		buf.WriteString(commonIndent)
		for ci, cell := range pr.cells {
			if ci > 0 {
				// Emit " &" separator, then preserve the leading
				// space convention: if the original cell started
				// with a space (e.g., " 2" in matrices), emit
				// " & "; if it started with a non-space (e.g.,
				// "= b" in align), emit " &" (no extra space).
				buf.WriteString(" &")
				trimmedLeft := strings.TrimLeft(cell, " \t")
				if len(trimmedLeft) < len(cell) {
					buf.WriteByte(' ')
				}
			}
			trimmed := strings.TrimSpace(cell)
			if ci < len(pr.cells)-1 {
				// Non-last column: pad to max width.
				pad := colWidths[ci] - visualWidth(trimmed)
				if pad < 0 {
					pad = 0
				}
				buf.WriteString(trimmed)
				buf.WriteString(strings.Repeat(" ", pad))
			} else {
				// Last column: no padding needed.
				buf.WriteString(trimmed)
			}
		}
		buf.WriteString(pr.suffix)
	}

	result := make([]byte, 0, len(prefix)+buf.Len()+len(suffix))
	result = append(result, prefix...)
	result = append(result, buf.Bytes()...)
	result = append(result, suffix...)
	return result, "", true
}

// rowPiece holds a single row's content and its suffix (the \\ delimiter
// and any trailing content like [2pt] or newline).
type rowPiece struct {
	content string // cell content before \\
	suffix  string // \\ and any trailing [skip] + newline
}

// splitRows splits the body of a math environment into rows at \\ at brace
// depth 0. The last row may not have a \\ suffix.
func splitRows(body []byte) []rowPiece {
	s := string(body)
	var rows []rowPiece
	depth := 0
	rowStart := 0

	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch == '{':
			depth++
		case ch == '}':
			if depth > 0 {
				depth--
			}
		case ch == '\\' && depth == 0 && i+1 < len(s) && s[i+1] == '\\':
			// Found \\ at depth 0.
			// Walk back over spaces/tabs before the \\ to include them in the suffix.
			suffixStart := i
			for suffixStart > rowStart && (s[suffixStart-1] == ' ' || s[suffixStart-1] == '\t') {
				suffixStart--
			}
			content := s[rowStart:suffixStart]
			suffixStart2 := suffixStart // actual suffix start (with leading whitespace)
			i += 2                      // skip \\
			// Skip optional * (starred row break, inhibits page break).
			if i < len(s) && s[i] == '*' {
				i++
			}
			// Skip optional [skip] argument.
			if i < len(s) && s[i] == '[' {
				for i < len(s) && s[i] != ']' {
					i++
				}
				if i < len(s) {
					i++ // skip ]
				}
			}
			// Skip trailing whitespace and newline.
			for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
				i++
			}
			if i < len(s) && s[i] == '\n' {
				i++
			}
			suffix := s[suffixStart2:i]
			rows = append(rows, rowPiece{content: content, suffix: suffix})
			rowStart = i
			i-- // loop will increment
		case ch == '\\' && i+1 < len(s):
			// Skip escaped character (but not \\).
			i++
		}
	}

	// Remaining content after the last \\ (the final row, which may not
	// have a \\ terminator).
	if rowStart < len(s) {
		remaining := s[rowStart:]
		trimmed := strings.TrimRight(remaining, " \t\n")
		if trimmed != "" {
			rows = append(rows, rowPiece{content: remaining, suffix: ""})
		}
	}

	return rows
}

// splitCells splits a row's content on & at brace depth 0.
func splitCells(content string) []string {
	var cells []string
	depth := 0
	cellStart := 0

	for i := 0; i < len(content); i++ {
		ch := content[i]
		switch {
		case ch == '{':
			depth++
		case ch == '}':
			if depth > 0 {
				depth--
			}
		case ch == '&' && depth == 0:
			cells = append(cells, content[cellStart:i])
			cellStart = i + 1
		case ch == '\\' && i+1 < len(content):
			i++ // skip escaped char
		}
	}
	cells = append(cells, content[cellStart:])
	return cells
}

// containsNestedAlignedEnv reports whether body contains a \begin{env}
// for any env in envSet.
func containsNestedAlignedEnv(body []byte, envSet map[string]bool) bool {
	s := string(body)
	idx := 0
	for idx < len(s) {
		pos := strings.Index(s[idx:], `\begin{`)
		if pos < 0 {
			break
		}
		pos += idx
		braceStart := pos + 7 // position after \begin{
		braceEnd := strings.IndexByte(s[braceStart:], '}')
		if braceEnd < 0 {
			break
		}
		envName := s[braceStart : braceStart+braceEnd]
		if envSet[envName] {
			return true
		}
		idx = braceStart + braceEnd + 1
	}
	return false
}

// hasLineComment reports whether content contains a % that is not escaped
// (i.e., not preceded by an odd number of backslashes) and not inside braces.
// This is a conservative check: any line-comment presence causes refusal.
func hasLineComment(content string) bool {
	depth := 0
	for i := 0; i < len(content); i++ {
		ch := content[i]
		switch {
		case ch == '\\' && i+1 < len(content):
			i++ // skip escaped char (handles \\, \%, \{, etc.)
		case ch == '{':
			depth++
		case ch == '}':
			if depth > 0 {
				depth--
			}
		case ch == '%' && depth == 0:
			// Any % reaching here is unescaped (the \\ case above already
			// consumed backslash-escaped characters like \%).
			return true
		}
	}
	return false
}

// delimEndAfterBeginWithArgs is like delimEndAfterBegin but also skips
// mandatory {arg} arguments that follow the environment name (e.g.,
// \begin{tabular}{ll} or \begin{array}{ccc}). This ensures the body
// starts after all arguments.
func delimEndAfterBeginWithArgs(src []byte, start int) int {
	pos := delimEndAfterBegin(src, start)
	if pos < 0 {
		return -1
	}
	// Skip additional {arg} groups on the same line.
	for pos < len(src) && src[pos] == '{' {
		end := skipBalanced(src, pos, '{', '}')
		if end < 0 {
			break
		}
		pos = end
	}
	return pos
}

// NOTE: visualWidth is defined in rules_wrap.go and shared across the
// package. It returns the visual column width of s.
