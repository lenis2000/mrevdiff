package format

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// ParseLineRange parses a "START:END" string (1-based, inclusive) into a
// [2]int{start, end}. Returns an error for malformed input or invalid ranges.
func ParseLineRange(s string) ([2]int, error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return [2]int{}, fmt.Errorf("invalid --lines format %q: expected START:END", s)
	}
	start, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return [2]int{}, fmt.Errorf("invalid --lines start %q: %w", parts[0], err)
	}
	end, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return [2]int{}, fmt.Errorf("invalid --lines end %q: %w", parts[1], err)
	}
	if start < 1 {
		return [2]int{}, fmt.Errorf("invalid --lines: start must be >= 1, got %d", start)
	}
	if end < start {
		return [2]int{}, fmt.Errorf("invalid --lines: end (%d) < start (%d)", end, start)
	}
	return [2]int{start, end}, nil
}

// ClipToRange takes the original source, the fully-formatted source, and the
// 1-based inclusive line range [start, end]. It returns a merged result where
// lines inside the range come from the formatted source and lines outside the
// range come from the original source.
//
// PRECONDITION: line-count-changing rules must be disabled so that the before
// and after sources have the same number of lines. If line counts differ,
// ClipToRange returns the original source unchanged and an error.
func ClipToRange(original, formatted []byte, lineRange [2]int) ([]byte, error) {
	origLines := splitKeepNL(original)
	fmtLines := splitKeepNL(formatted)

	if len(origLines) != len(fmtLines) {
		return original, fmt.Errorf(
			"line count mismatch (before=%d, after=%d); line-count-changing rules must be disabled under --lines",
			len(origLines), len(fmtLines))
	}

	start := lineRange[0] // 1-based
	end := lineRange[1]   // 1-based, inclusive

	// Clamp to file bounds.
	if start > len(origLines) {
		return original, nil // range is entirely past EOF; nothing to do
	}
	if end > len(origLines) {
		end = len(origLines)
	}

	var out []byte
	for i, origLine := range origLines {
		lineNum := i + 1 // 1-based
		if lineNum >= start && lineNum <= end {
			out = append(out, fmtLines[i]...)
		} else {
			out = append(out, origLine...)
		}
	}
	return out, nil
}

// splitKeepNL splits src into lines, keeping the trailing \n on each line.
// The last element may or may not end with \n depending on the input.
func splitKeepNL(src []byte) [][]byte {
	if len(src) == 0 {
		return nil
	}
	var lines [][]byte
	for len(src) > 0 {
		idx := bytes.IndexByte(src, '\n')
		if idx < 0 {
			lines = append(lines, src)
			break
		}
		lines = append(lines, src[:idx+1])
		src = src[idx+1:]
	}
	return lines
}
