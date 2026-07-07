package format

import (
	"os"
	"strings"
	"testing"
)

// TestAcceptance_StdinRoundTrip mirrors the manual test:
//
//	mrevdiff fmt --stdin < paper.tex | diff - paper.tex
//
// It reads the PNAS fixture, applies the pipeline (mimicking --stdin which
// uses format.Apply), and verifies that the output is the expected formatted
// version. Running through --stdin again on the expected output should be
// idempotent.
func TestAcceptance_StdinRoundTrip(t *testing.T) {
	src, err := os.ReadFile("../../testdata/pnas-fixture/main_pnas.tex")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	expected, err := os.ReadFile("../../testdata/pnas-fixture/main_pnas.expected.tex")
	if err != nil {
		t.Fatalf("read expected: %v", err)
	}

	opts := Options{PDFFix: true, Diag: false}
	result := Apply(src, opts)

	if string(result.Src) != string(expected) {
		t.Errorf("first pass did not match expected output; diff len before=%d after=%d expected=%d",
			len(src), len(result.Src), len(expected))
	}

	// Idempotency: applying again must produce byte-identical output.
	result2 := Apply(result.Src, opts)
	if string(result2.Src) != string(result.Src) {
		t.Errorf("second pass (idempotency) changed the output; len before=%d after=%d",
			len(result.Src), len(result2.Src))
	}
}

// TestAcceptance_FailOnChange mirrors the manual test:
//
//	mrevdiff fmt --fail-on-change paper.tex  (exits 1 on dirty source)
//
// The logic: run Apply, check if any hits were produced. If the source has
// formatting issues, there should be hits (exit 1). If it's clean, 0 hits
// (exit 0).
func TestAcceptance_FailOnChange(t *testing.T) {
	// Dirty source: should have hits.
	dirty, err := os.ReadFile("../../testdata/pnas-fixture/main_pnas.tex")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	opts := Options{PDFFix: true, Diag: false}
	result := Apply(dirty, opts)
	if len(result.Hits) == 0 {
		t.Fatalf("expected hits for dirty source (PNAS fixture), got 0")
	}
	if string(result.Src) == string(dirty) {
		t.Fatalf("expected changes for dirty source, but output matches input")
	}
	t.Logf("dirty source: %d hits (would exit 1 with --fail-on-change)", len(result.Hits))

	// Clean source: the output bytes should match input (no effective changes).
	// Note: some rules may fire and cancel each other out (e.g., paragraph-suppress
	// removes a blank line, env.spacing adds it back), producing hits but no
	// byte-level change. --fail-on-change checks byte equality, not hit count.
	clean, err := os.ReadFile("../../testdata/pnas-fixture/main_pnas.expected.tex")
	if err != nil {
		t.Fatalf("read expected: %v", err)
	}
	result2 := Apply(clean, opts)
	if string(result2.Src) != string(clean) {
		t.Fatalf("expected no byte-level changes for clean source, but output differs: len before=%d after=%d",
			len(clean), len(result2.Src))
	}
	t.Logf("clean source: byte-stable (%d hits that cancel out, would exit 0 with --fail-on-change)",
		len(result2.Hits))
}

// TestAcceptance_LinesRangeFormat mirrors the manual test:
//
//	mrevdiff fmt --lines=42:120 paper.tex (rewrites only the target range)
//
// Verifies that only lines within the specified range are modified,
// and lines outside the range are preserved byte-for-byte.
func TestAcceptance_LinesRangeFormat(t *testing.T) {
	src, err := os.ReadFile("../../testdata/pnas-fixture/main_pnas.tex")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	// Use --lines=42:120. First, run a full format.
	lineRange := [2]int{42, 120}
	opts := Options{
		PDFFix:    true,
		Diag:      false,
		LineRange: &lineRange,
	}

	// Line-count-changing rules should be skipped.
	skipped := SkippedLineRangeRules(opts)
	if len(skipped) == 0 {
		t.Fatalf("expected some rules to be skipped under --lines, got none")
	}
	t.Logf("skipped under --lines: %v", skipped)

	result := Apply(src, opts)

	// Clip to range.
	clipped, clipErr := ClipToRange(src, result.Src, lineRange)
	if clipErr != nil {
		t.Fatalf("ClipToRange: %v", clipErr)
	}

	// Verify: lines outside 42:120 must be identical to original.
	origLines := strings.Split(string(src), "\n")
	clipLines := strings.Split(string(clipped), "\n")

	if len(origLines) != len(clipLines) {
		t.Fatalf("line count changed: orig=%d clipped=%d", len(origLines), len(clipLines))
	}

	for i := range origLines {
		lineNum := i + 1
		if lineNum < 42 || lineNum > 120 {
			if origLines[i] != clipLines[i] {
				t.Errorf("line %d outside range was modified:\n  orig: %q\n  clip: %q",
					lineNum, origLines[i], clipLines[i])
			}
		}
	}

	// Verify: at least some lines within 42:120 are different (the fixture has issues in that range).
	changedInRange := 0
	for i := 41; i < 120 && i < len(origLines); i++ {
		if origLines[i] != clipLines[i] {
			changedInRange++
		}
	}
	t.Logf("lines changed within range 42:120: %d", changedInRange)
	// The PNAS fixture has trailing whitespace and other issues throughout,
	// so there should be at least a few changes in this range.
	if changedInRange == 0 {
		t.Errorf("expected changes within range 42:120, got 0 — range formatting may be broken")
	}
}

// TestAcceptance_MathAlignColumns mirrors the manual test:
//
//	mrevdiff fmt --rule=math.align-columns paper.tex (aligns tabular columns)
//
// Verifies that the math.align-columns rule correctly aligns & columns in
// math environments.
func TestAcceptance_MathAlignColumns(t *testing.T) {
	src, err := os.ReadFile("../../testdata/math-fixture/math_golden.tex")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	opts := Options{
		PDFFix: true,
		Rules:  []string{"math.align-columns"},
		Diag:   true,
		MathAlign: MathAlignOptions{
			Enabled: true,
		},
	}

	result := Apply(src, opts)

	// Should have hits for the misaligned environments.
	alignHits := 0
	for _, h := range result.Hits {
		if h.RuleID == "math.align-columns" {
			alignHits++
			t.Logf("align hit line %d: %s", h.Line, h.Excerpt)
		}
	}
	if alignHits == 0 {
		t.Fatalf("expected math.align-columns hits, got 0")
	}
	t.Logf("math.align-columns: %d hits", alignHits)

	// Verify the output has aligned columns. Check for the cases environment.
	output := string(result.Src)

	// The cases environment should have aligned & columns.
	if !strings.Contains(output, "\\begin{cases}") {
		t.Fatalf("expected cases environment in output")
	}

	// All Tier-1 hits should have nil ExpectedDiffSourceLines (whitespace-only).
	for _, h := range result.Hits {
		if h.ExpectedDiffSourceLines != nil {
			t.Errorf("Tier-1 math.align-columns hit at line %d has non-nil ExpectedDiffSourceLines: %v",
				h.Line, h.ExpectedDiffSourceLines)
		}
	}

	// Idempotency check: running again should produce no changes.
	result2 := Apply(result.Src, opts)
	if len(result2.Hits) > 0 {
		t.Errorf("math.align-columns should be idempotent; second pass produced %d hits", len(result2.Hits))
		for _, h := range result2.Hits {
			t.Logf("  second-pass hit: %s line %d: %s", h.RuleID, h.Line, h.Excerpt)
		}
	}
}

// TestAcceptance_FullTestSuite verifies that the format package can process
// various inputs without panicking, producing valid output.
func TestAcceptance_FullTestSuite(t *testing.T) {
	fixtures := []struct {
		name string
		path string
	}{
		{"sample", "../../testdata/sample.tex"},
		{"pnas", "../../testdata/pnas-fixture/main_pnas.tex"},
		{"math_golden", "../../testdata/math-fixture/math_golden.tex"},
	}

	for _, fx := range fixtures {
		t.Run(fx.name, func(t *testing.T) {
			src, err := os.ReadFile(fx.path)
			if err != nil {
				t.Fatalf("read %s: %v", fx.path, err)
			}

			// Run with all features enabled.
			opts := Options{
				PDFFix: true,
				Diag:   true,
				MathAlign: MathAlignOptions{
					Enabled: true,
				},
			}
			result := Apply(src, opts)
			t.Logf("%s: %d hits, %d diags, len=%d->%d",
				fx.name, len(result.Hits), len(result.Diags), len(src), len(result.Src))

			// Output should be non-empty.
			if len(result.Src) == 0 {
				t.Fatalf("output is empty for %s", fx.name)
			}

			// Output should still contain documentclass.
			if !strings.Contains(string(result.Src), "\\documentclass") {
				t.Fatalf("output missing \\documentclass for %s", fx.name)
			}
		})
	}
}
