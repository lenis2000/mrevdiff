package format

import (
	"bytes"
	"regexp"
)

// skipDirectiveRe matches the directive payload after the comment marker(s).
// The directive must occupy the rest of the line. Match is case-insensitive
// on the directive name and keyword.
var skipDirectiveRe = regexp.MustCompile(`(?i)\bmrevdiff-fmt:\s+(skip|off|on)\s*$`)

// BuildSkipMask scans src and returns a 1-indexed mask of length numLines+1
// (mask[0] unused). mask[L] == true means rules and diagnostics must leave
// line L untouched.
//
// Directives, on a comment line or trailing comment:
//
//	% mrevdiff-fmt: skip   — masks just this line
//	% mrevdiff-fmt: off    — starts a masked block (this line included)
//	% mrevdiff-fmt: on     — ends a masked block (this line included)
//
// The directive is only honoured when the comment starts with an unescaped
// '%' (so `\%` does not trigger). A `skip` inside an `off…on` block is a
// no-op (already masked).
//
// In addition, the preamble (everything from line 1 through the line that
// contains \begin{document}, inclusive) is always masked. The preamble is
// hand-curated by authors — package loads, macro definitions, class options,
// theorem setup — and reflow has no way to know which whitespace is load-
// bearing. If \begin{document} is absent (e.g. an \input-included fragment),
// nothing is masked on that basis.
func BuildSkipMask(src []byte) []bool {
	// Count lines: a final line without trailing \n is still a line.
	nLines := 1
	for _, b := range src {
		if b == '\n' {
			nLines++
		}
	}
	mask := make([]bool, nLines+1) // 1-indexed; mask[0] unused

	inBlock := false
	preambleEnd := 0 // 0 = no \begin{document} seen yet
	line := 1
	lineStart := 0
	for i := 0; i <= len(src); i++ {
		if i == len(src) || src[i] == '\n' {
			// process [lineStart, i)
			body := src[lineStart:i]
			if preambleEnd == 0 && hasBeginDocument(body) {
				preambleEnd = line
			}
			kind, ok := parseDirective(body)
			switch {
			case ok && kind == "off":
				inBlock = true
				mask[line] = true
			case ok && kind == "on":
				mask[line] = true
				inBlock = false
			case ok && kind == "skip":
				mask[line] = true
			default:
				if inBlock {
					mask[line] = true
				}
			}
			line++
			lineStart = i + 1
		}
	}
	for L := 1; L <= preambleEnd; L++ {
		mask[L] = true
	}
	return mask
}

// hasBeginDocument reports whether the line contains \begin{document} before
// any unescaped '%' (i.e. not inside a comment).
func hasBeginDocument(line []byte) bool {
	idx := bytes.Index(line, []byte(`\begin{document}`))
	if idx < 0 {
		return false
	}
	// Reject if there is an unescaped '%' before idx.
	for j := 0; j < idx; j++ {
		if line[j] != '%' {
			continue
		}
		n := 0
		for k := j - 1; k >= 0 && line[k] == '\\'; k-- {
			n++
		}
		if n%2 == 0 {
			return false
		}
	}
	return true
}

// parseDirective extracts a directive keyword (skip/off/on) from one line of
// source if the line carries a directive comment. Returns ("", false) if
// there is no directive on this line.
func parseDirective(line []byte) (string, bool) {
	// Locate the first unescaped '%' on the line.
	pct := -1
	for j := 0; j < len(line); j++ {
		if line[j] != '%' {
			continue
		}
		// Count consecutive '\\' before j.
		n := 0
		for k := j - 1; k >= 0 && line[k] == '\\'; k-- {
			n++
		}
		if n%2 == 0 {
			pct = j
			break
		}
	}
	if pct < 0 {
		return "", false
	}
	// Skip leading '%' and whitespace from the comment body.
	body := line[pct+1:]
	body = bytes.TrimLeft(body, "% \t")
	m := skipDirectiveRe.FindSubmatch(body)
	if m == nil {
		// Also allow the whole comment body to be matched even if tail
		// punctuation appears — but the spec says the directive must end
		// the line. The regex's `$` anchor handles that already.
		return "", false
	}
	return string(bytes.ToLower(m[1])), true
}
