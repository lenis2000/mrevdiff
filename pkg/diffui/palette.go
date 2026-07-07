package diffui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// paletteItem is one command row in the ":" palette.
type paletteItem struct {
	Action Action
	Label  string
	Desc   string
	// Keys is the display keybinding(s) currently bound to the action, or
	// "·" when the action is unbound.
	Keys string
}

// paletteState drives the ":" command palette overlay: a filter query over
// the command set (paletteActions) and a cursor into the filtered items.
type paletteState struct {
	Input  string
	Items  []paletteItem
	Cursor int
}

// selected returns the highlighted item, or ok=false when the filtered list
// is empty.
func (p *paletteState) selected() (paletteItem, bool) {
	if p == nil || p.Cursor < 0 || p.Cursor >= len(p.Items) {
		return paletteItem{}, false
	}
	return p.Items[p.Cursor], true
}

// move steps the cursor, clamping to the filtered list.
func (p *paletteState) move(delta int) {
	p.Cursor += delta
	if p.Cursor < 0 || len(p.Items) == 0 {
		p.Cursor = 0
		return
	}
	if p.Cursor >= len(p.Items) {
		p.Cursor = len(p.Items) - 1
	}
}

// refilter rebuilds the visible items from the query, matching the label,
// description, and action name case-insensitively (like the "/" search).
// Catalog order is preserved; the cursor is clamped back into range.
func (p *paletteState) refilter(km *Keymap) {
	q := strings.ToLower(strings.TrimSpace(p.Input))
	p.Items = p.Items[:0]
	for _, meta := range paletteActions() {
		hay := strings.ToLower(meta.Palette + " " + meta.Desc + " " + string(meta.Action))
		if q != "" && !strings.Contains(hay, q) {
			continue
		}
		p.Items = append(p.Items, paletteItem{
			Action: meta.Action,
			Label:  meta.Palette,
			Desc:   meta.Desc,
			Keys:   paletteKeyHint(km, meta.Action),
		})
	}
	if p.Cursor >= len(p.Items) {
		p.Cursor = len(p.Items) - 1
	}
	if p.Cursor < 0 {
		p.Cursor = 0
	}
}

// paletteKeyHint renders the keys bound to an action for the palette row.
func paletteKeyHint(km *Keymap, a Action) string {
	keys := km.KeysFor(a)
	if len(keys) == 0 {
		return "·"
	}
	return strings.Join(keys, "/")
}

// openPalette opens the ":" command palette with every command visible.
func (m Model) openPalette() (tea.Model, tea.Cmd) {
	p := &paletteState{}
	p.refilter(m.Keymap)
	m.Palette = p
	m.Status = ":"
	return m, nil
}

// updatePalette consumes keys while the palette is open: printable runes edit
// the filter, up/down (ctrl+p/ctrl+n) move the cursor, enter runs the
// selected command, esc closes.
func (m Model) updatePalette(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.Palette
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.Palette = nil
		m.Status = ""
		return m, nil
	case tea.KeyEnter:
		item, ok := p.selected()
		m.Palette = nil
		if !ok {
			m.Status = "no matching command"
			return m, nil
		}
		// The palette is closed first so a command that opens its own
		// overlay (search prompt, annotation editor, …) is not shadowed.
		return m.runAction(item.Action, 1)
	case tea.KeyUp, tea.KeyCtrlP:
		p.move(-1)
		return m, nil
	case tea.KeyDown, tea.KeyCtrlN:
		p.move(1)
		return m, nil
	case tea.KeyBackspace:
		if r := []rune(p.Input); len(r) > 0 {
			p.Input = string(r[:len(r)-1])
			p.refilter(m.Keymap)
		}
		m.Status = ":" + p.Input
		return m, nil
	case tea.KeySpace:
		p.Input += " "
		p.refilter(m.Keymap)
		m.Status = ":" + p.Input
		return m, nil
	case tea.KeyRunes:
		p.Input += string(msg.Runes)
		p.refilter(m.Keymap)
		m.Status = ":" + p.Input
		return m, nil
	}
	return m, nil
}

// paletteWindow returns the [start,end) slice of a length-n list that keeps
// the cursor visible within maxRows rows.
func paletteWindow(cursor, n, maxRows int) (int, int) {
	if maxRows < 1 {
		maxRows = 1
	}
	if n <= maxRows {
		return 0, n
	}
	start := cursor - maxRows/2
	if start < 0 {
		start = 0
	}
	end := start + maxRows
	if end > n {
		end = n
		start = end - maxRows
	}
	return start, end
}

// renderPaletteOverlay renders the ":" palette: the filter query, a windowed
// command list with the highlighted row marked, and the selected command's
// description in a footer.
func (m Model) renderPaletteOverlay(width, bodyHeight int) string {
	p := m.Palette
	innerW := width - 10
	if innerW < 36 {
		innerW = 36
	}
	lines := []string{
		clipLine("command palette — type to filter · ↑↓ move · enter run · esc close", innerW),
		clipLine(": "+p.Input+"▏", innerW),
		"",
	}
	if len(p.Items) == 0 {
		lines = append(lines, clipLine("  (no matching command)", innerW))
	} else {
		maxRows := bodyHeight - 10
		if maxRows < 3 {
			maxRows = 3
		}
		start, end := paletteWindow(p.Cursor, len(p.Items), maxRows)
		keyW := 0
		for i := start; i < end; i++ {
			if w := lipgloss.Width(p.Items[i].Keys); w > keyW {
				keyW = w
			}
		}
		if start > 0 {
			lines = append(lines, clipLine("   ↑ more", innerW))
		}
		for i := start; i < end; i++ {
			it := p.Items[i]
			marker := "  "
			if i == p.Cursor {
				marker = "> "
			}
			key := it.Keys + strings.Repeat(" ", keyW-lipgloss.Width(it.Keys))
			row := clipLine(marker+"["+key+"] "+it.Label, innerW)
			if i == p.Cursor {
				row = lipgloss.NewStyle().Bold(true).Render(row)
			}
			lines = append(lines, row)
		}
		if end < len(p.Items) {
			lines = append(lines, clipLine("   ↓ more", innerW))
		}
		if sel, ok := p.selected(); ok {
			lines = append(lines, "", clipLine("  "+sel.Desc, innerW))
		}
	}
	box := m.Styles.PaneFocused.
		Border(lipgloss.RoundedBorder()).
		Padding(1, 3).
		Render(strings.Join(lines, "\n"))
	return lipgloss.Place(width, bodyHeight, lipgloss.Center, lipgloss.Center, box)
}
