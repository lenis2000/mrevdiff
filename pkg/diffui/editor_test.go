package diffui

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"mrevdiff/pkg/diffreview"
)

func TestDiffExternalEditOpensNewPathOnly(t *testing.T) {
	m, oldPath, newPath := editableDiffModel(t, paragraphDoc("Old sentence."), paragraphDoc("New sentence."))
	m.NoBuild = true
	t.Setenv("EDITOR", "/bin/true --wait")

	var captured []string
	saved := runDiffEditorProcess
	runDiffEditorProcess = func(cmd *exec.Cmd, done func(error) tea.Msg) tea.Cmd {
		captured = append([]string(nil), cmd.Args...)
		return func() tea.Msg {
			if err := os.WriteFile(newPath, []byte(paragraphDoc("Edited externally.")), 0o600); err != nil {
				return done(err)
			}
			return done(nil)
		}
	}
	t.Cleanup(func() { runDiffEditorProcess = saved })

	next, cmd := m.editInExternalEditor()
	if cmd == nil {
		t.Fatalf("expected editor runner to return completion command")
	}
	m = next.(Model)
	if len(captured) == 0 {
		t.Fatalf("expected editor argv to be captured")
	}
	if !containsString(captured, newPath) {
		t.Fatalf("editor argv %v did not include new path %q", captured, newPath)
	}
	if containsString(captured, oldPath) {
		t.Fatalf("editor argv %v unexpectedly included old path %q", captured, oldPath)
	}
	if len(m.EditUndo) != 1 || m.EditUndo[0].Path != newPath {
		t.Fatalf("undo snapshot = %#v, want one snapshot for new path", m.EditUndo)
	}
	next, _ = m.Update(cmd())
	m = next.(Model)
	if !strings.Contains(string(m.Review.New.Source), "Edited externally.") {
		t.Fatalf("external edit completion did not reload new source")
	}
	if !strings.Contains(m.Status, "external edit reloaded") {
		t.Fatalf("external edit status = %q", m.Status)
	}
}

func TestDiffExternalEditErrorDoesNotReload(t *testing.T) {
	m, _, _ := editableDiffModel(t, paragraphDoc("Old sentence."), paragraphDoc("New sentence."))
	t.Setenv("EDITOR", "/bin/true")

	saved := runDiffEditorProcess
	runDiffEditorProcess = func(_ *exec.Cmd, done func(error) tea.Msg) tea.Cmd {
		return func() tea.Msg { return done(errors.New("boom")) }
	}
	t.Cleanup(func() { runDiffEditorProcess = saved })

	next, cmd := m.editInExternalEditor()
	m = next.(Model)
	if cmd == nil {
		t.Fatalf("expected editor command")
	}
	next, _ = m.Update(cmd())
	m = next.(Model)
	if !strings.Contains(m.Status, "editor exited with error: boom") {
		t.Fatalf("external edit error status = %q", m.Status)
	}
	if !strings.Contains(string(m.Review.New.Source), "New sentence.") {
		t.Fatalf("new source should not reload to unrelated content")
	}
}

func TestDiffInlineEditRewritesNewFileOnly(t *testing.T) {
	oldSrc := paragraphDoc("Old sentence.")
	newSrc := paragraphDoc("New sentence.")
	m, oldPath, newPath := editableDiffModel(t, oldSrc, newSrc)

	next, _ := m.startLineEdit()
	m = next.(Model)
	if m.LineEdit == nil {
		t.Fatalf("expected line editor")
	}
	m.LineEdit.TA.SetValue("Edited new sentence.")
	next, _ = m.submitLineEdit()
	m = next.(Model)

	oldBytes, err := os.ReadFile(oldPath)
	if err != nil {
		t.Fatalf("read old: %v", err)
	}
	newBytes, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatalf("read new: %v", err)
	}
	if string(oldBytes) != oldSrc {
		t.Fatalf("old file changed:\n%s", oldBytes)
	}
	if !strings.Contains(string(newBytes), "Edited new sentence.") {
		t.Fatalf("new file was not edited:\n%s", newBytes)
	}
	if !strings.Contains(string(m.Review.New.Source), "Edited new sentence.") {
		t.Fatalf("review did not reload edited new source")
	}
}

func TestDiffEditKeysRefuseWithoutAllowModifications(t *testing.T) {
	m, oldPath, newPath := editableDiffModel(t, paragraphDoc("Old sentence."), paragraphDoc("New sentence."))
	m.RequestedAllowMods = false
	m.AllowModifications = false

	beforeOld := mustReadFile(t, oldPath)
	beforeNew := mustReadFile(t, newPath)
	m = pressKey(t, m, "e")
	if m.Status != "edit disabled; rerun with --allow-modifications" {
		t.Fatalf("inline status = %q", m.Status)
	}
	m = pressKey(t, m, "E")
	if m.Status != "edit disabled; rerun with --allow-modifications" {
		t.Fatalf("external status = %q", m.Status)
	}
	if got := mustReadFile(t, oldPath); got != beforeOld {
		t.Fatalf("old file changed")
	}
	if got := mustReadFile(t, newPath); got != beforeNew {
		t.Fatalf("new file changed")
	}
}

func TestDiffDeletedOnlyRowRefusesEdit(t *testing.T) {
	m, _, _ := editableDiffModel(t, paragraphDoc("Old sentence."), paragraphDoc("New sentence."))
	deleted := diffreview.Pair{
		ID:       "deleted",
		Status:   diffreview.Deleted,
		Old:      fixtureBlock("old-deleted", 3, "Deleted sentence."),
		OldIndex: 0,
		NewIndex: -1,
	}
	m.Review.Pairs = []diffreview.Pair{deleted}
	m.Review.ByID = map[string]*diffreview.Pair{"deleted": &m.Review.Pairs[0]}
	m.Cursor = 0
	if pair := m.CurrentPair(); pair == nil || pair.Status != diffreview.Deleted {
		t.Fatalf("current pair = %#v, want deleted", pair)
	}

	m = pressKey(t, m, "e")
	if m.Status != "deleted block has no new source to edit" {
		t.Fatalf("inline status = %q", m.Status)
	}
	m = pressKey(t, m, "E")
	if m.Status != "deleted block has no new source to edit" {
		t.Fatalf("external status = %q", m.Status)
	}
}

func TestDiffReadOnlyNewEndpointRefusesEdit(t *testing.T) {
	m, _, newPath := editableDiffModel(t, paragraphDoc("Old sentence."), paragraphDoc("New sentence."))
	m.Review.New.Kind = diffreview.GitBlob
	m.Review.New.Editable = false
	m.AllowModifications = false
	beforeNew := mustReadFile(t, newPath)

	m = pressKey(t, m, "e")
	if m.Status != "new endpoint is read-only; use --base REV path.tex from the branch you want to edit" {
		t.Fatalf("inline status = %q", m.Status)
	}
	if got := mustReadFile(t, newPath); got != beforeNew {
		t.Fatalf("new file changed")
	}
}

func TestDiffReloadAfterEditRecomputesPairsAndPreservesSidecarState(t *testing.T) {
	oldSrc := theoremDoc("Old theorem body.")
	newSrc := theoremDoc("New theorem body.")
	m, _, _ := editableDiffModel(t, oldSrc, newSrc)
	m.SourceLineCursor = 2
	pair := m.CurrentPair()
	if pair == nil || pair.ID != "thm:a" {
		t.Fatalf("current pair id = %v, want thm:a", pair)
	}
	m.Reviewed[pair.ID] = true
	m.ensureSidecar().SetReviewed(pair.ID, true)
	m.ensureSidecar().UpsertAnnotation(diffreview.AnnotationForPair(m.Review, pair, "keep note"))
	m.Annotations[pair.ID] = "keep note"

	next, _ := m.startLineEdit()
	m = next.(Model)
	m.LineEdit.TA.SetValue("Old theorem body.")
	next, _ = m.submitLineEdit()
	m = next.(Model)

	reloaded := m.Review.ByID["thm:a"]
	if reloaded == nil {
		t.Fatalf("reloaded review lost thm:a pair")
	}
	if reloaded.Status != diffreview.Unchanged {
		t.Fatalf("reloaded status = %s, want unchanged", reloaded.Status)
	}
	if !m.Reviewed["thm:a"] {
		t.Fatalf("reviewed state was not preserved")
	}
	if got := m.Annotations["thm:a"]; got != "keep note" {
		t.Fatalf("annotation = %q, want keep note", got)
	}
}

func TestDiffReloadAfterEditRemapsSidecarBaseWithoutAbsorbingUnsavedNotes(t *testing.T) {
	oldSrc := theoremDoc("Old theorem body.")
	newSrc := theoremDoc("New theorem body.")
	m, _, newPath := editableDiffModel(t, oldSrc, newSrc)
	pair := m.CurrentPair()
	if pair == nil || pair.ID != "thm:a" {
		t.Fatalf("current pair id = %v, want thm:a", pair)
	}
	base := diffreview.NewSidecar(m.Review)
	base.UpsertAnnotation(diffreview.AnnotationForPair(m.Review, pair, "base note"))
	m.Sidecar = diffreview.CloneSidecar(base)
	m.SidecarBase = diffreview.CloneSidecar(base)
	m.Annotations = m.Sidecar.AnnotationNotes()

	m.Sidecar.UpsertAnnotation(diffreview.AnnotationForPair(m.Review, pair, "user note"))
	m.Annotations[pair.ID] = "user note"
	if err := os.WriteFile(newPath, []byte(theoremDoc("Edited theorem body.")), 0o600); err != nil {
		t.Fatalf("write edited new source: %v", err)
	}

	m = m.reloadAfterEdit("source reloaded")

	if got := m.Sidecar.AnnotationNotes()["thm:a"]; got != "user note" {
		t.Fatalf("live annotation = %q, want unsaved user note", got)
	}
	if got := m.SidecarBase.AnnotationNotes()["thm:a"]; got != "base note" {
		t.Fatalf("base annotation = %q, want original base note", got)
	}
	if len(m.SidecarBase.Annotations) != 1 || !strings.Contains(m.SidecarBase.Annotations[0].SourceQuote, "Edited theorem body.") {
		t.Fatalf("base annotation was not remapped to edited source: %#v", m.SidecarBase.Annotations)
	}
}

func TestDiffUndoRedoApplyOnlyNewFile(t *testing.T) {
	oldSrc := paragraphDoc("Old sentence.")
	newSrc := paragraphDoc("New sentence.")
	m, oldPath, newPath := editableDiffModel(t, oldSrc, newSrc)

	next, _ := m.startLineEdit()
	m = next.(Model)
	m.LineEdit.TA.SetValue("Edited sentence.")
	next, _ = m.submitLineEdit()
	m = next.(Model)

	next, _ = m.undoEdit()
	m = next.(Model)
	if got := mustReadFile(t, oldPath); got != oldSrc {
		t.Fatalf("old changed after undo")
	}
	if got := mustReadFile(t, newPath); got != newSrc {
		t.Fatalf("new after undo = %q, want original new source", got)
	}

	next, _ = m.redoEdit()
	m = next.(Model)
	if got := mustReadFile(t, oldPath); got != oldSrc {
		t.Fatalf("old changed after redo")
	}
	if got := mustReadFile(t, newPath); !strings.Contains(got, "Edited sentence.") {
		t.Fatalf("new after redo = %q, want edited source", got)
	}
}

func editableDiffModel(t *testing.T, oldSrc, newSrc string) (Model, string, string) {
	t.Helper()
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.tex")
	newPath := filepath.Join(dir, "new.tex")
	if err := os.WriteFile(oldPath, []byte(oldSrc), 0o600); err != nil {
		t.Fatalf("write old: %v", err)
	}
	if err := os.WriteFile(newPath, []byte(newSrc), 0o600); err != nil {
		t.Fatalf("write new: %v", err)
	}
	oldEndpoint := diffreview.Endpoint{
		Kind:   diffreview.WorkingFile,
		Label:  "old",
		Spec:   oldPath,
		Path:   oldPath,
		Source: []byte(oldSrc),
	}
	newEndpoint := diffreview.Endpoint{
		Kind:     diffreview.WorkingFile,
		Label:    "new",
		Spec:     newPath,
		Path:     newPath,
		Editable: true,
		Source:   []byte(newSrc),
	}
	review, err := diffreview.BuildReview(oldEndpoint, newEndpoint)
	if err != nil {
		t.Fatalf("build review: %v", err)
	}
	m := New(review, Options{AllowModifications: true, RequestedAllowMods: true})
	return m, oldPath, newPath
}

func paragraphDoc(sentence string) string {
	return "\\section{A}\n\n" + sentence + "\n"
}

func theoremDoc(body string) string {
	return "\\begin{theorem}\\label{thm:a}\n" + body + "\n\\end{theorem}\n"
}

func containsString(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
