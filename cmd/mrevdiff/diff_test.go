package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lenis2000/mrevdiff/pkg/diffreview"
	"github.com/lenis2000/mrevdiff/pkg/diffui"
	"github.com/lenis2000/mrevdiff/pkg/format"
)

func withStubDiffTUI(t *testing.T) *tea.Model {
	t.Helper()
	return withStubDiffTUIFinal(t, nil)
}

func withStubDiffTUIFinal(t *testing.T, mutate func(diffui.Model) diffui.Model) *tea.Model {
	t.Helper()
	saved := runDiffTUI
	var captured tea.Model
	runDiffTUI = func(model tea.Model, _, _ io.Writer) (tea.Model, error) {
		captured = model
		if mutate != nil {
			if m, ok := model.(diffui.Model); ok {
				return mutate(m), nil
			}
		}
		return model, nil
	}
	t.Cleanup(func() { runDiffTUI = saved })
	return &captured
}

func capturedDiffModel(t *testing.T, captured *tea.Model) diffui.Model {
	t.Helper()
	if captured == nil || *captured == nil {
		t.Fatalf("expected runDiffTUI to be invoked")
	}
	m, ok := (*captured).(diffui.Model)
	if !ok {
		t.Fatalf("unexpected diff model type %T", *captured)
	}
	if m.Review == nil {
		t.Fatalf("expected review to be populated")
	}
	return m
}

func TestRunDiffMissingArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runDiff([]string{}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit 2, got %d", code)
	}
	msg := stderr.String()
	if !strings.Contains(msg, "missing endpoints") || !strings.Contains(msg, "usage: mrevdiff") {
		t.Fatalf("expected missing-endpoints usage message, got %q", msg)
	}
}

func TestRunDiffRejectsAmbiguousBaseCall(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runDiff([]string{"--base", "HEAD", "old.tex", "new.tex"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit 2, got %d", code)
	}
	if !strings.Contains(stderr.String(), "--base cannot be combined") {
		t.Fatalf("expected ambiguous --base message, got %q", stderr.String())
	}
}

func TestRunDiffBadRefReportsResolveError(t *testing.T) {
	repo := initDiffRepo(t)
	writeDiffFile(t, repo, "paper.tex", diffFixture("Base paragraph for bad ref."))
	gitDiff(t, repo, "add", "paper.tex")
	gitDiff(t, repo, "commit", "-m", "base")
	chdir(t, repo)

	var stdout, stderr bytes.Buffer
	code := runDiff([]string{"--base", "missing-ref", "--no-build", "--noconfig", "paper.tex"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	msg := stderr.String()
	if !strings.Contains(msg, "resolve --base endpoints") || !strings.Contains(msg, "git show") {
		t.Fatalf("expected git-show resolve error, got %q", msg)
	}
}

func TestRunDiffPrimaryBaseBuildsReview(t *testing.T) {
	repo := initDiffRepo(t)
	writeDiffFile(t, repo, "paper.tex", diffFixture("Base paragraph has enough shared words for matching."))
	gitDiff(t, repo, "add", "paper.tex")
	gitDiff(t, repo, "commit", "-m", "base")
	writeDiffFile(t, repo, "paper.tex", diffFixture("Dirty paragraph has enough shared words for matching."))
	chdir(t, repo)

	captured := withStubDiffTUI(t)
	var stdout, stderr bytes.Buffer
	code := runDiff([]string{"--base", "HEAD", "--no-build", "--noconfig", "--stdout=none", "paper.tex"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", code, stderr.String())
	}

	m := capturedDiffModel(t, captured)
	if m.Review.Old.Kind != diffreview.GitBlob {
		t.Fatalf("expected old endpoint to be git blob, got %s", m.Review.Old.Kind)
	}
	if m.Review.New.Kind != diffreview.WorkingFile {
		t.Fatalf("expected new endpoint to be working file, got %s", m.Review.New.Kind)
	}
	if !m.Review.New.Editable {
		t.Fatalf("expected working-tree new endpoint to be editable")
	}
	if m.AllowModifications || m.RequestedAllowMods {
		t.Fatalf("expected edit permission to be disabled without --allow-modifications")
	}
	if !m.NoBuild {
		t.Fatalf("expected --no-build to be captured")
	}
	if m.Config == nil {
		t.Fatalf("expected config to be loaded")
	}
	if m.Review.Stats.Total == 0 {
		t.Fatalf("expected semantic pairs to be generated")
	}
	if !strings.Contains(string(m.Review.Old.Source), "Base paragraph") {
		t.Fatalf("old source was not read from HEAD")
	}
	if !strings.Contains(string(m.Review.New.Source), "Dirty paragraph") {
		t.Fatalf("new source was not read from working tree")
	}
}

func TestRunDiffExplicitOldNewBuildsReview(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.tex")
	newPath := filepath.Join(dir, "new.tex")
	if err := os.WriteFile(oldPath, []byte(diffFixture("Original explicit endpoint paragraph.")), 0o600); err != nil {
		t.Fatalf("write old: %v", err)
	}
	if err := os.WriteFile(newPath, []byte(diffFixture("Updated explicit endpoint paragraph.")), 0o600); err != nil {
		t.Fatalf("write new: %v", err)
	}

	captured := withStubDiffTUI(t)
	var stdout, stderr bytes.Buffer
	code := runDiff([]string{"--no-build", "--noconfig", "--stdout=none", oldPath, newPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", code, stderr.String())
	}

	m := capturedDiffModel(t, captured)
	if m.Review.Old.Spec != oldPath {
		t.Fatalf("expected old spec %q, got %q", oldPath, m.Review.Old.Spec)
	}
	if !m.Review.Old.Materialized || m.Review.Old.Path == oldPath {
		t.Fatalf("expected old filesystem endpoint to use a stable snapshot, got materialized=%v path=%q", m.Review.Old.Materialized, m.Review.Old.Path)
	}
	if m.Review.New.Spec != newPath {
		t.Fatalf("expected new spec %q, got %q", newPath, m.Review.New.Spec)
	}
	if !m.Review.New.Editable {
		t.Fatalf("expected filesystem new endpoint to be editable")
	}
}

func TestRunDiffSavesSidecarAndEmitsMarkdown(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.tex")
	newPath := filepath.Join(dir, "new.tex")
	sidecarPath := filepath.Join(dir, "review.md")
	if err := os.WriteFile(oldPath, []byte(diffFixture("Original paragraph with enough shared words for matching.")), 0o600); err != nil {
		t.Fatalf("write old: %v", err)
	}
	if err := os.WriteFile(newPath, []byte(diffFixture("Updated paragraph with enough shared words for matching.")), 0o600); err != nil {
		t.Fatalf("write new: %v", err)
	}

	var finalPairID string
	_ = withStubDiffTUIFinal(t, func(m diffui.Model) diffui.Model {
		pair := m.CurrentPair()
		if pair == nil {
			return m
		}
		finalPairID = pair.ID
		m.Reviewed[pair.ID] = true
		m.Sidecar.SetReviewed(pair.ID, true)
		m.Annotations[pair.ID] = "final note"
		m.Sidecar.UpsertAnnotation(diffreview.AnnotationForPair(m.Review, pair, "final note"))
		return m
	})
	var stdout, stderr bytes.Buffer
	code := runDiff([]string{"--no-build", "--noconfig", "--sidecar", sidecarPath, "--stdout=md", oldPath, newPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "Old: "+oldPath) || !strings.Contains(out, "New: "+newPath) ||
		!strings.Contains(out, "- changed") || !strings.Contains(out, "final note") {
		t.Fatalf("stdout markdown missing diff summary:\n%s", out)
	}
	saved, err := diffreview.LoadSidecar(sidecarPath)
	if err != nil {
		t.Fatalf("load sidecar: %v", err)
	}
	if saved.OldSpec != oldPath || saved.NewSpec != newPath {
		t.Fatalf("saved sidecar specs = old %q new %q", saved.OldSpec, saved.NewSpec)
	}
	if finalPairID == "" || !saved.ReviewedSet()[finalPairID] {
		t.Fatalf("saved sidecar did not include final reviewed state: %#v", saved.Reviewed)
	}
	if notes := saved.AnnotationNotes(); notes[finalPairID] != "final note" {
		t.Fatalf("saved sidecar did not include final annotation state: %#v", notes)
	}
	if len(saved.Pairs) == 0 {
		t.Fatalf("saved sidecar did not include pair summaries")
	}
}

func TestRunDiffMergesConcurrentSidecarWithLiveChanges(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.tex")
	newPath := filepath.Join(dir, "new.tex")
	sidecarPath := filepath.Join(dir, "review.md")
	if err := os.WriteFile(oldPath, []byte(diffFixture("Original paragraph with enough shared words for merging.")), 0o600); err != nil {
		t.Fatalf("write old: %v", err)
	}
	if err := os.WriteFile(newPath, []byte(diffFixture("Updated paragraph with enough shared words for merging.")), 0o600); err != nil {
		t.Fatalf("write new: %v", err)
	}

	var finalPairID string
	_ = withStubDiffTUIFinal(t, func(m diffui.Model) diffui.Model {
		pair := m.CurrentPair()
		if pair == nil {
			return m
		}
		finalPairID = pair.ID
		m.Sidecar.UpsertAnnotation(diffreview.AnnotationForPair(m.Review, pair, "memory note"))
		if err := diffreview.SaveSidecar(sidecarPath, &diffreview.Sidecar{
			Annotations: []diffreview.Annotation{{PairID: "external-only", Note: "external note"}},
		}); err != nil {
			t.Fatalf("save concurrent sidecar: %v", err)
		}
		return m
	})
	var stdout, stderr bytes.Buffer
	code := runDiff([]string{"--no-build", "--noconfig", "--sidecar", sidecarPath, "--stdout=none", oldPath, newPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", code, stderr.String())
	}

	saved, err := diffreview.LoadSidecar(sidecarPath)
	if err != nil {
		t.Fatalf("load sidecar: %v", err)
	}
	notes := saved.AnnotationNotes()
	if notes["external-only"] != "external note" {
		t.Fatalf("concurrent disk annotation was not preserved: %#v", notes)
	}
	if finalPairID == "" || notes[finalPairID] != "memory note" {
		t.Fatalf("in-session annotation was not preserved: pair=%q notes=%#v", finalPairID, notes)
	}
}

func TestRunDiffUsesRemappedSidecarBaseForConcurrentDeletes(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.tex")
	newPath := filepath.Join(dir, "new.tex")
	sidecarPath := filepath.Join(dir, "review.md")
	oldSrc := diffFixture("Original paragraph with enough shared words for remap merging.")
	newSrc := diffFixture("Updated paragraph with enough shared words for remap merging.")
	if err := os.WriteFile(oldPath, []byte(oldSrc), 0o600); err != nil {
		t.Fatalf("write old: %v", err)
	}
	if err := os.WriteFile(newPath, []byte(newSrc), 0o600); err != nil {
		t.Fatalf("write new: %v", err)
	}
	review, err := diffreview.BuildReview(
		diffreview.Endpoint{Kind: diffreview.WorkingFile, Spec: oldPath, Path: oldPath, Source: []byte(oldSrc)},
		diffreview.Endpoint{Kind: diffreview.WorkingFile, Spec: newPath, Path: newPath, Source: []byte(newSrc)},
	)
	if err != nil {
		t.Fatalf("build review: %v", err)
	}
	var pairID string
	for _, pair := range review.Pairs {
		if pair.Status == diffreview.Changed && pair.New != nil {
			pairID = pair.ID
			break
		}
	}
	if pairID == "" {
		t.Fatalf("missing changed pair in fixture review")
	}
	if err := diffreview.SaveSidecar(sidecarPath, &diffreview.Sidecar{
		Annotations: []diffreview.Annotation{{PairID: pairID, Status: "changed", Side: "new", SourceQuote: "initial quote", Note: "delete me"}},
	}); err != nil {
		t.Fatalf("save initial sidecar: %v", err)
	}

	_ = withStubDiffTUIFinal(t, func(m diffui.Model) diffui.Model {
		m.Sidecar.UpsertAnnotation(diffreview.Annotation{PairID: pairID, Status: "changed", Side: "new", SourceQuote: "remapped quote", Note: "delete me"})
		m.SidecarBase = diffreview.CloneSidecar(m.Sidecar)
		if err := diffreview.SaveSidecar(sidecarPath, &diffreview.Sidecar{}); err != nil {
			t.Fatalf("save concurrent sidecar: %v", err)
		}
		future := time.Now().Add(2 * time.Second)
		if err := os.Chtimes(sidecarPath, future, future); err != nil {
			t.Fatalf("touch concurrent sidecar: %v", err)
		}
		return m
	})
	var stdout, stderr bytes.Buffer
	code := runDiff([]string{"--no-build", "--noconfig", "--sidecar", sidecarPath, "--stdout=none", oldPath, newPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", code, stderr.String())
	}

	saved, err := diffreview.LoadSidecar(sidecarPath)
	if err != nil {
		t.Fatalf("load sidecar: %v", err)
	}
	if notes := saved.AnnotationNotes(); notes[pairID] != "" {
		t.Fatalf("concurrent delete was resurrected: %#v", notes)
	}
}

func TestRunDiffLoadsFmtReportIssuesForNewPairs(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.tex")
	newPath := filepath.Join(dir, "new.tex")
	if err := os.WriteFile(oldPath, []byte(diffFixture("Original paragraph with enough shared words for matching.")), 0o600); err != nil {
		t.Fatalf("write old: %v", err)
	}
	if err := os.WriteFile(newPath, []byte(diffFixture("Updated paragraph with enough shared words for matching.")), 0o600); err != nil {
		t.Fatalf("write new: %v", err)
	}
	if err := format.WriteReport(format.ReportPath(newPath), format.Report{
		File: filepath.Base(newPath),
		Diags: []format.ReportDiag{{
			RuleID:  "lint.todo-marker",
			Line:    4,
			Message: "TODO marker",
		}},
	}); err != nil {
		t.Fatalf("write report: %v", err)
	}

	captured := withStubDiffTUI(t)
	var stdout, stderr bytes.Buffer
	code := runDiff([]string{"--no-build", "--noconfig", "--stdout=none", oldPath, newPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", code, stderr.String())
	}

	m := capturedDiffModel(t, captured)
	if len(m.Issues) == 0 {
		t.Fatalf("expected fmt-report diagnostics mapped to diff pair IDs")
	}
	for pairID, issues := range m.Issues {
		if m.Review.ByID[pairID] == nil {
			t.Fatalf("issue keyed by non-pair ID %q: %#v", pairID, m.Issues)
		}
		if !strings.Contains(strings.Join(issues, "\n"), "lint.todo-marker") {
			t.Fatalf("unexpected issue text for %q: %#v", pairID, issues)
		}
		return
	}
}

func TestRunDiffAllowModificationsTogglesEditPermission(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.tex")
	newPath := filepath.Join(dir, "new.tex")
	if err := os.WriteFile(oldPath, []byte(diffFixture("Old editable endpoint paragraph.")), 0o600); err != nil {
		t.Fatalf("write old: %v", err)
	}
	if err := os.WriteFile(newPath, []byte(diffFixture("New editable endpoint paragraph.")), 0o600); err != nil {
		t.Fatalf("write new: %v", err)
	}

	captured := withStubDiffTUI(t)
	var stdout, stderr bytes.Buffer
	code := runDiff([]string{"--allow-modifications", "--no-build", "--noconfig", "--stdout=none", oldPath, newPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", code, stderr.String())
	}

	m := capturedDiffModel(t, captured)
	if !m.RequestedAllowMods {
		t.Fatalf("expected requested allow-modifications flag to be captured")
	}
	if !m.AllowModifications {
		t.Fatalf("expected effective edit permission for filesystem new endpoint")
	}
}

func TestRunDiffReadOnlyNewEndpointDisablesEditPermission(t *testing.T) {
	repo := initDiffRepo(t)
	writeDiffFile(t, repo, "paper.tex", diffFixture("Committed read-only endpoint paragraph."))
	gitDiff(t, repo, "add", "paper.tex")
	gitDiff(t, repo, "commit", "-m", "base")
	chdir(t, repo)

	captured := withStubDiffTUI(t)
	var stdout, stderr bytes.Buffer
	code := runDiff([]string{"--allow-modifications", "--no-build", "--noconfig", "--stdout=none", "HEAD:paper.tex", "HEAD:paper.tex"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", code, stderr.String())
	}

	m := capturedDiffModel(t, captured)
	if m.Review.New.Editable {
		t.Fatalf("expected git-blob new endpoint to be read-only")
	}
	if !m.RequestedAllowMods {
		t.Fatalf("expected requested allow-modifications flag to be captured")
	}
	if m.AllowModifications {
		t.Fatalf("expected effective edit permission to stay disabled")
	}
	if !strings.Contains(m.Status, "new endpoint is read-only") {
		t.Fatalf("expected read-only status message, got %q", m.Status)
	}
}

func TestRunDiffOpenCompareFlagIsCaptured(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.tex")
	newPath := filepath.Join(dir, "new.tex")
	if err := os.WriteFile(oldPath, []byte(diffFixture("Old open compare endpoint paragraph.")), 0o600); err != nil {
		t.Fatalf("write old: %v", err)
	}
	if err := os.WriteFile(newPath, []byte(diffFixture("New open compare endpoint paragraph.")), 0o600); err != nil {
		t.Fatalf("write new: %v", err)
	}

	captured := withStubDiffTUI(t)
	var stdout, stderr bytes.Buffer
	code := runDiff([]string{"--open-compare", "--no-build", "--noconfig", "--stdout=none", oldPath, newPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", code, stderr.String())
	}

	m := capturedDiffModel(t, captured)
	if !m.OpenCompare {
		t.Fatalf("expected --open-compare to be captured in the diff model")
	}
}

func diffFixture(paragraph string) string {
	return "\\documentclass{amsart}\n" +
		"\\begin{document}\n" +
		"\\section{Intro}\n" +
		paragraph + "\n" +
		"\\end{document}\n"
}

func initDiffRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitDiff(t, dir, "init")
	gitDiff(t, dir, "config", "user.name", "Test User")
	gitDiff(t, dir, "config", "user.email", "test@example.com")
	gitDiff(t, dir, "config", "commit.gpgsign", "false")
	return dir
}

func writeDiffFile(t *testing.T, repo, relPath, content string) {
	t.Helper()
	path := filepath.Join(repo, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", relPath, err)
	}
}

func gitDiff(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
