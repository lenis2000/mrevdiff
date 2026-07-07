package diffui

import (
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
