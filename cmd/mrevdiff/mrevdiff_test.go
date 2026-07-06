package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mrevdiff/pkg/diffreview"
	"mrevdiff/pkg/diffui"
)

// chdir changes the working directory for the duration of t, restoring
// the original cwd in t.Cleanup.
func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %q: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

func TestVersionFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runDiff([]string{"--version"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if !strings.Contains(stdout.String(), "mrevdiff "+version) {
		t.Fatalf("expected version banner, got %q", stdout.String())
	}
}

// TestBareFileDefaultsToBaseHEAD covers the revdiff-style convenience form:
// `mrevdiff paper.tex` reviews the uncommitted changes of paper.tex against
// HEAD.
func TestBareFileDefaultsToBaseHEAD(t *testing.T) {
	repo := initDiffRepo(t)
	writeDiffFile(t, repo, "paper.tex", diffFixture("Base paragraph for the bare form."))
	gitDiff(t, repo, "add", "paper.tex")
	gitDiff(t, repo, "commit", "-m", "base")
	writeDiffFile(t, repo, "paper.tex", diffFixture("Edited paragraph for the bare form."))
	chdir(t, repo)

	captured := withStubDiffTUI(t)
	var stdout, stderr bytes.Buffer
	code := runDiff([]string{"--no-build", "--noconfig", "--stdout=none", "--no-history", "paper.tex"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", code, stderr.String())
	}
	m := capturedDiffModel(t, captured)
	if m.Review.Old.Kind != diffreview.GitBlob {
		t.Fatalf("bare form should resolve OLD as a git blob, got kind %v (spec %q)", m.Review.Old.Kind, m.Review.Old.Spec)
	}
	if !strings.HasPrefix(m.Review.Old.Spec, "HEAD:") {
		t.Fatalf("bare form should default to HEAD, got old spec %q", m.Review.Old.Spec)
	}
}

// TestQDiscardSkipsSidecarAndEmit checks the cmd-side half of the Q
// contract: a discarded session writes no sidecar and emits nothing.
func TestQDiscardSkipsSidecarAndEmit(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.tex")
	newPath := filepath.Join(dir, "new.tex")
	if err := os.WriteFile(oldPath, []byte(diffFixture("Old paragraph for discard.")), 0o600); err != nil {
		t.Fatalf("write old: %v", err)
	}
	if err := os.WriteFile(newPath, []byte(diffFixture("New paragraph for discard.")), 0o600); err != nil {
		t.Fatalf("write new: %v", err)
	}
	sidecarPath := filepath.Join(dir, "sidecar.md")

	withStubDiffTUIFinal(t, func(m diffui.Model) diffui.Model {
		m.Discarded = true
		return m
	})
	var stdout, stderr bytes.Buffer
	code := runDiff([]string{"--no-build", "--noconfig", "--sidecar", sidecarPath, "--stdout=md", oldPath, newPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("discard must emit nothing, got %q", stdout.String())
	}
	if _, err := os.Stat(sidecarPath); !os.IsNotExist(err) {
		t.Fatalf("discard must not write the sidecar (stat err = %v)", err)
	}
}

// TestExitCodeOnAnnotations mirrors revdiff's launcher contract: exit 10
// when annotations exist and the flag is set, 0 otherwise.
func TestExitCodeOnAnnotations(t *testing.T) {
	makeEndpoints := func(t *testing.T) (string, string, string) {
		dir := t.TempDir()
		oldPath := filepath.Join(dir, "old.tex")
		newPath := filepath.Join(dir, "new.tex")
		if err := os.WriteFile(oldPath, []byte(diffFixture("Old paragraph for exit code.")), 0o600); err != nil {
			t.Fatalf("write old: %v", err)
		}
		if err := os.WriteFile(newPath, []byte(diffFixture("New paragraph for exit code.")), 0o600); err != nil {
			t.Fatalf("write new: %v", err)
		}
		return dir, oldPath, newPath
	}
	annotate := func(m diffui.Model) diffui.Model {
		m.Sidecar.Annotations = append(m.Sidecar.Annotations, diffreview.Annotation{
			PairID: "p1",
			Note:   "tighten this paragraph",
		})
		return m
	}

	t.Run("flag set with annotations exits 10", func(t *testing.T) {
		dir, oldPath, newPath := makeEndpoints(t)
		withStubDiffTUIFinal(t, annotate)
		var stdout, stderr bytes.Buffer
		code := runDiff([]string{"--exit-code-on-annotations", "--no-build", "--noconfig",
			"--sidecar", filepath.Join(dir, "s.md"), "--stdout=none", "--no-history", oldPath, newPath}, &stdout, &stderr)
		if code != exitCodeAnnotations {
			t.Fatalf("expected exit %d, got %d (stderr=%q)", exitCodeAnnotations, code, stderr.String())
		}
	})
	t.Run("flag unset with annotations exits 0", func(t *testing.T) {
		dir, oldPath, newPath := makeEndpoints(t)
		withStubDiffTUIFinal(t, annotate)
		var stdout, stderr bytes.Buffer
		code := runDiff([]string{"--no-build", "--noconfig",
			"--sidecar", filepath.Join(dir, "s.md"), "--stdout=none", "--no-history", oldPath, newPath}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected exit 0, got %d (stderr=%q)", code, stderr.String())
		}
	})
	t.Run("flag set without annotations exits 0", func(t *testing.T) {
		dir, oldPath, newPath := makeEndpoints(t)
		withStubDiffTUI(t)
		var stdout, stderr bytes.Buffer
		code := runDiff([]string{"--exit-code-on-annotations", "--no-build", "--noconfig",
			"--sidecar", filepath.Join(dir, "s.md"), "--stdout=none", "--no-history", oldPath, newPath}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected exit 0, got %d (stderr=%q)", code, stderr.String())
		}
	})
}

// TestHistoryAutoSave checks the silent history net: an annotated quit
// leaves a markdown snapshot under <history-dir>/<project>/, and a
// no-annotation quit leaves nothing.
func TestHistoryAutoSave(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.tex")
	newPath := filepath.Join(dir, "new.tex")
	if err := os.WriteFile(oldPath, []byte(diffFixture("Old paragraph for history.")), 0o600); err != nil {
		t.Fatalf("write old: %v", err)
	}
	if err := os.WriteFile(newPath, []byte(diffFixture("New paragraph for history.")), 0o600); err != nil {
		t.Fatalf("write new: %v", err)
	}
	histDir := filepath.Join(dir, "hist")

	withStubDiffTUIFinal(t, func(m diffui.Model) diffui.Model {
		m.Sidecar.Annotations = append(m.Sidecar.Annotations, diffreview.Annotation{
			PairID: "p1",
			Note:   "history note",
		})
		return m
	})
	var stdout, stderr bytes.Buffer
	code := runDiff([]string{"--no-build", "--noconfig", "--history-dir", histDir,
		"--sidecar", filepath.Join(dir, "s.md"), "--stdout=none", oldPath, newPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", code, stderr.String())
	}
	entries, err := filepath.Glob(filepath.Join(histDir, "*", "*.md"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected exactly one history file, got %v (err=%v)", entries, err)
	}
	data, err := os.ReadFile(entries[0])
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	if !strings.Contains(string(data), "history note") {
		t.Fatalf("history file should contain the annotation, got %q", string(data))
	}

	// Second run without annotations must not add a history file.
	withStubDiffTUI(t)
	code = runDiff([]string{"--no-build", "--noconfig", "--history-dir", histDir,
		"--sidecar", filepath.Join(dir, "s2.md"), "--stdout=none", oldPath, newPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", code, stderr.String())
	}
	entries, _ = filepath.Glob(filepath.Join(histDir, "*", "*.md"))
	if len(entries) != 1 {
		t.Fatalf("no-annotation quit must not save history, got %v", entries)
	}
}
