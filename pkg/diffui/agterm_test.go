package diffui

import (
	"fmt"
	"os"
	"path/filepath"
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

// forceAgtermTree stubs the tree read behind agtermSessionName with a name
// that the recording rename stub keeps current, so mark/restore can be driven
// end to end. It also clears the mark globals before and after the test.
func forceAgtermTree(t *testing.T, name string) *string {
	t.Helper()
	_ = forceAgterm(t)
	t.Setenv("AGTERM_PANE", "") // the review speaks for the session
	current := name
	savedOutput := agtermOutput
	agtermOutput = func(args ...string) ([]byte, error) {
		return []byte(fmt.Sprintf(
			`{"result":{"tree":{"workspaces":[{"sessions":[{"id":"test-session-uuid","name":%q}]}]}}}`,
			current)), nil
	}
	savedRun := agtermRun
	agtermRun = func(args ...string) error {
		if len(args) >= 3 && args[0] == "session" && args[1] == "rename" {
			current = args[2]
		}
		return savedRun(args...)
	}
	agtermMarkedName, agtermOrigName = "", ""
	t.Cleanup(func() {
		agtermOutput = savedOutput
		agtermMarkedName, agtermOrigName = "", ""
	})
	return &current
}

// TestAgtermSessionIconMarksAndRestores pins the sidebar mark: the review
// prefixes the icon onto the name the session already had — keeping the rest
// of it — and hands that name back on exit.
func TestAgtermSessionIconMarksAndRestores(t *testing.T) {
	name := forceAgtermTree(t, "3· SIGMA")

	AgtermMarkSession()
	if *name != agtermIcon+" SIGMA" {
		t.Fatalf("mark should prefix the icon onto the undecorated name, got %q", *name)
	}

	AgtermRestoreSessionName()
	if *name != "SIGMA" {
		t.Fatalf("exit should hand the original name back, got %q", *name)
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
