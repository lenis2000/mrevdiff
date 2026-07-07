package diffui

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lenis2000/mrevdiff/pkg/diffreview"
)

func TestCompareEditorArgvBuilderForMatchedAddedAndDeletedPairs(t *testing.T) {
	m := New(fixtureReviewWithPaths(), Options{Filter: FilterAll})

	m.Cursor = 1
	m.SourceLineCursor = 2
	target, err := m.compareTarget()
	if err != nil {
		t.Fatalf("matched compare target: %v", err)
	}
	assertArgv(t, buildCompareEditorArgv("zed", nil, target), []string{
		"/repo/.mrevdiff/session/paper.old.tex:4",
		"/repo/paper.tex:4",
	})

	m.Cursor = 2
	m.SourceLineCursor = 1
	target, err = m.compareTarget()
	if err != nil {
		t.Fatalf("added compare target: %v", err)
	}
	assertArgv(t, buildCompareEditorArgv("zed", nil, target), []string{
		"/repo/.mrevdiff/session/paper.old.tex:4",
		"/repo/paper.tex:6",
	})

	m.Cursor = 3
	target, err = m.compareTarget()
	if err != nil {
		t.Fatalf("deleted compare target: %v", err)
	}
	assertArgv(t, buildCompareEditorArgv("zed", nil, target), []string{
		"/repo/.mrevdiff/session/paper.old.tex:9",
		"/repo/paper.tex:12",
	})
}

func TestCompareEditorArgvDefaultsToPlainPathsForNonZedCommand(t *testing.T) {
	target := compareTarget{
		OldPath: "/repo/old.tex",
		NewPath: "/repo/new.tex",
		OldLine: 10,
		NewLine: 20,
	}
	assertArgv(t, buildCompareEditorArgv("/bin/true", []string{"--flag"}, target), []string{
		"--flag",
		"/repo/old.tex",
		"/repo/new.tex",
	})
}

func TestResolveCompareEditorPrefersOpendiffFallback(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"opendiff", "zed"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("write fake %s: %v", name, err)
		}
	}
	t.Setenv("MREVDIFF_COMPARE_EDITOR", "")
	t.Setenv("PATH", dir)

	head, _, ok := resolveCompareEditor()
	if !ok {
		t.Fatalf("expected fake compare editor to resolve")
	}
	if filepath.Base(head) != "opendiff" {
		t.Fatalf("compare editor = %q, want opendiff", head)
	}
}

func TestOpenCompareEditorMissingCompareEditorGivesStatus(t *testing.T) {
	t.Setenv("MREVDIFF_COMPARE_EDITOR", "")
	t.Setenv("PATH", t.TempDir())

	m := New(fixtureReviewWithPaths(), Options{})
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("C")})
	if cmd != nil {
		t.Fatalf("expected no command when compare editor is missing")
	}
	nm, ok := next.(Model)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}
	if !strings.Contains(nm.Status, "no compare editor found") {
		t.Fatalf("missing-editor status = %q", nm.Status)
	}
}

func TestZDoesNotOpenCompareEditor(t *testing.T) {
	truePath, err := exec.LookPath("true")
	if err != nil {
		t.Fatalf("true not found: %v", err)
	}
	t.Setenv("MREVDIFF_COMPARE_EDITOR", truePath)

	m := New(fixtureReviewWithPaths(), Options{})
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Z")})
	if cmd != nil {
		t.Fatalf("expected Z to be unbound, got command")
	}
	nm, ok := next.(Model)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}
	if nm.Status != "" {
		t.Fatalf("expected Z to leave status alone, got %q", nm.Status)
	}
}

func TestOpenCompareInitSchedulesOneOpenCommand(t *testing.T) {
	truePath, err := exec.LookPath("true")
	if err != nil {
		t.Fatalf("true not found: %v", err)
	}
	t.Setenv("MREVDIFF_COMPARE_EDITOR", truePath)
	m := New(fixtureReviewWithPaths(), Options{OpenCompare: true})

	saved := runDiffCompareProcess
	var calls int
	var gotArgs []string
	runDiffCompareProcess = func(cmd *exec.Cmd) tea.Cmd {
		calls++
		gotArgs = append([]string{}, cmd.Args...)
		return func() tea.Msg { return diffCompareFinishedMsg{} }
	}
	t.Cleanup(func() { runDiffCompareProcess = saved })

	cmd := m.Init()
	if cmd == nil {
		t.Fatalf("expected --open-compare to schedule a command")
	}
	if calls != 1 {
		t.Fatalf("scheduled compare commands = %d, want 1", calls)
	}
	if len(gotArgs) != 3 || gotArgs[0] != truePath ||
		gotArgs[1] != "/repo/.mrevdiff/session/paper.old.tex" ||
		gotArgs[2] != "/repo/paper.tex" {
		t.Fatalf("unexpected command args: %#v", gotArgs)
	}

	next, _ := m.Update(cmd())
	nm, ok := next.(Model)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}
	if nm.Status != "opened external compare" {
		t.Fatalf("status after command = %q", nm.Status)
	}
}

func assertArgv(t *testing.T, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %#v, want %#v", got, want)
	}
}

func fixtureReviewWithPaths() *diffreview.Review {
	review := fixtureReview()
	review.Old = diffreview.Endpoint{
		Kind:         diffreview.GitBlob,
		Spec:         "HEAD:paper.tex",
		Path:         "/repo/.mrevdiff/session/paper.old.tex",
		Materialized: true,
	}
	review.New = diffreview.Endpoint{
		Kind: diffreview.WorkingFile,
		Spec: "paper.tex",
		Path: "/repo/paper.tex",
	}
	return review
}
