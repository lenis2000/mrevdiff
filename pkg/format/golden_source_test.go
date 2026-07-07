package format

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGoldenPNASSource runs the source-level golden comparison without any
// build tag requirement. This validates that the format pipeline produces
// the expected output from the frozen PNAS fixture, without needing LaTeX
// build tools or diff-pdf.
func TestGoldenPNASSource(t *testing.T) {
	fixtureDir := filepath.Join("..", "..", "testdata", "pnas-fixture")

	inputPath := filepath.Join(fixtureDir, "main_pnas.tex")
	expectedPath := filepath.Join(fixtureDir, "main_pnas.expected.tex")

	inputSrc, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatalf("read fixture input: %v", err)
	}
	expectedSrc, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("read fixture expected: %v", err)
	}

	// Run the pipeline with Tier-1 + Tier-2 (same options as gen_expected.go).
	result := Apply(inputSrc, Options{PDFFix: true, Diag: false})

	// Byte-for-byte comparison.
	if !bytes.Equal(result.Src, expectedSrc) {
		gotLines := strings.Split(string(result.Src), "\n")
		wantLines := strings.Split(string(expectedSrc), "\n")
		diffs := 0
		for i := 0; i < len(gotLines) || i < len(wantLines); i++ {
			var gl, wl string
			if i < len(gotLines) {
				gl = gotLines[i]
			}
			if i < len(wantLines) {
				wl = wantLines[i]
			}
			if gl != wl {
				t.Errorf("line %d differs:\n  got:  %q\n  want: %q", i+1, gl, wl)
				diffs++
				if diffs >= 5 {
					break
				}
			}
		}
		t.Fatalf("source mismatch: got %d bytes (%d lines), want %d bytes (%d lines)",
			len(result.Src), len(gotLines), len(expectedSrc), len(wantLines))
	}

	// Verify pipeline is idempotent: second pass should be a no-op.
	result2 := Apply(result.Src, Options{PDFFix: true, Diag: false})
	if !bytes.Equal(result2.Src, result.Src) {
		t.Error("pipeline is not idempotent: second pass changed the output")
	}

	t.Logf("golden source comparison OK: %d bytes, %d hits, idempotent", len(result.Src), len(result.Hits))
}
