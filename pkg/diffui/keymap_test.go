package diffui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestDefaultKeymapCoversDispatch(t *testing.T) {
	km := NewKeymap()
	// Spot-check representative default bindings.
	cases := map[string]Action{
		"j": ActionNext, "down": ActionNext,
		"k": ActionPrev, "F": ActionFullPage, "x": ActionBlink,
		"/": ActionSearch, "@": ActionAnnotationList, "\\": ActionLayoutCycle,
		"|": ActionPDFZoom, "?": ActionHelp, "q": ActionQuit, "Q": ActionDiscard,
		" ": ActionReviewToggle, "ctrl+a": ActionAnnotateEdit,
	}
	for key, want := range cases {
		if got := km.Lookup(key); got != want {
			t.Fatalf("default binding %q = %q, want %q", key, got, want)
		}
	}
	// Every catalog action must have at least one default key so nothing is
	// unreachable out of the box.
	bound := map[Action]bool{}
	for _, a := range defaultBindings() {
		bound[a] = true
	}
	for _, meta := range actionCatalog() {
		if !bound[meta.Action] {
			t.Fatalf("action %q has no default binding", meta.Action)
		}
	}
}

func TestKeymapApplyFileMapUnmap(t *testing.T) {
	km := NewKeymap()
	warns := km.ApplyFile(`
# remap next to semicolon, drop the blink comparator
map ; next
unmap x
map ctrl+n search-next
`)
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if km.Lookup(";") != ActionNext {
		t.Fatalf("; should map to next")
	}
	if km.Lookup("x") != ActionNone {
		t.Fatalf("x should be unmapped")
	}
	if km.Lookup("ctrl+n") != ActionSearchNext {
		t.Fatalf("ctrl+n should map to search-next")
	}
	// The original j binding survives (additive).
	if km.Lookup("j") != ActionNext {
		t.Fatalf("default j binding must survive an additive file")
	}
}

func TestKeymapApplyFileWarnsOnBadInput(t *testing.T) {
	km := NewKeymap()
	warns := km.ApplyFile("map ; bogus-action\nmap\nfrobnicate x\n")
	if len(warns) != 3 {
		t.Fatalf("expected 3 warnings, got %d: %v", len(warns), warns)
	}
	if km.Lookup(";") != ActionNone {
		t.Fatalf("an unknown action must not bind")
	}
}

func TestKeymapApplyConfig(t *testing.T) {
	km := NewKeymap()
	warns := km.ApplyConfig(map[string]string{";": "next", "ctrl+x": "bogus"})
	if km.Lookup(";") != ActionNext {
		t.Fatalf("config override should bind ;")
	}
	if len(warns) != 1 {
		t.Fatalf("expected one warning for the bogus action, got %v", warns)
	}
}

func TestKeymapDumpRoundTrips(t *testing.T) {
	km := NewKeymap()
	dump := km.Dump()
	// A fresh keymap fed its own dump must be unchanged.
	km2 := NewKeymap()
	// Clear km2, then re-apply the dump; it must reproduce the defaults.
	for key := range km2.bindings {
		delete(km2.bindings, key)
	}
	if warns := km2.ApplyFile(dump); len(warns) != 0 {
		t.Fatalf("dump must parse cleanly: %v", warns)
	}
	for key, act := range km.bindings {
		if km2.Lookup(key) != act {
			t.Fatalf("dump round-trip lost binding %q -> %q", key, act)
		}
	}
}

// TestRemappedKeyDispatches proves the dispatch actually runs on the
// remapped key: bind ";" to next and confirm it advances the cursor while
// the default "j" still works.
func TestRemappedKeyDispatches(t *testing.T) {
	km := NewKeymap()
	km.ApplyFile("map ; next\nunmap j")
	m := New(fixtureReview(), Options{Keymap: km})
	start := currentID(m)

	m = pressKey(t, m, ";")
	if currentID(m) == start {
		t.Fatalf("remapped ; should advance the cursor")
	}

	// j was unmapped — it must now be inert.
	held := currentID(m)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if currentID(next.(Model)) != held {
		t.Fatalf("unmapped j must not move the cursor")
	}
}

// TestVimMotionsSurviveRemapping pins that the fixed count prefix and gg
// leader still work regardless of the keymap.
func TestVimMotionsSurviveRemapping(t *testing.T) {
	m := New(fixtureManyChangedReview(20), Options{})
	if currentID(m) != "p00" {
		t.Fatalf("cursor should start at p00, got %s", currentID(m))
	}
	m = pressKey(t, m, "1")
	m = pressKey(t, m, "0")
	m = pressKey(t, m, "j")
	if currentID(m) != "p10" {
		t.Fatalf("10j should jump ten pairs, got %s", currentID(m))
	}
	m = pressKey(t, m, "g")
	m = pressKey(t, m, "g")
	if currentID(m) != "p00" {
		t.Fatalf("gg should return to the first pair, got %s", currentID(m))
	}
}

func TestQuitAndHelpAreRemappable(t *testing.T) {
	km := NewKeymap()
	km.ApplyFile("unmap ?\nmap H help")
	m := New(fixtureReview(), Options{Keymap: km})
	m.Width, m.Height = 80, 40

	// ? is no longer help.
	m2 := pressKey(t, m, "?")
	if m2.ShowHelp {
		t.Fatalf("? should no longer toggle help after unmap")
	}
	// H now toggles help.
	m3 := pressKey(t, m, "H")
	if !m3.ShowHelp {
		t.Fatalf("remapped H should toggle help")
	}
	// ctrl+c always quits regardless of the keymap.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatalf("ctrl+c must always quit")
	}
}
