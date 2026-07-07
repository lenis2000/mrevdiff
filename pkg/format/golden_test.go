//go:build pdfverify

package format

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const pnasFixtureDir = "../../testdata/pnas-fixture"

// TestGoldenPNAS runs the format pipeline on the frozen PNAS fixture and
// validates output byte-for-byte against the expected file. When LaTeX
// build tools are available, it also runs the text-layer verifier and
// the paranoid (diff-pdf) verifier.
func TestGoldenPNAS(t *testing.T) {
	inputPath := filepath.Join(pnasFixtureDir, "main_pnas.tex")
	expectedPath := filepath.Join(pnasFixtureDir, "main_pnas.expected.tex")
	expectedTxtPath := filepath.Join(pnasFixtureDir, "main_pnas.expected.txt")

	// Read the frozen input.
	inputSrc, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatalf("read fixture input: %v", err)
	}

	// Read the frozen expected output.
	expectedSrc, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("read fixture expected: %v", err)
	}

	// Run the pipeline with Tier-1 + Tier-2 (same options as gen_expected.go).
	result := Apply(inputSrc, Options{
		PDFFix: true,
		Diag:   false,
	})

	// Byte-for-byte comparison of source output.
	if !bytes.Equal(result.Src, expectedSrc) {
		// Find the first differing line for a useful error message.
		gotLines := strings.Split(string(result.Src), "\n")
		wantLines := strings.Split(string(expectedSrc), "\n")
		for i := 0; i < len(gotLines) || i < len(wantLines); i++ {
			var gl, wl string
			if i < len(gotLines) {
				gl = gotLines[i]
			}
			if i < len(wantLines) {
				wl = wantLines[i]
			}
			if gl != wl {
				t.Errorf("source mismatch at line %d:\n  got:  %q\n  want: %q", i+1, truncLine(gl, 120), truncLine(wl, 120))
				if i > 3 {
					t.Fatalf("(stopping after first diff; total got %d lines, want %d lines)",
						len(gotLines), len(wantLines))
				}
			}
		}
		t.Fatalf("source mismatch: got %d bytes, want %d bytes", len(result.Src), len(expectedSrc))
	}

	t.Logf("source comparison OK: %d bytes, %d hits", len(result.Src), len(result.Hits))

	// Verify that we got a reasonable number of hits.
	if len(result.Hits) == 0 {
		t.Error("expected non-zero hits from the pipeline")
	}

	// Phase 2: Verifier round-trip (requires latexmk, pdftotext, pdfinfo).
	if !toolsAvailable("latexmk", "pdftotext", "pdfinfo") {
		t.Log("skipping verifier round-trip: latexmk/pdftotext/pdfinfo not available")
		return
	}

	tree, err := DiscoverTree(inputPath)
	if err != nil {
		t.Fatalf("discover tree: %v", err)
	}

	vr, err := Verify(*tree, inputSrc, result.Src, result.Hits)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	defer func() {
		// Clean up the tempdir after the test.
		if td := LastTempDir(); td != "" {
			os.RemoveAll(td)
		}
	}()

	if !vr.OK {
		t.Errorf("verifier reported unexpected diffs:")
		var buf bytes.Buffer
		FormatDiffs(&buf, vr.Unexpected)
		t.Log(buf.String())
	}
	for _, w := range vr.Warnings {
		t.Logf("verifier warning: %s", w)
	}
	t.Log("text-layer verification OK")

	// Phase 3: Compare pdftotext output against frozen expected.txt if it exists.
	if _, statErr := os.Stat(expectedTxtPath); statErr == nil {
		expectedTxt, readErr := os.ReadFile(expectedTxtPath)
		if readErr != nil {
			t.Fatalf("read expected txt: %v", readErr)
		}

		// Extract pdftotext from the after PDF.
		afterText, ptErr := runPdftotext(vr.AfterPDF)
		if ptErr != nil {
			t.Fatalf("pdftotext after: %v", ptErr)
		}
		afterNorm := normalizeTextLines(afterText)
		expectedNorm := normalizeTextLines(expectedTxt)

		if afterNorm != expectedNorm {
			afterLines := strings.Split(afterNorm, "\n")
			expectedLines := strings.Split(expectedNorm, "\n")
			for i := 0; i < len(afterLines) || i < len(expectedLines); i++ {
				var al, el string
				if i < len(afterLines) {
					al = afterLines[i]
				}
				if i < len(expectedLines) {
					el = expectedLines[i]
				}
				if al != el {
					t.Errorf("pdftotext mismatch at line %d:\n  got:  %q\n  want: %q", i+1, truncLine(al, 120), truncLine(el, 120))
					break
				}
			}
			t.Fatal("pdftotext output does not match frozen expected.txt")
		}
		t.Log("pdftotext comparison OK")
	} else {
		t.Log("skipping pdftotext comparison: main_pnas.expected.txt not found (run 'make pnas-fixture' to generate)")
	}

	// Phase 4: Paranoid (diff-pdf) verification.
	if !ParanoidAvailable {
		t.Log("skipping paranoid verification: pdfverify tag present but diff-pdf check deferred")
		return
	}
	if !toolsAvailable("diff-pdf") {
		t.Log("skipping paranoid verification: diff-pdf not available")
		return
	}

	pr, err := VerifyParanoid(vr.BeforePDF, vr.AfterPDF)
	if err != nil {
		t.Fatalf("paranoid verify: %v", err)
	}
	if !pr.OK {
		t.Errorf("paranoid verification failed: %s (diff PDF: %s)", pr.Message, pr.DiffPDFPath)
	} else {
		t.Logf("paranoid verification OK: %s", pr.Message)
	}
}

// TestGoldenPNAS_SourceOnly runs the source-level golden comparison without
// needing any external tools. This always runs when the pdfverify tag is set.
func TestGoldenPNAS_SourceOnly(t *testing.T) {
	inputPath := filepath.Join(pnasFixtureDir, "main_pnas.tex")
	expectedPath := filepath.Join(pnasFixtureDir, "main_pnas.expected.tex")

	inputSrc, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatalf("read fixture input: %v", err)
	}
	expectedSrc, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("read fixture expected: %v", err)
	}

	result := Apply(inputSrc, Options{PDFFix: true, Diag: false})

	if !bytes.Equal(result.Src, expectedSrc) {
		t.Fatalf("source mismatch: got %d bytes, want %d bytes (re-run gen_expected.go to update fixtures)",
			len(result.Src), len(expectedSrc))
	}

	// Verify pipeline is idempotent.
	result2 := Apply(result.Src, Options{PDFFix: true, Diag: false})
	if !bytes.Equal(result2.Src, result.Src) {
		t.Error("pipeline is not idempotent: second pass changed the output")
	}
}

func toolsAvailable(names ...string) bool {
	for _, name := range names {
		if _, err := exec.LookPath(name); err != nil {
			return false
		}
	}
	return true
}

func truncLine(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
