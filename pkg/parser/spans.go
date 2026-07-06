package parser

import (
	"bytes"
	"sort"
)

// ProtectedSpan marks a byte range in the source that must not be rewritten by
// the format pass. The Start/End pair is a half-open interval [Start, End).
type ProtectedSpan struct {
	Start, End int
	Kind       string // "verbatim" | "lstlisting" | "comment-env" | "verb-inline" | "comment-line"
}

// ProtectedSpans scans src and returns every region that should be left
// untouched by source-rewriting rules: verbatim-like environments, inline
// \verb and \lstinline commands, and %-comment lines.
//
// The returned spans are sorted by Start and never overlap.
func ProtectedSpans(src []byte) []ProtectedSpan {
	return ProtectedSpansExtra(src, nil)
}

// ProtectedSpansExtra is ProtectedSpans plus a caller-supplied list of
// additional environment names whose bodies should be treated as verbatim
// (e.g. user-defined listing wrappers). Names are matched exactly.
func ProtectedSpansExtra(src []byte, extraEnvs []string) []ProtectedSpan {
	var spans []ProtectedSpan

	// Pass 1: per-line scan for %-comments and inline \verb / \lstinline.
	pos := 0
	for pos < len(src) {
		lineStart := pos
		lineEnd := nextNewline(src, pos)
		spans = appendLineSpans(spans, src, lineStart, lineEnd)
		if lineEnd < len(src) {
			pos = lineEnd + 1
		} else {
			pos = lineEnd
		}
	}

	// Build the env set for pass 2: defaults + extras.
	envs := skipEnvs
	if len(extraEnvs) > 0 {
		envs = make(map[string]bool, len(skipEnvs)+len(extraEnvs))
		for k, v := range skipEnvs {
			envs[k] = v
		}
		for _, e := range extraEnvs {
			envs[e] = true
		}
	}

	// Pass 2: multi-line skip-envs.
	spans = append(spans, scanSkipEnvsWith(src, spans, envs)...)
	sortSpans(spans)
	return spans
}

// LineOffsets returns the byte offset of the start of each 1-based line.
// offsets[0] is 0 (unused sentinel); offsets[1] is the start of line 1 (always 0).
// The length is numLines+1 where numLines counts each \n-terminated run as a
// line (a final line without \n is still counted).
func LineOffsets(src []byte) []int {
	offsets := []int{0, 0}
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			offsets = append(offsets, i+1)
		}
	}
	return offsets
}

// OverlapsProtected reports whether the half-open byte range [start, end)
// overlaps any span in the given sorted slice.
func OverlapsProtected(start, end int, spans []ProtectedSpan) bool {
	for _, sp := range spans {
		if sp.Start >= end {
			break
		}
		if sp.End > start {
			return true
		}
	}
	return false
}

// --- internal helpers --------------------------------------------------------

func nextNewline(src []byte, pos int) int {
	for i := pos; i < len(src); i++ {
		if src[i] == '\n' {
			return i
		}
	}
	return len(src)
}

// appendLineSpans scans one line [lineStart, lineEnd) for %-comments and
// inline \verb / \lstinline. Skip-envs are handled separately.
func appendLineSpans(spans []ProtectedSpan, src []byte, lineStart, lineEnd int) []ProtectedSpan {
	i := lineStart
	for i < lineEnd {
		switch c := src[i]; c {
		case '%':
			// Count consecutive backslashes before the %. If odd, the %
			// is escaped (\%); if even (including zero), the % starts a
			// real comment (e.g. \\% where \\ is a line break).
			nSlashes := 0
			for k := i - 1; k >= lineStart && src[k] == '\\'; k-- {
				nSlashes++
			}
			if nSlashes%2 == 1 {
				i++
				continue
			}
			spans = append(spans, ProtectedSpan{Start: i, End: lineEnd, Kind: "comment-line"})
			return spans
		case '\\':
			sp, end, ok := scanInlineVerb(src, i, lineEnd)
			if ok {
				spans = append(spans, sp)
				i = end
			} else {
				if i+1 < lineEnd {
					i += 2
				} else {
					i++
				}
			}
		default:
			i++
		}
	}
	return spans
}

// scanInlineVerb checks if src[pos:] starts with \verb, \verb*, or \lstinline
// and returns the protected span covering the entire command including its
// delimited argument.
func scanInlineVerb(src []byte, pos, lineEnd int) (ProtectedSpan, int, bool) {
	if pos+1 >= lineEnd || !isLetter(src[pos+1]) {
		return ProtectedSpan{}, 0, false
	}

	j := pos + 1
	for j < lineEnd && isLetter(src[j]) {
		j++
	}
	name := string(src[pos+1 : j])

	if j < lineEnd && src[j] == '*' {
		if name == "verb" {
			j++ // consume the '*'
		}
		// \lstinline* is not standard; ignore the star for other commands.
	}

	switch name {
	case "verb":
		// \verb<delim>...<delim>  (or \verb*<delim>...<delim> after consuming '*' above)
		// LaTeX \verb uses the same character as both opening and closing delimiter.
		if j >= lineEnd {
			return ProtectedSpan{}, 0, false
		}
		delim := src[j]
		k := j + 1
		for k < lineEnd && src[k] != delim {
			k++
		}
		if k < lineEnd {
			k++ // include closing delimiter
		}
		return ProtectedSpan{Start: pos, End: k, Kind: "verb-inline"}, k, true

	case "lstinline":
		if j >= lineEnd {
			return ProtectedSpan{}, 0, false
		}
		// \lstinline can use {…} (brace-balanced) or <delim>…<delim> like \verb.
		// It can also have an optional [...] argument before the content.
		switch src[j] {
		case '[':
			// Skip optional argument
			depth := 1
			j++
			for j < lineEnd && depth > 0 {
				switch src[j] {
				case '[':
					depth++
				case ']':
					depth--
				}
				j++
			}
			if j >= lineEnd {
				return ProtectedSpan{}, 0, false
			}
		}
		if src[j] == '{' {
			depth := 1
			k := j + 1
			for k < lineEnd && depth > 0 {
				switch src[k] {
				case '{':
					depth++
				case '}':
					depth--
				}
				k++
			}
			return ProtectedSpan{Start: pos, End: k, Kind: "verb-inline"}, k, true
		}
		delim := src[j]
		k := j + 1
		for k < lineEnd && src[k] != delim {
			k++
		}
		if k < lineEnd {
			k++
		}
		return ProtectedSpan{Start: pos, End: k, Kind: "verb-inline"}, k, true
	}

	return ProtectedSpan{}, 0, false
}

// scanSkipEnvsWith is scanSkipEnvs against a caller-supplied env set so the
// extras list from ProtectedSpansExtra is honoured.
func scanSkipEnvsWith(src []byte, existing []ProtectedSpan, envs map[string]bool) []ProtectedSpan {
	var spans []ProtectedSpan
	pos := 0
	for pos < len(src) {
		idx := bytes.Index(src[pos:], []byte(`\begin{`))
		if idx < 0 {
			break
		}
		beginPos := pos + idx
		nameStart := beginPos + 7 // len(`\begin{`)
		nameEnd := nameStart
		for nameEnd < len(src) && src[nameEnd] != '}' && src[nameEnd] != '\n' {
			nameEnd++
		}
		if nameEnd >= len(src) || src[nameEnd] != '}' {
			pos = nameStart
			continue
		}
		envName := string(src[nameStart:nameEnd])
		afterBegin := nameEnd + 1

		if !envs[envName] {
			pos = afterBegin
			continue
		}

		// Skip \begin markers that fall inside a comment line (pass-1 span).
		if OverlapsProtected(beginPos, afterBegin, existing) {
			pos = afterBegin
			continue
		}

		endMarker := []byte(`\end{` + envName + `}`)
		endIdx := bytes.Index(src[afterBegin:], endMarker)
		if endIdx < 0 {
			spans = append(spans, ProtectedSpan{
				Start: beginPos, End: len(src),
				Kind: spanKindForEnv(envName),
			})
			break
		}
		endPos := afterBegin + endIdx + len(endMarker)
		spans = append(spans, ProtectedSpan{
			Start: beginPos, End: endPos,
			Kind: spanKindForEnv(envName),
		})
		pos = endPos
	}
	return spans
}

func spanKindForEnv(env string) string {
	switch env {
	case "verbatim", "verbatim*", "Verbatim", "Verbatim*":
		return "verbatim"
	case "lstlisting", "minted":
		return "lstlisting"
	case "comment":
		return "comment-env"
	}
	return "verbatim"
}

func sortSpans(spans []ProtectedSpan) {
	sort.Slice(spans, func(i, j int) bool {
		return spans[i].Start < spans[j].Start
	})
}
