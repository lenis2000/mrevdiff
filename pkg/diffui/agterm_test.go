package diffui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lenis2000/mrevdiff/pkg/diffreview"
)

// forceAgterm makes the integration report available with a recording
// stub, bypassing env detection (and restores everything afterwards).
func forceAgterm(t *testing.T) *[][]string {
	t.Helper()
	agtermOnce.Do(func() {}) // burn the Once so setup never overwrites us
	savedCtl, savedSession := agtermCtl, agtermSession
	savedRun := agtermRun
	agtermCtl = "/stub/agtermctl"
	agtermSession = "test-session-uuid"
	var calls [][]string
	agtermRun = func(args ...string) error {
		calls = append(calls, args)
		return nil
	}
	t.Cleanup(func() {
		agtermCtl, agtermSession = savedCtl, savedSession
		agtermRun = savedRun
	})
	return &calls
}

// fakeAgtermSession holds what agterm derives a session's tree name from:
// the custom name a rename sets (blank clears it), else the main pane's OSC
// title, else its cwd basename.
type fakeAgtermSession struct {
	custom, title, cwd string
	splitFocused       bool
	splitTitle         string
}

func (s *fakeAgtermSession) treeName() string {
	switch {
	case s.custom != "":
		return s.custom
	case s.splitFocused:
		return s.splitTitle
	case s.title != "":
		return s.title
	case s.cwd == "":
		return "~"
	}
	return filepath.Base(s.cwd)
}

// forceAgtermTree stubs the tree read behind agtermSessionName with a session
// whose custom name is name; the returned pointer is that custom name, which
// the recording rename stub keeps current, so mark/restore can be driven end
// to end. It also clears the mark globals before and after the test.
func forceAgtermTree(t *testing.T, name string) *string {
	t.Helper()
	return &forceAgtermSession(t, fakeAgtermSession{custom: name}).custom
}

// forceAgtermSession is forceAgtermTree for a session described in full.
func forceAgtermSession(t *testing.T, fake fakeAgtermSession) *fakeAgtermSession {
	t.Helper()
	_ = forceAgterm(t)
	t.Setenv("AGTERM_PANE", "") // the review speaks for the session
	s := &fake
	savedOutput := agtermOutput
	agtermOutput = func(args ...string) ([]byte, error) {
		node := map[string]any{"id": "test-session-uuid", "name": s.treeName(), "cwd": s.cwd}
		if s.title != "" {
			node["title"] = s.title
		}
		if s.splitFocused {
			node["splitFocused"] = true
		}
		tree := map[string]any{"result": map[string]any{"tree": map[string]any{
			"workspaces": []any{map[string]any{"sessions": []any{node}}}}}}
		return json.Marshal(tree)
	}
	savedRun := agtermRun
	agtermRun = func(args ...string) error {
		if len(args) >= 3 && args[0] == "session" && args[1] == "rename" {
			s.custom = strings.TrimSpace(args[2])
		}
		return savedRun(args...)
	}
	agtermMarkedName, agtermOrigName = "", ""
	t.Cleanup(func() {
		agtermOutput = savedOutput
		agtermMarkedName, agtermOrigName = "", ""
	})
	return s
}

// TestAgtermSessionIconMarksAndRestores pins the sidebar mark: the review
// puts the icon between the sidebar's tab number and the name the session
// already had — keeping both — and hands the numbered name back on exit.
func TestAgtermSessionIconMarksAndRestores(t *testing.T) {
	name := forceAgtermTree(t, "3· SIGMA")

	AgtermMarkSession()
	if *name != "3· "+agtermIcon+" SIGMA" {
		t.Fatalf("mark should put the icon after the tab number, got %q", *name)
	}

	AgtermRestoreSessionName()
	if *name != "3· SIGMA" {
		t.Fatalf("exit should hand the numbered name back, got %q", *name)
	}
}

var (
	hookNumber = regexp.MustCompile(`^(?:\d+[·⋅│]|⌘\d+)(?:\s+|$)`)
	hookMarker = regexp.MustCompile(`^[■□]\s*`)
	hookClock  = regexp.MustCompile(`^\[\d{1,2}\]\s*`)
)

// sidebarHook relabels the fake the way the hook pass of ~/.vim/agterm/
// agterm-sidebar.sh does on a tree change: an unnumbered name agterm derived
// itself is left alone; any other name is re-prefixed with tab number n and
// the scratch marker ("■ ", "□ " or ""), keeping the body after one run of
// numbers, one marker and one counter.
func sidebarHook(s *fakeAgtermSession, n int, marker string) {
	name := s.treeName()
	if !hookNumber.MatchString(name) && (name == s.title || name == "~" || name == filepath.Base(s.cwd)) {
		return
	}
	body := name
	for b := hookNumber.ReplaceAllString(body, ""); b != body; b = hookNumber.ReplaceAllString(body, "") {
		body = b
	}
	body = strings.TrimSpace(hookClock.ReplaceAllString(hookMarker.ReplaceAllString(body, ""), ""))
	s.custom = strings.TrimRight(fmt.Sprintf("%d· %s%s", n, marker, body), " ")
}

// TestAgtermSessionIconSurvivesSidebarHook pins mark and restore against the
// sidebar hook, which rewrites the tab number and scratch marker in front of
// every custom name whenever the tree changes: the mark keeps the decoration
// so the hook has nothing to add, the exit still recognises its mark after the
// hook moved the number or the marker, the restored name keeps the current
// decoration, and nothing doubles up over repeated reviews.
func TestAgtermSessionIconSurvivesSidebarHook(t *testing.T) {
	for _, tc := range []struct {
		desc   string
		fake   fakeAgtermSession
		marked string
		n      int    // tab number during the review
		marker string // scratch marker during the review
		want   string
	}{
		{"hidden scratch", fakeAgtermSession{custom: "2· □ SIGMA", title: "zsh", cwd: "/w/paper"},
			"2· □ 📕 SIGMA", 2, "□ ", "2· □ SIGMA"},
		{"scratch opened mid-review", fakeAgtermSession{custom: "2· SIGMA", title: "zsh", cwd: "/w/paper"},
			"2· 📕 SIGMA", 2, "□ ", "2· □ SIGMA"},
		{"scratch closed mid-review", fakeAgtermSession{custom: "2· ■ SIGMA", title: "zsh", cwd: "/w/paper"},
			"2· ■ 📕 SIGMA", 2, "", "2· SIGMA"},
		{"tab moved", fakeAgtermSession{custom: "4· SIGMA", title: "zsh", cwd: "/w/paper"},
			"4· 📕 SIGMA", 1, "", "1· SIGMA"},
		{"unnumbered custom name", fakeAgtermSession{custom: "SIGMA", title: "zsh", cwd: "/w/paper"},
			"📕 SIGMA", 3, "", "3· SIGMA"},
		{"doubled decoration", fakeAgtermSession{custom: "2· □ 2· □ SIGMA", title: "zsh", cwd: "/w/paper"},
			"2· □ 📕 SIGMA", 2, "□ ", "2· □ SIGMA"},
		{"numbered name equal to the cwd basename", fakeAgtermSession{custom: "1· zzrevx", cwd: "/w/zzrevx"},
			"1· 📕 zzrevx", 1, "", "1· zzrevx"},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			s := forceAgtermSession(t, tc.fake)
			for run := 1; run <= 2; run++ {
				AgtermMarkSession()
				if run == 1 && s.custom != tc.marked {
					t.Fatalf("mark: got %q, want %q", s.custom, tc.marked)
				}
				sidebarHook(s, tc.n, tc.marker)
				AgtermRestoreSessionName()
				if s.custom != tc.want {
					t.Fatalf("run %d: exit left %q, want %q", run, s.custom, tc.want)
				}
				sidebarHook(s, tc.n, tc.marker)
				if s.custom != tc.want {
					t.Fatalf("run %d: the hook relabelled the restored name to %q", run, s.custom)
				}
			}
		})
	}
}

// TestAgtermSessionIconLeavesHookedMarkAlone pins the nested review once the
// hook has decorated the outer one's mark: the inner review sees the icon
// behind the decoration and leaves the session alone.
func TestAgtermSessionIconLeavesHookedMarkAlone(t *testing.T) {
	name := forceAgtermTree(t, "2· □ "+agtermIcon+" SIGMA")

	AgtermMarkSession()
	AgtermRestoreSessionName()
	if *name != "2· □ "+agtermIcon+" SIGMA" {
		t.Fatalf("an already-marked session must be left untouched, got %q", *name)
	}
}

// TestAgtermSessionNameReadsOwnWindow pins the tree read to the window the
// review runs in: without --window agtermctl reads the frontmost window, which
// need not hold this session by the time the review exits.
func TestAgtermSessionNameReadsOwnWindow(t *testing.T) {
	t.Setenv("AGTERM_WINDOW_ID", "WINDOW-1")
	_ = forceAgtermTree(t, "SIGMA")
	stub := agtermOutput
	var got []string
	agtermOutput = func(args ...string) ([]byte, error) {
		got = args
		return stub(args...)
	}

	if name, _ := agtermSessionName(); name != "SIGMA" {
		t.Fatalf("tree read returned %q", name)
	}
	if strings.Join(got, " ") != "tree --json --window WINDOW-1" {
		t.Fatalf("tree read should name the session's window, got %q", got)
	}
}

// TestAgtermSessionIconNotDoubled pins that a session already carrying the
// icon (a nested review) is left alone, so the icon never stacks up and the
// exit of the inner review does not strip the outer one's mark.
func TestAgtermSessionIconNotDoubled(t *testing.T) {
	name := forceAgtermTree(t, agtermIcon+" SIGMA")

	AgtermMarkSession()
	AgtermRestoreSessionName()
	if *name != agtermIcon+" SIGMA" {
		t.Fatalf("an already-marked session must be left untouched, got %q", *name)
	}
}

// TestAgtermSessionIconKeepsUserRename pins that a rename by the user during
// the review wins: exit must not clobber it with the pre-review name.
func TestAgtermSessionIconKeepsUserRename(t *testing.T) {
	name := forceAgtermTree(t, "SIGMA")

	AgtermMarkSession()
	*name = "renamed by hand"
	AgtermRestoreSessionName()
	if *name != "renamed by hand" {
		t.Fatalf("a mid-review rename is the user's to keep, got %q", *name)
	}
}

// TestAgtermSessionIconClearsDerivedName pins the restore of a session that
// had no custom name: exit clears the icon name with a blank rename, so the
// label follows the live title again rather than being pinned to the label
// the review started under.
func TestAgtermSessionIconClearsDerivedName(t *testing.T) {
	for _, tc := range []struct {
		desc string
		fake fakeAgtermSession
	}{
		{"title", fakeAgtermSession{title: "mrevdiff paper.tex", cwd: "/work/paper"}},
		{"cwd basename", fakeAgtermSession{cwd: "/work/paper"}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			s := forceAgtermSession(t, tc.fake)
			label := s.treeName()

			AgtermMarkSession()
			if s.custom != agtermIcon+" "+label {
				t.Fatalf("mark should prefix the icon onto the derived label, got %q", s.custom)
			}

			AgtermRestoreSessionName()
			if s.custom != "" {
				t.Fatalf("exit must clear the custom name, not pin %q", s.custom)
			}
			s.title = "zsh"
			if got := s.treeName(); got != "zsh" {
				t.Fatalf("the label should follow the live title after exit, got %q", got)
			}
		})
	}
}

// TestAgtermSessionIconKeepsCustomNameMatchingCwd pins that only a name agterm
// itself shows counts as derived: a custom name that happens to equal the cwd
// basename, on a session that has a title, is a rename and is handed back.
func TestAgtermSessionIconKeepsCustomNameMatchingCwd(t *testing.T) {
	s := forceAgtermSession(t, fakeAgtermSession{custom: "paper", title: "zsh", cwd: "/work/paper"})

	AgtermMarkSession()
	AgtermRestoreSessionName()
	if s.custom != "paper" {
		t.Fatalf("a custom name must be handed back, got %q", s.custom)
	}
}

// TestAgtermSessionIconPinsSplitFocusedName pins the fallback when the tree
// cannot tell: the split pane's title is not in the tree, so a session
// focused on its split keeps the old restore by name.
func TestAgtermSessionIconPinsSplitFocusedName(t *testing.T) {
	s := forceAgtermSession(t, fakeAgtermSession{title: "zsh", cwd: "/work/paper",
		splitFocused: true, splitTitle: "vim"})

	AgtermMarkSession()
	AgtermRestoreSessionName()
	if s.custom != "vim" {
		t.Fatalf("an undecidable name must be restored by name, got %q", s.custom)
	}
}

// TestAgtermFlagFollowsAnnotationsAndFailures pins the reconciler: the
// flag flips on when feedback appears, off when it is gone, and only
// transitions shell out.
func TestAgtermFlagFollowsAnnotationsAndFailures(t *testing.T) {
	calls := forceAgterm(t)
	m := New(fixtureReview(), Options{})

	if fn := (&m).syncAgtermFlag(); fn != nil {
		t.Fatalf("no annotations, no failure — flag must stay off")
	}

	m.ensureSidecar().Annotations = []diffreview.Annotation{{PairID: "changed", Note: "n"}}
	fn := (&m).syncAgtermFlag()
	if fn == nil {
		t.Fatalf("annotations appearing must flip the flag on")
	}
	fn()
	if len(*calls) != 1 || strings.Join((*calls)[0], " ") != "session flag on --target test-session-uuid" {
		t.Fatalf("unexpected agtermctl invocation: %v", *calls)
	}
	if fn := (&m).syncAgtermFlag(); fn != nil {
		t.Fatalf("unchanged state must not shell out again")
	}

	m.ensureSidecar().Annotations = nil
	fn = (&m).syncAgtermFlag()
	if fn == nil {
		t.Fatalf("annotations vanishing must flip the flag off")
	}
	fn()
	if got := strings.Join((*calls)[1], " "); got != "session flag off --target test-session-uuid" {
		t.Fatalf("unexpected off invocation: %q", got)
	}

	m.buildFailed = true
	if fn := (&m).syncAgtermFlag(); fn == nil {
		t.Fatalf("a failed rebuild must flip the flag on")
	}
}

// TestAgtermOverlayEditorRunsInOverlay pins the E-in-overlay path: inside
// agterm the editor runs via `agtermctl session overlay open` with a
// login-shell wrapper, --block, and this session as the target — the TUI
// is never suspended.
func TestAgtermOverlayEditorRunsInOverlay(t *testing.T) {
	calls := forceAgterm(t)
	t.Setenv("EDITOR", "vi")

	dir := t.TempDir()
	newPath := filepath.Join(dir, "paper.tex")
	if err := os.WriteFile(newPath, []byte("line one\nline two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	review := fixtureReview()
	review.New = diffreview.Endpoint{Kind: diffreview.WorkingFile, Path: newPath, Editable: true}
	m := New(review, Options{AllowModifications: true, RequestedAllowMods: true})
	m.Width, m.Height = 120, 40

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("E")})
	m = next.(Model)
	if cmd == nil {
		t.Fatalf("E should produce an overlay command, status %q", m.Status)
	}
	msg := cmd()
	batch, isBatch := msg.(tea.BatchMsg)
	if isBatch {
		// The Update wrapper may batch the agterm flag sync with the edit;
		// find the edit-finished message.
		found := false
		for _, c := range batch {
			if c == nil {
				continue
			}
			if fin, ok := c().(diffEditFinishedMsg); ok {
				found = true
				if fin.err != nil {
					t.Fatalf("stubbed overlay edit should succeed: %v", fin.err)
				}
			}
		}
		if !found {
			t.Fatalf("batched command should include the edit finish")
		}
	} else if fin, ok := msg.(diffEditFinishedMsg); ok {
		if fin.err != nil {
			t.Fatalf("stubbed overlay edit should succeed: %v", fin.err)
		}
	} else {
		t.Fatalf("expected diffEditFinishedMsg, got %T", msg)
	}

	var overlayCall []string
	for _, c := range *calls {
		if len(c) > 2 && c[0] == "session" && c[1] == "overlay" {
			overlayCall = c
		}
	}
	if overlayCall == nil {
		t.Fatalf("no overlay invocation recorded: %v", *calls)
	}
	joined := strings.Join(overlayCall, " ")
	for _, needle := range []string{"overlay open", "zsh -lc", "--target test-session-uuid", "--block", "--cwd " + dir} {
		if !strings.Contains(joined, needle) {
			t.Fatalf("overlay invocation missing %q: %q", needle, joined)
		}
	}
	if !strings.Contains(joined, "paper.tex") {
		t.Fatalf("overlay command should reference the edited file: %q", joined)
	}
}
