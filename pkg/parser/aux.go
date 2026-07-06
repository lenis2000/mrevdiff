package parser

import (
	"errors"
	"os"
	"strings"
)

// AuxEntry holds the data extracted from a single \newlabel line of a LaTeX
// .aux file. Only the fields used by the rest of the tool are materialised;
// the remaining positional arguments of \newlabel are currently discarded.
type AuxEntry struct {
	Label  string
	Number string
	Page   string
}

// LoadAux reads path and returns the \newlabel entries it declares. A
// missing file is treated as an empty aux (nil error) so the tool can run on
// sources that have never been compiled.
func LoadAux(path string) (map[string]AuxEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]AuxEntry{}, nil
		}
		return nil, err
	}
	return ParseAux(data), nil
}

// ParseAux parses the contents of a LaTeX .aux file and returns a map from
// label name to AuxEntry. Lines that do not match the expected shape are
// skipped silently — .aux files contain many other record types (hyperref,
// bibliography, cref internals) that we do not model.
func ParseAux(data []byte) map[string]AuxEntry {
	out := map[string]AuxEntry{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, `\newlabel{`) {
			continue
		}
		rest := strings.TrimPrefix(line, `\newlabel`)
		label, rest, ok := readBracedGroup(rest)
		if !ok || label == "" {
			continue
		}
		if suffixedLabel(label) {
			continue
		}
		arg, _, ok := readBracedGroup(rest)
		if !ok {
			continue
		}
		num, arg2, ok := readBracedGroup(arg)
		if !ok {
			out[label] = AuxEntry{Label: label}
			continue
		}
		page, _, _ := readBracedGroup(arg2)
		out[label] = AuxEntry{Label: label, Number: num, Page: page}
	}
	return out
}

// ApplyAux copies Number from matching AuxEntry into each labeled Block.
// Returns the number of blocks that received a value.
func ApplyAux(doc *Document, entries map[string]AuxEntry) int {
	if doc == nil || len(entries) == 0 {
		return 0
	}
	n := 0
	for _, b := range doc.Blocks {
		if b == doc.Root || b.Label == "" {
			continue
		}
		if e, ok := entries[b.Label]; ok && e.Number != "" {
			b.Number = e.Number
			n++
		}
	}
	return n
}

// suffixedLabel reports whether label is an internal \newlabel record that
// should not shadow the user-visible label (for example "foo@cref" or
// "foo.sub@cref" produced by the cleveref package). We keep only the plain
// entry whose key has no "@" suffix.
func suffixedLabel(label string) bool {
	return strings.Contains(label, "@")
}

// readBracedGroup reads a balanced {...} group at the beginning of s (after
// optional leading spaces/tabs). Escaped braces (`\{`, `\}`) are ignored for
// depth tracking. Returns the content without the outer braces, the
// remainder of s after the closing brace, and true if a group was consumed.
func readBracedGroup(s string) (string, string, bool) {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	if i >= len(s) || s[i] != '{' {
		return "", s, false
	}
	depth := 1
	start := i + 1
	j := start
	for j < len(s) {
		c := s[j]
		if c == '\\' && j+1 < len(s) {
			j += 2
			continue
		}
		switch c {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start:j], s[j+1:], true
			}
		}
		j++
	}
	return "", s, false
}

// readBracketGroup reads a balanced [...] group at the beginning of s.
// Same semantics as readBracedGroup but with square brackets.
func readBracketGroup(s string) (string, string, bool) {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	if i >= len(s) || s[i] != '[' {
		return "", s, false
	}
	depth := 1
	start := i + 1
	j := start
	for j < len(s) {
		c := s[j]
		if c == '\\' && j+1 < len(s) {
			j += 2
			continue
		}
		switch c {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return s[start:j], s[j+1:], true
			}
		}
		j++
	}
	return "", s, false
}
