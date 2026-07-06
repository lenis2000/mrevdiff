package parser

import (
	"errors"
	"os"
	"regexp"
	"strings"
)

// BibEntry is a parsed \bibitem from a .bbl file.
//
// Text holds the full body of the entry with leading/trailing whitespace
// trimmed but otherwise unmodified. Authors and Title are best-effort
// heuristics that downstream UI (Task 16's bib popup) can lean on.
type BibEntry struct {
	Key     string
	Display string
	Text    string
	Authors string
	Title   string
}

// LoadBBL reads path and returns the \bibitem entries it declares. A
// missing file is treated as an empty bibliography (nil slice + nil error).
func LoadBBL(path string) ([]BibEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return ParseBBL(data), nil
}

// ParseBBL extracts \bibitem entries from the contents of a .bbl file.
// Lines after \end{thebibliography} are ignored.
func ParseBBL(data []byte) []BibEntry {
	src := string(data)
	if idx := strings.Index(src, `\end{thebibliography}`); idx >= 0 {
		src = src[:idx]
	}
	parts := strings.Split(src, `\bibitem`)
	if len(parts) <= 1 {
		return nil
	}
	var entries []BibEntry
	for _, chunk := range parts[1:] {
		rest := chunk
		var display string
		if d, r, ok := readBracketGroup(rest); ok {
			display = d
			rest = r
		}
		key, body, ok := readBracedGroup(rest)
		if !ok || key == "" {
			continue
		}
		text := strings.TrimSpace(body)
		authors, title := extractAuthorsTitle(text)
		entries = append(entries, BibEntry{
			Key:     key,
			Display: display,
			Text:    text,
			Authors: authors,
			Title:   title,
		})
	}
	return entries
}

// ApplyBBL stores entries on doc and resolves outgoing cite refs whose
// Target matches a bib entry's Key. Returns the number of bib-entry child
// blocks appended to the bibliography wrapper (0 if no wrapper exists).
func ApplyBBL(doc *Document, entries []BibEntry) int {
	if doc == nil {
		return 0
	}
	if doc.BibEntries == nil {
		doc.BibEntries = map[string]*BibEntry{}
	}
	for i := range entries {
		e := entries[i]
		doc.BibEntries[e.Key] = &e
	}

	// Re-resolve cite refs using the newly known bib keys.
	for _, b := range doc.Blocks {
		for i := range b.RefsOut {
			r := &b.RefsOut[i]
			if r.Kind != "cite" {
				continue
			}
			if _, ok := doc.BibEntries[r.Target]; ok {
				r.Resolved = true
			}
		}
	}

	// Find the (first) bibliography wrapper and append one child per entry.
	var wrapper *Block
	for _, b := range doc.Blocks {
		if b.Kind == KindBibliography && b.EnvName == "thebibliography" {
			wrapper = b
			break
		}
	}
	if wrapper == nil {
		return 0
	}
	added := 0
	for _, e := range entries {
		if _, exists := doc.ByLabel[e.Key]; exists {
			continue
		}
		entryID := wrapper.ID + ".bib." + e.Key
		child := &Block{
			ID:        entryID,
			Kind:      KindBibliography,
			ParentID:  wrapper.ID,
			Label:     e.Key,
			Title:     firstNonEmpty(e.Title, e.Authors),
			Source:    e.Text,
			StartLine: wrapper.StartLine,
			EndLine:   wrapper.EndLine,
		}
		doc.Blocks = append(doc.Blocks, child)
		doc.ByID[child.ID] = child
		doc.ByLabel[e.Key] = child
		wrapper.ChildIDs = append(wrapper.ChildIDs, child.ID)
		added++
	}
	return added
}

var emTitleRE = regexp.MustCompile(`\\newblock\s*\{\\em\s+([^{}]*)\}`)

// extractAuthorsTitle returns a best-effort (authors, title) split from a
// bbl entry body. Authors = first non-empty line before any \newblock;
// Title = content of the first `\newblock {\em …}` if present.
func extractAuthorsTitle(text string) (string, string) {
	authors := ""
	before := text
	if idx := strings.Index(text, `\newblock`); idx >= 0 {
		before = text[:idx]
	}
	for _, line := range strings.Split(before, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.TrimSuffix(line, ".")
		authors = line
		break
	}
	title := ""
	if m := emTitleRE.FindStringSubmatch(text); len(m) == 2 {
		title = strings.TrimSpace(m[1])
		title = strings.TrimSuffix(title, ".")
	}
	return authors, title
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
