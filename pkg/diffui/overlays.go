package diffui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"mrevdiff/pkg/diffreview"
	"mrevdiff/pkg/parser"
)

// searchState drives the / search across pair sources and labels.
type searchState struct {
	// Typing is true while the query is being composed in the status line.
	Typing bool
	Input  string
	Query  string
	// Matches are indices into Review.Pairs, in review order.
	Matches []int
	Pos     int
}

// annListEntry is one row of the @ annotation-list overlay.
type annListEntry struct {
	PairID   string
	Detached bool
	File     string
	Line     int
	Note     string
}

type annListState struct {
	Entries []annListEntry
	Cursor  int
}

// startSearch opens the / prompt in the status line.
func (m Model) startSearch() (tea.Model, tea.Cmd) {
	m.Search = &searchState{Typing: true}
	m.Status = "/"
	return m, nil
}

// updateSearchInput consumes keys while the / query is being composed.
func (m Model) updateSearchInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := m.Search
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.Search = nil
		m.Status = "search cancelled"
		return m, nil
	case tea.KeyEnter:
		s.Typing = false
		s.Query = s.Input
		return m.runSearch()
	case tea.KeyBackspace:
		if len(s.Input) > 0 {
			r := []rune(s.Input)
			s.Input = string(r[:len(r)-1])
		}
	case tea.KeyRunes:
		s.Input += string(msg.Runes)
	case tea.KeySpace:
		s.Input += " "
	}
	m.Status = "/" + s.Input
	return m, nil
}

// pairMatchesQuery reports whether the pair's ID, labels, or either side's
// source contains the query (case-insensitive).
func pairMatchesQuery(pair *diffreview.Pair, query string) bool {
	if pair == nil {
		return false
	}
	q := strings.ToLower(query)
	if strings.Contains(strings.ToLower(pair.ID), q) {
		return true
	}
	for _, b := range []*parser.Block{pair.Old, pair.New} {
		if b == nil {
			continue
		}
		if strings.Contains(strings.ToLower(b.Label), q) {
			return true
		}
		if strings.Contains(strings.ToLower(b.Source), q) {
			return true
		}
	}
	return false
}

// runSearch computes the match list over the pairs visible under the
// current filter and jumps to the first match at or after the cursor.
func (m Model) runSearch() (tea.Model, tea.Cmd) {
	s := m.Search
	if s == nil || strings.TrimSpace(s.Query) == "" {
		m.Search = nil
		m.Status = "search cancelled"
		return m, nil
	}
	s.Matches = s.Matches[:0]
	if m.Review != nil {
		for _, idx := range m.visibleIndices() {
			if idx < 0 || idx >= len(m.Review.Pairs) {
				continue
			}
			if pairMatchesQuery(&m.Review.Pairs[idx], s.Query) {
				s.Matches = append(s.Matches, idx)
			}
		}
	}
	if len(s.Matches) == 0 {
		m.Status = fmt.Sprintf("no match for %q (filter: %s)", s.Query, m.Filter.String())
		return m, nil
	}
	// First match at or after the cursor, wrapping.
	s.Pos = 0
	for i, idx := range s.Matches {
		if idx >= m.Cursor {
			s.Pos = i
			break
		}
	}
	return m.jumpToMatch()
}

// nextMatch steps through the match list with n/N, wrapping.
func (m Model) nextMatch(delta int) (tea.Model, tea.Cmd) {
	s := m.Search
	if s == nil || len(s.Matches) == 0 {
		m.Status = "no active search — press / first"
		return m, nil
	}
	s.Pos = (s.Pos + delta + len(s.Matches)) % len(s.Matches)
	return m.jumpToMatch()
}

func (m Model) jumpToMatch() (tea.Model, tea.Cmd) {
	s := m.Search
	idx := s.Matches[s.Pos]
	m.Cursor = idx
	m.SourceLineCursor = 1
	m.snapCursor()
	pairID := ""
	if m.Review != nil && idx < len(m.Review.Pairs) {
		pairID = m.Review.Pairs[idx].ID
	}
	m.Status = fmt.Sprintf("match %d/%d: %s (n/N to step)", s.Pos+1, len(s.Matches), pairID)
	return m.withPDFRender()
}

// openAnnList builds and shows the @ annotation-list overlay.
func (m Model) openAnnList() (tea.Model, tea.Cmd) {
	side := m.ensureSidecar()
	entries := make([]annListEntry, 0, len(side.Annotations)+len(side.Detached))
	for _, a := range side.Annotations {
		entries = append(entries, annListEntry{
			PairID: a.PairID, File: a.File, Line: a.StartLine, Note: a.Note,
		})
	}
	for _, a := range side.Detached {
		entries = append(entries, annListEntry{
			PairID: a.PairID, File: a.File, Line: a.StartLine, Note: a.Note, Detached: true,
		})
	}
	if len(entries) == 0 {
		m.Status = "no annotations yet (a annotates the current pair)"
		return m, nil
	}
	m.AnnList = &annListState{Entries: entries}
	return m, nil
}

// updateAnnList consumes keys while the annotation list is open.
func (m Model) updateAnnList(key string) (tea.Model, tea.Cmd) {
	l := m.AnnList
	switch key {
	case "esc", "@", "q":
		m.AnnList = nil
		m.Status = ""
		return m, nil
	case "j", "down":
		if l.Cursor < len(l.Entries)-1 {
			l.Cursor++
		}
		return m, nil
	case "k", "up":
		if l.Cursor > 0 {
			l.Cursor--
		}
		return m, nil
	case "d":
		if l.Cursor >= len(l.Entries) {
			return m, nil
		}
		e := l.Entries[l.Cursor]
		side := m.ensureSidecar()
		if e.Detached {
			for i, a := range side.Detached {
				if a.PairID == e.PairID && a.Note == e.Note {
					side.Detached = append(side.Detached[:i], side.Detached[i+1:]...)
					break
				}
			}
		} else {
			side.DeleteAnnotation(e.PairID)
			delete(m.Annotations, e.PairID)
		}
		l.Entries = append(l.Entries[:l.Cursor], l.Entries[l.Cursor+1:]...)
		if l.Cursor >= len(l.Entries) && l.Cursor > 0 {
			l.Cursor--
		}
		if len(l.Entries) == 0 {
			m.AnnList = nil
			m.Status = "all annotations deleted"
		}
		return m, nil
	case "enter":
		if l.Cursor >= len(l.Entries) {
			return m, nil
		}
		e := l.Entries[l.Cursor]
		m.AnnList = nil
		if e.Detached {
			m.Status = "annotation is detached — its pair no longer exists"
			return m, nil
		}
		if idx := pairIndexByID(m.Review, e.PairID); idx >= 0 {
			m.Cursor = idx
			m.SourceLineCursor = 1
			m.snapCursor()
			m.Status = "jumped to " + e.PairID
			return m.withPDFRender()
		}
		m.Status = "pair not found: " + e.PairID
		return m, nil
	}
	return m, nil
}

// renderAnnListOverlay renders the @ overlay.
func (m Model) renderAnnListOverlay(width, bodyHeight int) string {
	l := m.AnnList
	innerW := width - 10
	if innerW < 30 {
		innerW = 30
	}
	lines := []string{"annotations — enter jumps · d deletes · esc closes", ""}
	for i, e := range l.Entries {
		marker := "  "
		if i == l.Cursor {
			marker = "> "
		}
		flag := ""
		if e.Detached {
			flag = " [detached]"
		}
		loc := e.PairID
		if e.File != "" && e.Line > 0 {
			loc = fmt.Sprintf("%s · %s:%d", e.PairID, e.File, e.Line)
		}
		note := strings.SplitN(e.Note, "\n", 2)[0]
		lines = append(lines, clipLine(fmt.Sprintf("%s%s%s", marker, loc, flag), innerW))
		lines = append(lines, clipLine("      "+note, innerW))
	}
	box := m.Styles.PaneFocused.
		Border(lipgloss.RoundedBorder()).
		Padding(1, 3).
		Render(strings.Join(lines, "\n"))
	return lipgloss.Place(width, bodyHeight, lipgloss.Center, lipgloss.Center, box)
}

// renderInfoOverlay renders the i review-scope popup.
func (m Model) renderInfoOverlay(width, bodyHeight int) string {
	innerW := width - 10
	if innerW < 30 {
		innerW = 30
	}
	var lines []string
	add := func(s string) { lines = append(lines, clipLine(s, innerW)) }
	add("mrevdiff — review scope")
	add("")
	if m.Review != nil {
		oldLine := "Old: " + m.Review.Old.Spec
		if m.Review.Old.ResolvedSHA != "" {
			sha := m.Review.Old.ResolvedSHA
			if len(sha) > 12 {
				sha = sha[:12]
			}
			oldLine += " @ " + sha
		}
		add(oldLine)
		add("New: " + m.Review.New.Spec)
		counts := map[diffreview.PairStatus]int{}
		for i := range m.Review.Pairs {
			counts[m.Review.Pairs[i].Status]++
		}
		add(fmt.Sprintf("Pairs: %d total — %d changed, %d format-only, %d added, %d deleted, %d moved, %d unchanged",
			len(m.Review.Pairs), counts[diffreview.Changed], counts[diffreview.FormatOnly],
			counts[diffreview.Added], counts[diffreview.Deleted], counts[diffreview.Moved], counts[diffreview.Unchanged]))
	}
	side := m.ensureSidecar()
	add(fmt.Sprintf("Annotations: %d (+%d detached), reviewed: %d", len(side.Annotations), len(side.Detached), len(m.Reviewed)))
	add("Sidecar: " + m.SidecarPath)
	edits := "read-only"
	if m.AllowModifications {
		edits = "e/E enabled"
	}
	add(fmt.Sprintf("Filter: %s · regime: %s · layout: %s · edits: %s",
		m.Filter.String(), m.DiffRegime.String(), layoutName(m.Layout), edits))
	if m.Description != "" {
		add("")
		add(strings.Repeat("─", min(innerW, 40)))
		for _, l := range strings.Split(strings.TrimSpace(m.Description), "\n") {
			for _, wrapped := range wrapPlainLine(l, innerW) {
				add(wrapped)
			}
		}
	}
	add("")
	add("(any key closes)")
	box := m.Styles.PaneFocused.
		Border(lipgloss.RoundedBorder()).
		Padding(1, 3).
		Render(strings.Join(lines, "\n"))
	return lipgloss.Place(width, bodyHeight, lipgloss.Center, lipgloss.Center, box)
}

// wrapPlainLine wraps an unstyled line at width, breaking on spaces where
// possible. Good enough for description prose.
func wrapPlainLine(s string, width int) []string {
	if width < 1 || len(s) <= width {
		return []string{s}
	}
	var out []string
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}
	cur := words[0]
	for _, w := range words[1:] {
		if len(cur)+1+len(w) <= width {
			cur += " " + w
			continue
		}
		out = append(out, cur)
		cur = w
	}
	out = append(out, cur)
	return out
}
