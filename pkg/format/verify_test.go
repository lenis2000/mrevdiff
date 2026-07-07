package format

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// normalizeTextLines
// ---------------------------------------------------------------------------

func TestNormalizeTextLines(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "strip trailing spaces",
			input: "hello   \nworld  \n",
			want:  "hello\nworld",
		},
		{
			name:  "collapse internal whitespace",
			input: "hello    world\nfoo   bar\n",
			want:  "hello world\nfoo bar",
		},
		{
			name:  "tabs become single space",
			input: "hello\tworld\n",
			want:  "hello world",
		},
		{
			name:  "empty lines preserved",
			input: "a\n\nb\n",
			want:  "a\n\nb",
		},
		{
			name:  "single line",
			input: "hello",
			want:  "hello",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeTextLines([]byte(tt.input))
			assert.Equal(t, tt.want, got)
		})
	}
}

// ---------------------------------------------------------------------------
// splitPages
// ---------------------------------------------------------------------------

func TestSplitPages(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  [][]string
	}{
		{
			name:  "single page",
			input: "line1\nline2",
			want:  [][]string{{"line1", "line2"}},
		},
		{
			name:  "two pages",
			input: "page1-l1\npage1-l2\fpage2-l1\npage2-l2",
			want:  [][]string{{"page1-l1", "page1-l2"}, {"page2-l1", "page2-l2"}},
		},
		{
			name:  "empty page",
			input: "page1\f\fpage3",
			want:  [][]string{{"page1"}, nil, {"page3"}},
		},
		{
			name:  "trailing form feed",
			input: "page1\f",
			want:  [][]string{{"page1"}, nil},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitPages(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ---------------------------------------------------------------------------
// collapseSpaces
// ---------------------------------------------------------------------------

func TestCollapseSpaces(t *testing.T) {
	assert.Equal(t, "a b c", collapseSpaces("a   b  c"))
	assert.Equal(t, "hello world", collapseSpaces("hello\t\tworld"))
	assert.Equal(t, " x ", collapseSpaces("  x  "))
	assert.Equal(t, "no change", collapseSpaces("no change"))
}

// ---------------------------------------------------------------------------
// isWhitelisted
// ---------------------------------------------------------------------------

func TestIsWhitelisted(t *testing.T) {
	entries := []whitelistEntry{
		{Page: 1, LineInPage: 10, SourceLine: 42, RuleID: "test"},
		{Page: 2, LineInPage: 5, SourceLine: 100, RuleID: "test2"},
	}

	// Within tolerance of entry 1.
	assert.True(t, isWhitelisted(entries, 1, 8))
	assert.True(t, isWhitelisted(entries, 1, 10))
	assert.True(t, isWhitelisted(entries, 1, 12))

	// Outside tolerance of entry 1.
	assert.False(t, isWhitelisted(entries, 1, 7))
	assert.False(t, isWhitelisted(entries, 1, 13))

	// Wrong page.
	assert.False(t, isWhitelisted(entries, 3, 10))

	// Within tolerance of entry 2.
	assert.True(t, isWhitelisted(entries, 2, 3))
	assert.True(t, isWhitelisted(entries, 2, 7))

	// Empty whitelist.
	assert.False(t, isWhitelisted(nil, 1, 10))
}

// ---------------------------------------------------------------------------
// estimatePdftextLine
// ---------------------------------------------------------------------------

func TestEstimatePdftextLine(t *testing.T) {
	// Y at top margin should give line 0.
	assert.Equal(t, 0, estimatePdftextLine(72.0, 14.0))
	// Y below top margin.
	assert.Equal(t, 1, estimatePdftextLine(86.0, 14.0))
	// Y above top margin (should clamp to 0).
	assert.Equal(t, 0, estimatePdftextLine(50.0, 14.0))
}

// ---------------------------------------------------------------------------
// detectNoOps
// ---------------------------------------------------------------------------

func TestDetectNoOps(t *testing.T) {
	entries := []whitelistEntry{
		{Page: 1, LineInPage: 5, SourceLine: 42, RuleID: "math.paragraph-suppress"},
	}
	hits := []Hit{
		{RuleID: "math.paragraph-suppress", Line: 42, ExpectedDiffSourceLines: []int{42}},
	}

	// Case 1: no actual diff in the whitelist region → should warn.
	before := [][]string{{"a", "b", "c", "d", "e", "f", "g", "h"}}
	after := [][]string{{"a", "b", "c", "d", "e", "f", "g", "h"}}
	warnings := detectNoOps(entries, before, after, hits)
	assert.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "math.paragraph-suppress")
	assert.Contains(t, warnings[0], "L42")

	// Case 2: actual diff at the expected position → no warning.
	after2 := [][]string{{"a", "b", "c", "d", "e", "CHANGED", "g", "h"}}
	warnings2 := detectNoOps(entries, before, after2, hits)
	assert.Empty(t, warnings2)
}

// ---------------------------------------------------------------------------
// DiscoverTree
// ---------------------------------------------------------------------------

func TestDiscoverTree(t *testing.T) {
	// Create a temp directory with some files.
	dir := t.TempDir()
	writeFile := func(name, content string) {
		path := filepath.Join(dir, name)
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.WriteFile(path, []byte(content), 0o644)
	}

	writeFile("paper.tex", `\documentclass{article}\begin{document}Hello\end{document}`)
	writeFile("latexmkrc", `$pdf_mode = 1;`)
	writeFile("custom.cls", `\ProvidesClass{custom}`)
	writeFile("refs.bib", `@article{test, title={Test}}`)
	writeFile("fig.pdf", "fake pdf")
	writeFile("fig.png", "fake png")
	writeFile("notes.txt", "not included")
	writeFile(".hidden/secret.tex", "hidden")

	tree, err := DiscoverTree(filepath.Join(dir, "paper.tex"))
	require.NoError(t, err)

	assert.Equal(t, dir, tree.Root)
	assert.Equal(t, "paper.tex", tree.Paper)

	// Check that expected files are included.
	fileSet := map[string]bool{}
	for _, f := range tree.Files {
		fileSet[f] = true
	}
	assert.True(t, fileSet["paper.tex"], "paper.tex should be included")
	assert.True(t, fileSet["latexmkrc"], "latexmkrc should be included")
	assert.True(t, fileSet["custom.cls"], "custom.cls should be included")
	assert.True(t, fileSet["refs.bib"], "refs.bib should be included")
	assert.True(t, fileSet["fig.pdf"], "fig.pdf should be included")
	assert.True(t, fileSet["fig.png"], "fig.png should be included")

	// Check that excluded files are not included.
	assert.False(t, fileSet["notes.txt"], "notes.txt should not be included")
	assert.False(t, fileSet[".hidden/secret.tex"], "hidden dir should be skipped")
}

// ---------------------------------------------------------------------------
// CleanTempDirs
// ---------------------------------------------------------------------------

func TestCleanTempDirs(t *testing.T) {
	// Create a temp dir matching the pattern.
	dir, err := os.MkdirTemp("", "mr-fmt-")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(dir) }() // safety

	// Verify it exists.
	_, err = os.Stat(dir)
	require.NoError(t, err)

	// Clean should remove it.
	require.NoError(t, CleanTempDirs())

	_, err = os.Stat(dir)
	assert.True(t, os.IsNotExist(err), "dir should be removed")
}

// ---------------------------------------------------------------------------
// FormatDiffs
// ---------------------------------------------------------------------------

func TestFormatDiffs(t *testing.T) {
	var buf []byte
	w := &bytesWriter{buf: &buf}

	diffs := []Diff{
		{Page: 0, LineInPage: 0, Before: "page count: 2", After: "page count: 3"},
		{Page: 1, LineInPage: 5, Before: "hello world", After: "hello changed"},
	}
	FormatDiffs(w, diffs)

	out := string(*w.buf)
	assert.Contains(t, out, "page count: 2")
	assert.Contains(t, out, "page 1, line 5")
	assert.Contains(t, out, "hello world")
	assert.Contains(t, out, "hello changed")
}

// bytesWriter is a simple io.Writer that appends to a byte slice.
type bytesWriter struct {
	buf *[]byte
}

func (w *bytesWriter) Write(p []byte) (int, error) {
	*w.buf = append(*w.buf, p...)
	return len(p), nil
}

// ---------------------------------------------------------------------------
// Integration: Verify with Tier-1 rules on sample.tex
// ---------------------------------------------------------------------------

func TestVerifyIntegration_Tier1(t *testing.T) {
	// Skip if required tools are not available.
	for _, tool := range []string{"latexmk", "pdftotext", "pdfinfo"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("skipping: %s not available", tool)
		}
	}

	// Use testdata/sample.tex.
	samplePath := filepath.Join("..", "..", "testdata", "sample.tex")
	if _, err := os.Stat(samplePath); err != nil {
		t.Skipf("skipping: sample.tex not found at %s", samplePath)
	}

	src, err := os.ReadFile(samplePath)
	require.NoError(t, err)

	// Apply Tier-1 rules.
	result := Apply(src, Options{})
	if string(result.Src) == string(src) {
		t.Log("no changes from Tier-1 rules — verification trivially passes")
		return
	}

	// Discover tree and verify.
	tree, err := DiscoverTree(samplePath)
	require.NoError(t, err)

	vr, err := Verify(context.Background(), *tree, src, result.Src, result.Hits)
	require.NoError(t, err)

	// Tier-1 rules should produce identical PDF text.
	assert.True(t, vr.OK, "Tier-1 rewrites should not change PDF text; unexpected diffs: %v", vr.Unexpected)
	assert.Empty(t, vr.Unexpected)

	// Verify that PDF paths are populated.
	assert.NotEmpty(t, vr.BeforePDF, "BeforePDF should be set")
	assert.NotEmpty(t, vr.AfterPDF, "AfterPDF should be set")
}

// ---------------------------------------------------------------------------
// ParanoidAvailable (stub check)
// ---------------------------------------------------------------------------

func TestParanoidAvailable(t *testing.T) {
	// In a normal (non-pdfverify) build, ParanoidAvailable should be false.
	// In a pdfverify build, it should be true.
	// We just verify the constant is accessible and consistent with VerifyParanoid.
	if ParanoidAvailable {
		t.Log("pdfverify build tag is active — paranoid verifier available")
	} else {
		t.Log("pdfverify build tag not active — paranoid verifier is a stub")
		// Calling the stub should return an error.
		_, err := VerifyParanoid(context.Background(), "/nonexistent/before.pdf", "/nonexistent/after.pdf")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not available")
	}
}

// ---------------------------------------------------------------------------
// VerifyParanoid integration
// ---------------------------------------------------------------------------

func TestVerifyParanoidIntegration(t *testing.T) {
	if !ParanoidAvailable {
		t.Skip("skipping: pdfverify build tag not set")
	}

	// Check diff-pdf is available.
	if _, err := exec.LookPath("diff-pdf"); err != nil {
		t.Skip("skipping: diff-pdf not available")
	}

	// Check pdfinfo is available.
	if _, err := exec.LookPath("pdfinfo"); err != nil {
		t.Skip("skipping: pdfinfo not available")
	}

	// Also need latexmk and pdftotext for the full pipeline.
	for _, tool := range []string{"latexmk", "pdftotext"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("skipping: %s not available", tool)
		}
	}

	// Use testdata/sample.tex.
	samplePath := filepath.Join("..", "..", "testdata", "sample.tex")
	if _, err := os.Stat(samplePath); err != nil {
		t.Skipf("skipping: sample.tex not found at %s", samplePath)
	}

	src, err := os.ReadFile(samplePath)
	require.NoError(t, err)

	// Apply Tier-1 rules.
	result := Apply(src, Options{})

	// Discover tree and verify with text layer first.
	tree, err := DiscoverTree(samplePath)
	require.NoError(t, err)

	vr, err := Verify(context.Background(), *tree, src, result.Src, result.Hits)
	require.NoError(t, err)
	require.True(t, vr.OK, "text-layer verification should pass")

	// Now run paranoid verification on the same PDFs.
	pr, err := VerifyParanoid(context.Background(), vr.BeforePDF, vr.AfterPDF)
	require.NoError(t, err)
	assert.True(t, pr.OK, "paranoid verification should pass for Tier-1 rewrites: %s", pr.Message)
}

// ---------------------------------------------------------------------------
// VerifyResult.BeforePDF / AfterPDF populated by Verify
// ---------------------------------------------------------------------------

func TestVerifyResult_PDFPaths(t *testing.T) {
	// Just verify the struct fields exist and are typed correctly.
	vr := &VerifyResult{
		OK:        true,
		BeforePDF: "/tmp/test/before/paper.pdf",
		AfterPDF:  "/tmp/test/after/paper.pdf",
	}
	assert.Equal(t, "/tmp/test/before/paper.pdf", vr.BeforePDF)
	assert.Equal(t, "/tmp/test/after/paper.pdf", vr.AfterPDF)
}

// ---------------------------------------------------------------------------
// ParanoidResult type
// ---------------------------------------------------------------------------

func TestParanoidResult(t *testing.T) {
	// Test the type is usable.
	pr := &ParanoidResult{
		OK:          false,
		Message:     "pixel diff detected",
		DiffPDFPath: "/tmp/mr-fmt-xxx/diff.pdf",
	}
	assert.False(t, pr.OK)
	assert.Equal(t, "pixel diff detected", pr.Message)
	assert.Equal(t, "/tmp/mr-fmt-xxx/diff.pdf", pr.DiffPDFPath)

	pr2 := &ParanoidResult{OK: true, Message: "pixel-identical"}
	assert.True(t, pr2.OK)
	assert.Empty(t, pr2.DiffPDFPath)
}
