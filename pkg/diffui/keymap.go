package diffui

import (
	"fmt"
	"sort"
	"strings"
)

// Action is a named, remappable command. The key dispatch switch runs on
// actions, not raw keys, so a keybindings file (or the [keybinds] config
// table) can rebind any action without touching code. Vim motion mechanics
// — the count prefix (10j) and the gg leader — stay literal and are not
// remappable.
type Action string

const (
	ActionNone           Action = ""
	ActionNext           Action = "next"
	ActionPrev           Action = "prev"
	ActionJumpDown       Action = "jump-down"
	ActionJumpUp         Action = "jump-up"
	ActionFirst          Action = "first"
	ActionLast           Action = "last"
	ActionSectionNext    Action = "section-next"
	ActionSectionPrev    Action = "section-prev"
	ActionSourceLinePrev Action = "source-line-prev"
	ActionSourceLineNext Action = "source-line-next"
	ActionFocusPrev      Action = "focus-prev"
	ActionFocusNext      Action = "focus-next"
	ActionFilterCycle    Action = "filter-cycle"
	ActionRegimeToggle   Action = "regime-toggle"
	ActionFoldToggle     Action = "fold-toggle"
	ActionReviewToggle   Action = "review-toggle"
	ActionAnnotate       Action = "annotate"
	ActionAnnotateEdit   Action = "annotate-edit"
	ActionAnnotateDelete Action = "annotate-delete"
	ActionAnnotationList Action = "annotation-list"
	ActionCopy           Action = "copy"
	ActionSearch         Action = "search"
	ActionSearchNext     Action = "search-next"
	ActionSearchPrev     Action = "search-prev"
	ActionInfo           Action = "info"
	ActionEditInline     Action = "edit-inline"
	ActionEditExternal   Action = "edit-external"
	ActionUndo           Action = "undo"
	ActionRedo           Action = "redo"
	ActionReload         Action = "reload"
	ActionFullPage       Action = "pdf-full-page"
	ActionBlink          Action = "pdf-blink"
	ActionSkim           Action = "skim"
	ActionPreview        Action = "preview"
	ActionCompare        Action = "compare"
	ActionLayoutCycle    Action = "layout-cycle"
	ActionPDFZoom        Action = "pdf-zoom"
	ActionResizeShrink   Action = "resize-shrink"
	ActionResizeGrow     Action = "resize-grow"
	ActionHelp           Action = "help"
	ActionQuit           Action = "quit"
	ActionDiscard        Action = "discard"
	ActionPalette        Action = "palette"
)

// actionMeta describes an action for --dump-keys and for validating
// keybindings-file entries. Ordered as it should print.
type actionMeta struct {
	Action Action
	Desc   string
	// Palette is the human label shown in the ":" command palette. An empty
	// label hides the action from the palette — pure vim motions, focus and
	// resize nudges, and the search-step / quit / help / palette actions are
	// all keyboard-only and carry no label.
	Palette string
}

func actionCatalog() []actionMeta {
	return []actionMeta{
		{ActionNext, "move to next change pair (or source line when a source pane is focused)", ""},
		{ActionPrev, "move to previous change pair / source line", ""},
		{ActionJumpDown, "jump 10 pairs down", ""},
		{ActionJumpUp, "jump 5 pairs up", ""},
		{ActionFirst, "first pair", ""},
		{ActionLast, "last pair", ""},
		{ActionSectionNext, "next section", ""},
		{ActionSectionPrev, "previous section", ""},
		{ActionSourceLinePrev, "select previous source line (PDF anchor)", ""},
		{ActionSourceLineNext, "select next source line (PDF anchor)", ""},
		{ActionFocusPrev, "focus previous pane", ""},
		{ActionFocusNext, "focus next pane", ""},
		{ActionFilterCycle, "cycle the outline filter", "Cycle outline filter"},
		{ActionRegimeToggle, "toggle semantic / coalesced diff regime", "Toggle semantic / coalesced diff"},
		{ActionFoldToggle, "fold / unfold the current outline group", "Fold / unfold outline group"},
		{ActionReviewToggle, "mark the current pair reviewed", "Mark pair reviewed"},
		{ActionAnnotate, "annotate the current pair", "Annotate current pair"},
		{ActionAnnotateEdit, "edit the current annotation", "Edit current annotation"},
		{ActionAnnotateDelete, "delete the current annotation", "Delete current annotation"},
		{ActionAnnotationList, "open the annotation list", "Open annotation list"},
		{ActionCopy, "copy the selected change (side follows focus)", "Copy selected change"},
		{ActionSearch, "search pairs (text, labels, IDs)", "Search pairs (text, labels, IDs)"},
		{ActionSearchNext, "next search match", ""},
		{ActionSearchPrev, "previous search match", ""},
		{ActionInfo, "review scope + description popup", "Show review scope / description"},
		{ActionEditInline, "inline single-line edit of the new file", "Edit new file inline (current line)"},
		{ActionEditExternal, "$EDITOR edit of the new file at the cursor line", "Edit new file in $EDITOR"},
		{ActionUndo, "undo the last in-place edit", "Undo last in-place edit"},
		{ActionRedo, "redo the last undone edit", "Redo last in-place edit"},
		{ActionReload, "re-diff source and rebuild/reload the PDF", "Recompile / rebuild PDF"},
		{ActionFullPage, "toggle full-page preview / region crop", "Toggle full-page PDF preview"},
		{ActionBlink, "blink comparator: flip old / new PDF", "Blink comparator (old / new PDF)"},
		{ActionSkim, "Skim forward-search at the cursor line", "Open in Skim at current line"},
		{ActionPreview, "open the new PDF in Preview", "Open new PDF in Preview"},
		{ActionCompare, "open old vs new in the external compare editor", "Compare old vs new (external)"},
		{ActionLayoutCycle, "cycle the pane layout", "Cycle pane layout"},
		{ActionPDFZoom, "PDF-only zoom (toggle)", "Toggle PDF-only zoom"},
		{ActionResizeShrink, "shrink the focused pane / source split", ""},
		{ActionResizeGrow, "grow the focused pane / source split", ""},
		{ActionHelp, "toggle the help overlay", ""},
		{ActionQuit, "quit — save sidecar, emit annotations", ""},
		{ActionDiscard, "discard annotations/marks and quit (press twice)", ""},
		{ActionPalette, "open the command palette", ""},
	}
}

// paletteActions returns the catalog entries that carry a palette label, in
// catalog order — the command set shown in the ":" palette.
func paletteActions() []actionMeta {
	var out []actionMeta
	for _, m := range actionCatalog() {
		if m.Palette != "" {
			out = append(out, m)
		}
	}
	return out
}

// validActions is the set of remappable action names.
func validActions() map[Action]bool {
	out := make(map[Action]bool)
	for _, m := range actionCatalog() {
		out[m.Action] = true
	}
	return out
}

// defaultBindings maps default keys to actions. Multiple keys may share an
// action (j and down both advance). The gg leader (g) and digit count
// prefixes are handled literally in updateKey and are intentionally absent.
func defaultBindings() map[string]Action {
	return map[string]Action{
		"j": ActionNext, "down": ActionNext,
		"k": ActionPrev, "up": ActionPrev,
		"J": ActionJumpDown, "pgdown": ActionJumpDown,
		"K": ActionJumpUp, "pgup": ActionJumpUp,
		"home": ActionFirst,
		"G":    ActionLast, "end": ActionLast,
		"}": ActionSectionNext,
		"{": ActionSectionPrev,
		"[": ActionSourceLinePrev,
		"]": ActionSourceLineNext,
		"h": ActionFocusPrev, "left": ActionFocusPrev,
		"l": ActionFocusNext, "right": ActionFocusNext,
		"f":      ActionFilterCycle,
		"m":      ActionRegimeToggle,
		"z":      ActionFoldToggle,
		" ":      ActionReviewToggle,
		"a":      ActionAnnotate,
		"ctrl+a": ActionAnnotateEdit,
		"d":      ActionAnnotateDelete,
		"@":      ActionAnnotationList,
		"y":      ActionCopy,
		"/":      ActionSearch,
		"n":      ActionSearchNext,
		"N":      ActionSearchPrev,
		"i":      ActionInfo,
		"e":      ActionEditInline,
		"E":      ActionEditExternal,
		"u":      ActionUndo,
		"ctrl+r": ActionRedo,
		"B":      ActionReload,
		"F":      ActionFullPage,
		"x":      ActionBlink,
		"S":      ActionSkim, "s": ActionSkim,
		"P":  ActionPreview,
		"C":  ActionCompare,
		"\\": ActionLayoutCycle,
		"|":  ActionPDFZoom,
		"<":  ActionResizeShrink,
		">":  ActionResizeGrow,
		":":  ActionPalette,
		"?":  ActionHelp,
		"q":  ActionQuit,
		"Q":  ActionDiscard,
	}
}

// Keymap resolves keys to actions. It is built from the defaults, then
// overlaid with user overrides (config [keybinds] table, then the
// keybindings file), so the file wins on conflicts.
type Keymap struct {
	bindings map[string]Action
}

// NewKeymap returns the default keymap.
func NewKeymap() *Keymap {
	return &Keymap{bindings: defaultBindings()}
}

// normalizeKey canonicalizes the space bar to " " so bindings match
// whichever form the terminal reports ("space" in a keybindings file, the
// literal " " from bubbletea's KeyMsg.String()).
func normalizeKey(key string) string {
	if key == "space" {
		return " "
	}
	return key
}

// Lookup returns the action bound to key, or ActionNone.
func (k *Keymap) Lookup(key string) Action {
	if k == nil {
		return ActionNone
	}
	return k.bindings[normalizeKey(key)]
}

// KeysFor returns the display keys currently bound to action, sorted, so
// the help overlay reflects the live (possibly remapped) bindings.
func (k *Keymap) KeysFor(action Action) []string {
	if k == nil {
		return nil
	}
	var keys []string
	for key, a := range k.bindings {
		if a == action {
			keys = append(keys, displayKey(key))
		}
	}
	sort.Strings(keys)
	return keys
}

// bind sets or clears a binding. An empty/"none"/"unmap" action removes
// the key; an unknown action name is reported and ignored.
func (k *Keymap) bind(key string, action Action) error {
	if key == "" {
		return fmt.Errorf("empty key")
	}
	key = normalizeKey(key)
	switch action {
	case ActionNone, "unmap", "none", "unbind":
		delete(k.bindings, key)
		return nil
	}
	if !validActions()[action] {
		return fmt.Errorf("unknown action %q", action)
	}
	k.bindings[key] = action
	return nil
}

// ApplyConfig overlays a config [keybinds] table (key -> action name).
// Unknown actions are collected and returned as warnings, never fatal.
func (k *Keymap) ApplyConfig(overrides map[string]string) []string {
	var warns []string
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := k.bind(key, Action(strings.TrimSpace(overrides[key]))); err != nil {
			warns = append(warns, fmt.Sprintf("keybinds %q: %v", key, err))
		}
	}
	return warns
}

// ApplyFile parses keybindings-file content: one directive per line,
// "map <key> <action>" or "unmap <key>", '#' comments and blanks ignored.
// Additive over the current bindings. Returns per-line warnings.
func (k *Keymap) ApplyFile(content string) []string {
	var warns []string
	for i, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Strip a trailing inline comment (` #...`); a leading '#' is a
		// full-line comment, already handled above.
		if idx := strings.Index(line, " #"); idx >= 0 {
			line = strings.TrimSpace(line[:idx])
		}
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		switch fields[0] {
		case "map":
			if len(fields) != 3 {
				warns = append(warns, fmt.Sprintf("line %d: map needs <key> <action>", i+1))
				continue
			}
			if err := k.bind(fields[1], Action(fields[2])); err != nil {
				warns = append(warns, fmt.Sprintf("line %d: %v", i+1, err))
			}
		case "unmap":
			if len(fields) != 2 {
				warns = append(warns, fmt.Sprintf("line %d: unmap needs <key>", i+1))
				continue
			}
			delete(k.bindings, normalizeKey(fields[1]))
		default:
			warns = append(warns, fmt.Sprintf("line %d: unknown directive %q (want map/unmap)", i+1, fields[0]))
		}
	}
	return warns
}

// Dump renders the effective bindings as a keybindings-file template: a
// commented action catalog, then one `map` line per bound key (grouped by
// action, keys sorted). `mrevdiff --dump-keys` prints this.
func (k *Keymap) Dump() string {
	byAction := map[Action][]string{}
	for key, act := range k.bindings {
		byAction[act] = append(byAction[act], key)
	}
	var b strings.Builder
	b.WriteString("# mrevdiff keybindings\n")
	b.WriteString("# Format: `map <key> <action>` or `unmap <key>`; '#' comments.\n")
	b.WriteString("# Keys are bubbletea names: letters, `ctrl+a`, `left`, `pgdown`, `space`, `\\`, `|`.\n")
	b.WriteString("# The count prefix (10j) and the gg leader are fixed and cannot be remapped.\n#\n")
	for _, meta := range actionCatalog() {
		keys := byAction[meta.Action]
		sort.Strings(keys)
		b.WriteString(fmt.Sprintf("# %-18s %s\n", string(meta.Action), meta.Desc))
		for _, key := range keys {
			b.WriteString(fmt.Sprintf("map %s %s\n", displayKey(key), meta.Action))
		}
		if len(keys) == 0 {
			b.WriteString(fmt.Sprintf("# (unbound) map <key> %s\n", meta.Action))
		}
	}
	return b.String()
}

// displayKey renders a raw key for the dump, spelling the space bar as
// "space" so the template line is copy-pasteable.
func displayKey(key string) string {
	if key == " " {
		return "space"
	}
	return key
}
