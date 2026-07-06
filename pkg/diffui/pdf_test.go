package diffui

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mrevdiff/pkg/diffreview"
	"mrevdiff/pkg/pdf"
	"mrevdiff/pkg/synctex"
)

func TestPrepareNewPDFUsesNewFilesystemEndpoint(t *testing.T) {
	dir := t.TempDir()
	oldDir := filepath.Join(dir, "old")
	newDir := filepath.Join(dir, "new")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatalf("mkdir old: %v", err)
	}
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		t.Fatalf("mkdir new: %v", err)
	}
	oldPath := filepath.Join(oldDir, "paper.tex")
	newPath := filepath.Join(newDir, "paper.tex")
	if err := os.WriteFile(oldPath, []byte("\\bye\n"), 0o600); err != nil {
		t.Fatalf("write old: %v", err)
	}
	if err := os.WriteFile(newPath, []byte("\\bye\n"), 0o600); err != nil {
		t.Fatalf("write new: %v", err)
	}
	marker := filepath.Join(dir, "built.txt")
	t.Setenv("MARKER", marker)
	review := &diffreview.Review{
		Old: diffreview.Endpoint{Kind: diffreview.WorkingFile, Path: oldPath},
		New: diffreview.Endpoint{Kind: diffreview.WorkingFile, Path: newPath, Editable: true},
	}

	_, err := PrepareNewPDF(review, PDFOptions{
		BuildCmd: `printf '%s' "$PWD/$MREVDIFF_BASENAME" > "$MARKER"`,
	})
	if err != nil {
		t.Fatalf("prepare pdf: %v", err)
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	want := filepath.Join(newDir, "paper")
	if got := string(data); !samePath(got, want) {
		t.Fatalf("build ran for %q, want new endpoint %q", got, want)
	}
}

func TestPrepareNewPDFNoBuildSkipsBuildCommand(t *testing.T) {
	review, _, newPath := pdfReviewFixture(t)
	marker := filepath.Join(t.TempDir(), "built.txt")
	t.Setenv("MARKER", marker)

	artifacts, err := PrepareNewPDF(review, PDFOptions{
		NoBuild:  true,
		BuildCmd: `printf built > "$MARKER"`,
	})
	if err != nil {
		t.Fatalf("prepare pdf: %v", err)
	}
	if artifacts == nil {
		t.Fatalf("expected artifacts")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("build command should not have run for %s", newPath)
	}
}

func TestPrepareNewPDFBuildFailureHonorsDraft(t *testing.T) {
	review, _, _ := pdfReviewFixture(t)
	var stderr bytes.Buffer

	_, err := PrepareNewPDF(review, PDFOptions{BuildCmd: "false", Stderr: &stderr})
	if err == nil {
		t.Fatalf("expected non-draft build failure")
	}

	artifacts, err := PrepareNewPDF(review, PDFOptions{BuildCmd: "false", Draft: true})
	if err != nil {
		t.Fatalf("draft build failure should not abort: %v", err)
	}
	if artifacts == nil || !artifacts.BuildStale || !strings.Contains(artifacts.Status, "build:") {
		t.Fatalf("draft artifacts = %#v, want stale build warning", artifacts)
	}
}

func TestDiffPDFReloadUsesFreshArtifactsAfterBuildWarning(t *testing.T) {
	_, _, newPath := pdfReviewFixture(t)
	samplePDF, err := filepath.Abs(filepath.Join("..", "..", "testdata", "sample.pdf"))
	if err != nil {
		t.Fatalf("sample pdf path: %v", err)
	}
	sampleSyncTeX, err := filepath.Abs(filepath.Join("..", "..", "testdata", "sample.synctex.gz"))
	if err != nil {
		t.Fatalf("sample synctex path: %v", err)
	}
	t.Setenv("SAMPLE_PDF", samplePDF)
	t.Setenv("SAMPLE_SYNCTEX", sampleSyncTeX)
	cmd := `cp "$SAMPLE_PDF" "$MREVDIFF_BASENAME.pdf"; cp "$SAMPLE_SYNCTEX" "$MREVDIFF_BASENAME.synctex.gz"; false`

	msg := performDiffPDFReload(newPath, 1, nil, cmd, true)
	if msg.NewPDF != nil {
		defer func() { _ = msg.NewPDF.Close() }()
	}
	if msg.BuildStale {
		t.Fatalf("fresh artifacts after build warning were marked stale: %#v", msg)
	}
	if msg.NewPDF == nil || msg.NewSyncTeX == nil {
		t.Fatalf("fresh artifacts were not opened: %#v", msg)
	}
	if !strings.Contains(msg.Status, "rebuild failed") {
		t.Fatalf("expected warning status, got %q", msg.Status)
	}
}

func TestDiffPDFReloadDoesNotLoadAuxWhenArtifactsAreStale(t *testing.T) {
	_, _, newPath := pdfReviewFixture(t)
	auxPath := strings.TrimSuffix(newPath, filepath.Ext(newPath)) + ".aux"
	if err := os.WriteFile(auxPath, []byte("\\newlabel{eq:x}{{1}{1}}\n"), 0o600); err != nil {
		t.Fatalf("write aux: %v", err)
	}

	msg := performDiffPDFReload(newPath, 1, nil, "false", true)
	if !msg.BuildStale {
		t.Fatalf("expected stale build after failed reload without artifacts")
	}
	if msg.Aux != nil || msg.BBL != nil {
		t.Fatalf("stale reload loaded build metadata: aux=%#v bbl=%#v", msg.Aux, msg.BBL)
	}
}

func TestDiffPDFReloadSuccessfulBuildWithoutArtifactsIsStale(t *testing.T) {
	_, _, newPath := pdfReviewFixture(t)

	msg := performDiffPDFReload(newPath, 1, nil, "true", true)
	if !msg.BuildStale {
		t.Fatalf("successful build without PDF artifacts should be stale: %#v", msg)
	}
	if msg.NewPDF != nil || msg.NewSyncTeX != nil {
		t.Fatalf("missing artifact reload unexpectedly opened handles: %#v", msg)
	}
	if !strings.Contains(msg.Status, "new PDF not loaded") {
		t.Fatalf("expected missing PDF status, got %q", msg.Status)
	}
}

func TestApplyPDFReloadClearsOldArtifactsWhenReloadHasNoPair(t *testing.T) {
	samplePDF, err := filepath.Abs(filepath.Join("..", "..", "testdata", "sample.pdf"))
	if err != nil {
		t.Fatalf("sample pdf path: %v", err)
	}
	sampleSyncTeX, err := filepath.Abs(filepath.Join("..", "..", "testdata", "sample.synctex.gz"))
	if err != nil {
		t.Fatalf("sample synctex path: %v", err)
	}
	oldPDF, err := pdf.Open(samplePDF)
	if err != nil {
		t.Fatalf("open sample pdf: %v", err)
	}
	oldSyncTeX, err := synctex.Open(sampleSyncTeX)
	if err != nil {
		_ = oldPDF.Close()
		t.Fatalf("open sample synctex: %v", err)
	}

	m := New(fixtureReview(), Options{PDF: oldPDF, Synctex: oldSyncTeX, KittyAvailable: true})
	m.pdfReloadGen = 3
	out, _ := m.applyPDFReload(diffPDFReloadMsg{
		Generation: 3,
		OldPDF:     oldPDF,
	})
	if out.PDF != nil {
		_ = out.PDF.Close()
		t.Fatalf("expected stale PDF handle to be cleared")
	}
	if out.Synctex != nil {
		t.Fatalf("expected stale SyncTeX handle to be cleared")
	}
	if out.PDFStatus != "(new PDF not loaded)" {
		t.Fatalf("expected missing PDF status, got %q", out.PDFStatus)
	}
}

func TestPrepareNewPDFSkipsBuildWhenLmkfIsWatching(t *testing.T) {
	review, _, newPath := pdfReviewFixture(t)
	statusFile := writeLmkfStatus(t, newPath)
	t.Cleanup(func() { _ = os.Remove(statusFile) })
	marker := filepath.Join(t.TempDir(), "built.txt")
	t.Setenv("MARKER", marker)

	artifacts, err := PrepareNewPDF(review, PDFOptions{BuildCmd: `printf built > "$MARKER"`})
	if err != nil {
		t.Fatalf("prepare pdf: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("build command should not run while lmkf is active")
	}
	if artifacts == nil || !strings.Contains(artifacts.Status, "lmkf is building") {
		t.Fatalf("lmkf status missing from artifacts: %#v", artifacts)
	}
}

func TestDiffPDFReloadWaitsForLmkfAndOpensFreshArtifacts(t *testing.T) {
	_, _, newPath := pdfReviewFixture(t)
	statusFile := writeLmkfStatus(t, newPath)
	t.Cleanup(func() { _ = os.Remove(statusFile) })
	installSampleArtifacts(t, newPath)
	writeLmkfLog(t, newPath, "Here is how much of TeX's memory you used")
	markLmkfFilesFresh(t, newPath)

	msg := performDiffPDFReload(newPath, 1, nil, `printf should-not-run > forbidden`, true)
	if msg.NewPDF != nil {
		defer func() { _ = msg.NewPDF.Close() }()
	}
	if msg.BuildStale {
		t.Fatalf("lmkf-fresh reload was marked stale: %#v", msg)
	}
	if msg.NewPDF == nil || msg.NewSyncTeX == nil {
		t.Fatalf("lmkf-fresh artifacts were not opened: %#v", msg)
	}
	if msg.Status != "lmkf rebuild ok" {
		t.Fatalf("status = %q, want lmkf rebuild ok", msg.Status)
	}
}

func TestDiffPDFReloadReportsLmkfErrors(t *testing.T) {
	_, _, newPath := pdfReviewFixture(t)
	statusFile := writeLmkfStatus(t, newPath)
	t.Cleanup(func() { _ = os.Remove(statusFile) })
	installSampleArtifacts(t, newPath)
	writeLmkfLog(t, newPath, "! Undefined control sequence\nHere is how much of TeX's memory you used")
	markLmkfFilesFresh(t, newPath)

	msg := performDiffPDFReload(newPath, 1, nil, "true", true)
	if msg.NewPDF != nil {
		defer func() { _ = msg.NewPDF.Close() }()
	}
	if !msg.BuildStale {
		t.Fatalf("lmkf error should mark build stale: %#v", msg)
	}
	if !strings.Contains(msg.Status, "lmkf rebuild error") || !strings.Contains(msg.Status, "Undefined control sequence") {
		t.Fatalf("expected lmkf error status, got %q", msg.Status)
	}
}

func TestNewEndpointBuildPathRejectsGitBlob(t *testing.T) {
	review := &diffreview.Review{
		New: diffreview.Endpoint{Kind: diffreview.GitBlob, Path: "/tmp/materialized.tex"},
	}
	if got, ok := newEndpointBuildPath(review); ok || got != "" {
		t.Fatalf("git blob build path = %q, %v; want no build path", got, ok)
	}
}

func TestPDFPaneDeletedPairShowsPlaceholder(t *testing.T) {
	review := fixtureReview()
	m := New(review, Options{KittyAvailable: true})
	m.Cursor = pairIndexByID(review, "deleted")
	m.PDFImage = "stale-image"

	body := m.pdfPaneBody()
	if !strings.Contains(body, deletedPDFPlaceholder) {
		t.Fatalf("deleted PDF placeholder missing from %q", body)
	}
	if strings.Contains(body, "stale-image") {
		t.Fatalf("deleted pair should clear stale image, got %q", body)
	}
}

func samePath(a, b string) bool {
	if a == b {
		return true
	}
	adir, aerr := filepath.EvalSymlinks(filepath.Dir(a))
	bdir, berr := filepath.EvalSymlinks(filepath.Dir(b))
	return aerr == nil && berr == nil && adir == bdir && filepath.Base(a) == filepath.Base(b)
}

func installSampleArtifacts(t *testing.T, texPath string) {
	t.Helper()
	samplePDF, err := filepath.Abs(filepath.Join("..", "..", "testdata", "sample.pdf"))
	if err != nil {
		t.Fatalf("sample pdf path: %v", err)
	}
	sampleSyncTeX, err := filepath.Abs(filepath.Join("..", "..", "testdata", "sample.synctex.gz"))
	if err != nil {
		t.Fatalf("sample synctex path: %v", err)
	}
	base := strings.TrimSuffix(texPath, filepath.Ext(texPath))
	pdfData, err := os.ReadFile(samplePDF)
	if err != nil {
		t.Fatalf("read sample pdf: %v", err)
	}
	if err := os.WriteFile(base+".pdf", pdfData, 0o600); err != nil {
		t.Fatalf("write sample pdf: %v", err)
	}
	sxData, err := os.ReadFile(sampleSyncTeX)
	if err != nil {
		t.Fatalf("read sample synctex: %v", err)
	}
	if err := os.WriteFile(base+".synctex.gz", sxData, 0o600); err != nil {
		t.Fatalf("write sample synctex: %v", err)
	}
}

func writeLmkfLog(t *testing.T, texPath, body string) {
	t.Helper()
	logPath := strings.TrimSuffix(texPath, filepath.Ext(texPath)) + ".log"
	if err := os.WriteFile(logPath, []byte(body+"\n"), 0o600); err != nil {
		t.Fatalf("write lmkf log: %v", err)
	}
}

func markLmkfFilesFresh(t *testing.T, texPath string) {
	t.Helper()
	base := strings.TrimSuffix(texPath, filepath.Ext(texPath))
	old := time.Now().Add(-10 * time.Second)
	fresh := time.Now().Add(10 * time.Second)
	if err := os.Chtimes(texPath, old, old); err != nil {
		t.Fatalf("chtimes tex: %v", err)
	}
	for _, path := range []string{base + ".log", base + ".pdf", base + ".synctex.gz"} {
		if err := os.Chtimes(path, fresh, fresh); err != nil {
			t.Fatalf("chtimes %s: %v", path, err)
		}
	}
}

func pdfReviewFixture(t *testing.T) (*diffreview.Review, string, string) {
	t.Helper()
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old", "paper.tex")
	newPath := filepath.Join(dir, "new", "paper.tex")
	if err := os.MkdirAll(filepath.Dir(oldPath), 0o755); err != nil {
		t.Fatalf("mkdir old: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		t.Fatalf("mkdir new: %v", err)
	}
	if err := os.WriteFile(oldPath, []byte("\\bye\n"), 0o600); err != nil {
		t.Fatalf("write old: %v", err)
	}
	if err := os.WriteFile(newPath, []byte("\\bye\n"), 0o600); err != nil {
		t.Fatalf("write new: %v", err)
	}
	return &diffreview.Review{
		Old: diffreview.Endpoint{Kind: diffreview.WorkingFile, Path: oldPath},
		New: diffreview.Endpoint{Kind: diffreview.WorkingFile, Path: newPath, Editable: true},
	}, oldPath, newPath
}

func writeLmkfStatus(t *testing.T, texPath string) string {
	t.Helper()
	abs, err := filepath.Abs(texPath)
	if err != nil {
		t.Fatalf("abs tex path: %v", err)
	}
	statusDir := "/tmp/lmkf-status"
	if err := os.MkdirAll(statusDir, 0o755); err != nil {
		t.Fatalf("mkdir lmkf status: %v", err)
	}
	statusFile := filepath.Join(statusDir, filepath.Base(filepath.Dir(abs)))
	logPath := strings.TrimSuffix(abs, filepath.Ext(abs)) + ".log"
	if err := os.WriteFile(statusFile, []byte(logPath), 0o600); err != nil {
		t.Fatalf("write lmkf status: %v", err)
	}
	return statusFile
}
