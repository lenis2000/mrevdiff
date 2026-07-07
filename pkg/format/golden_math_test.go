package format

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const mathFixtureDir = "../../testdata/math-fixture"

// ---------------------------------------------------------------------------
// PNAS golden round-trip with Phase 2-4 rules enabled
// ---------------------------------------------------------------------------

// TestGoldenPNAS_MathRules verifies that the PNAS fixture exercises the
// Phase 2-4 rules when math options are enabled. This complements
// TestGoldenPNASSource (which runs with default options) by enabling
// math.align-columns and math.continuation-indent explicitly.
func TestGoldenPNAS_MathRules(t *testing.T) {
	fixtureDir := filepath.Join("..", "..", "testdata", "pnas-fixture")
	inputPath := filepath.Join(fixtureDir, "main_pnas.tex")

	inputSrc, err := os.ReadFile(inputPath)
	require.NoError(t, err, "read PNAS input")

	// Run with math rules enabled.
	opts := Options{
		PDFFix:    true,
		Diag:      false,
		MathAlign: MathAlignOptions{Enabled: true},
	}
	result := Apply(inputSrc, opts)

	// Collect rule IDs that fired.
	ruleHits := map[string]int{}
	for _, h := range result.Hits {
		ruleHits[h.RuleID]++
	}

	t.Logf("PNAS math-enabled run: %d hits across %d rules", len(result.Hits), len(ruleHits))
	for ruleID, count := range ruleHits {
		t.Logf("  %s: %d", ruleID, count)
	}

	// Phase 2-4 rules must fire at least once each.
	assert.Greater(t, ruleHits["math.continuation-indent"], 0,
		"math.continuation-indent should fire on PNAS fixture")
	assert.Greater(t, ruleHits["math.align-columns"], 0,
		"math.align-columns should fire on PNAS fixture")
	assert.Greater(t, ruleHits["prose.tilde-refs"], 0,
		"prose.tilde-refs should fire on PNAS fixture")

	// Idempotency check: second pass should not change anything.
	result2 := Apply(result.Src, opts)
	if !bytes.Equal(result2.Src, result.Src) {
		t.Error("pipeline with math rules is not idempotent: second pass changed the output")
	}
}

// ---------------------------------------------------------------------------
// Math fixture: hand-crafted misaligned content for columnizer testing
// ---------------------------------------------------------------------------

// TestGoldenMathFixture runs the format pipeline on the math-fixture and
// verifies that align-columns, continuation-indent, and tilde-refs all fire.
func TestGoldenMathFixture(t *testing.T) {
	inputPath := filepath.Join(mathFixtureDir, "math_golden.tex")

	inputSrc, err := os.ReadFile(inputPath)
	require.NoError(t, err, "read math fixture")

	opts := Options{
		PDFFix:    true,
		MathAlign: MathAlignOptions{Enabled: true},
	}
	result := Apply(inputSrc, opts)

	ruleHits := map[string]int{}
	for _, h := range result.Hits {
		ruleHits[h.RuleID]++
	}

	t.Logf("math fixture: %d hits", len(result.Hits))
	for ruleID, count := range ruleHits {
		t.Logf("  %s: %d", ruleID, count)
	}

	// Verify math.align-columns fires on the misaligned environments.
	assert.Greater(t, ruleHits["math.align-columns"], 0,
		"math.align-columns should fire on misaligned cases/pmatrix/tabular/align*")

	// Verify math.continuation-indent fires on the equation with binop continuations.
	assert.Greater(t, ruleHits["math.continuation-indent"], 0,
		"math.continuation-indent should fire on equation* with continuation lines")

	// Verify prose.tilde-refs fires on unprotected space+cite/ref.
	assert.Greater(t, ruleHits["prose.tilde-refs"], 0,
		"prose.tilde-refs should fire on space before \\cite/\\ref")

	// Idempotency.
	result2 := Apply(result.Src, opts)
	if !bytes.Equal(result2.Src, result.Src) {
		t.Error("math fixture pipeline is not idempotent")
	}
}

// TestGoldenMathFixture_AlignDetails checks that math.align-columns
// correctly aligns specific environments in the math fixture.
func TestGoldenMathFixture_AlignDetails(t *testing.T) {
	inputPath := filepath.Join(mathFixtureDir, "math_golden.tex")

	inputSrc, err := os.ReadFile(inputPath)
	require.NoError(t, err)

	opts := Options{
		Rules:     []string{"math.align-columns"},
		MathAlign: MathAlignOptions{Enabled: true},
	}
	result := Apply(inputSrc, opts)

	// Should fire on: cases, pmatrix, tabular, align*
	// The second align* (already aligned) should be a no-op.
	// The skipped align* blocks (unequal cells, comments) are guarded by skip directives.
	alignHits := 0
	for _, h := range result.Hits {
		if h.RuleID == "math.align-columns" {
			alignHits++
			t.Logf("  L%d: %s", h.Line, h.Excerpt)
		}
	}

	assert.Greater(t, alignHits, 0, "expected align-columns hits on the fixture")

	// Check that the output contains properly aligned cases env.
	out := string(result.Src)
	// After alignment, the cases cells should have consistent padding.
	assert.Contains(t, out, "\\begin{cases}")
	assert.Contains(t, out, "\\end{cases}")
}

// TestGoldenMathFixture_ContIndentDetails checks continuation-indent
// on the math fixture.
func TestGoldenMathFixture_ContIndentDetails(t *testing.T) {
	inputPath := filepath.Join(mathFixtureDir, "math_golden.tex")

	inputSrc, err := os.ReadFile(inputPath)
	require.NoError(t, err)

	opts := Options{
		Rules: []string{"math.continuation-indent"},
	}
	result := Apply(inputSrc, opts)

	contHits := 0
	for _, h := range result.Hits {
		if h.RuleID == "math.continuation-indent" {
			contHits++
			t.Logf("  L%d: %s", h.Line, h.Excerpt)
		}
	}

	assert.Greater(t, contHits, 0, "expected continuation-indent hits")

	// Check the output: binop lines should be indented past the = anchor.
	out := string(result.Src)
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Find continuation lines (starting with + or -) inside equation*
		if (strings.HasPrefix(trimmed, "+ ") || strings.HasPrefix(trimmed, "- ")) &&
			i > 0 {
			// Should be indented (not at column 0).
			indent := len(line) - len(strings.TrimLeft(line, " "))
			if indent == 0 {
				t.Errorf("line %d: continuation binop at column 0 (should be indented): %q", i+1, line)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Tier-1 math rules: verify whitespace-only changes (PDF byte-identity)
// ---------------------------------------------------------------------------

// TestTier1MathRules_WhitespaceOnly verifies that Tier-1 math rules
// (math.align-columns, math.continuation-indent) produce only whitespace
// changes. This ensures they maintain the Tier-1 contract of producing
// byte-identical PDFs.
func TestTier1MathRules_WhitespaceOnly(t *testing.T) {
	inputPath := filepath.Join(mathFixtureDir, "math_golden.tex")

	inputSrc, err := os.ReadFile(inputPath)
	require.NoError(t, err)

	tier1MathRules := []string{
		"math.align-columns",
		"math.continuation-indent",
	}

	for _, ruleID := range tier1MathRules {
		t.Run(ruleID, func(t *testing.T) {
			opts := Options{
				Rules:     []string{ruleID},
				MathAlign: MathAlignOptions{Enabled: true},
			}
			result := Apply(inputSrc, opts)

			if bytes.Equal(result.Src, inputSrc) {
				t.Log("no changes made (input already formatted)")
				return
			}

			// Verify all hits have nil ExpectedDiffSourceLines (Tier-1 contract).
			for _, h := range result.Hits {
				assert.Nil(t, h.ExpectedDiffSourceLines,
					"Tier-1 rule %s should have nil ExpectedDiffSourceLines (hit at L%d: %s)",
					h.RuleID, h.Line, h.Excerpt)
			}

			// Verify changes are whitespace-only by comparing non-whitespace content.
			origNoWS := stripWhitespace(inputSrc)
			outNoWS := stripWhitespace(result.Src)
			if origNoWS != outNoWS {
				// Find first difference for diagnostic.
				origLines := strings.Split(string(inputSrc), "\n")
				outLines := strings.Split(string(result.Src), "\n")
				for i := 0; i < len(origLines) || i < len(outLines); i++ {
					var ol, nl string
					if i < len(origLines) {
						ol = origLines[i]
					}
					if i < len(outLines) {
						nl = outLines[i]
					}
					olNoWS := strings.ReplaceAll(strings.ReplaceAll(ol, " ", ""), "\t", "")
					nlNoWS := strings.ReplaceAll(strings.ReplaceAll(nl, " ", ""), "\t", "")
					if olNoWS != nlNoWS {
						t.Errorf("non-whitespace change at line %d:\n  orig: %q\n  new:  %q", i+1, ol, nl)
						break
					}
				}
				t.Fatalf("Tier-1 rule %s made non-whitespace changes", ruleID)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Tier-2 tilde rule: verify expected-diff-only
// ---------------------------------------------------------------------------

// TestTier2TildeRule_ExpectedDiffOnly verifies that the prose.tilde-refs
// rule (Tier-2) populates ExpectedDiffSourceLines for every hit, and that
// the changes are exactly the insertion of ~ before cite/ref commands.
func TestTier2TildeRule_ExpectedDiffOnly(t *testing.T) {
	inputPath := filepath.Join(mathFixtureDir, "math_golden.tex")

	inputSrc, err := os.ReadFile(inputPath)
	require.NoError(t, err)

	opts := Options{
		Rules: []string{"prose.tilde-refs"},
	}
	result := Apply(inputSrc, opts)

	require.Greater(t, len(result.Hits), 0,
		"prose.tilde-refs should fire on the math fixture")

	for _, h := range result.Hits {
		assert.Equal(t, "prose.tilde-refs", h.RuleID)
		// Tier-2 hits must have non-nil ExpectedDiffSourceLines.
		assert.NotNil(t, h.ExpectedDiffSourceLines,
			"Tier-2 rule prose.tilde-refs should have non-nil ExpectedDiffSourceLines (hit at L%d)", h.Line)
		assert.Greater(t, len(h.ExpectedDiffSourceLines), 0,
			"ExpectedDiffSourceLines should not be empty (hit at L%d)", h.Line)
	}

	// The changes should be: space -> ~ before cite/ref commands.
	if !bytes.Equal(result.Src, inputSrc) {
		origLines := strings.Split(string(inputSrc), "\n")
		outLines := strings.Split(string(result.Src), "\n")
		for i := 0; i < len(origLines) && i < len(outLines); i++ {
			if origLines[i] != outLines[i] {
				// The difference should be space replaced with ~.
				t.Logf("line %d changed:", i+1)
				t.Logf("  orig: %s", origLines[i])
				t.Logf("  new:  %s", outLines[i])
				// Verify the change is space -> ~
				assert.Contains(t, outLines[i], "~\\",
					"tilde-refs change should introduce ~\\ before cite/ref at line %d", i+1)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// PNAS fixture: tilde rule expected-diff verification
// ---------------------------------------------------------------------------

// TestGoldenPNAS_TildeExpectedDiff verifies that prose.tilde-refs hits in
// the PNAS fixture have proper ExpectedDiffSourceLines metadata.
func TestGoldenPNAS_TildeExpectedDiff(t *testing.T) {
	fixtureDir := filepath.Join("..", "..", "testdata", "pnas-fixture")
	inputPath := filepath.Join(fixtureDir, "main_pnas.tex")

	inputSrc, err := os.ReadFile(inputPath)
	require.NoError(t, err)

	opts := Options{PDFFix: true, Diag: false}
	result := Apply(inputSrc, opts)

	tildeHits := 0
	for _, h := range result.Hits {
		if h.RuleID == "prose.tilde-refs" {
			tildeHits++
			assert.NotNil(t, h.ExpectedDiffSourceLines,
				"PNAS tilde-refs hit at L%d should have ExpectedDiffSourceLines", h.Line)
			assert.Greater(t, len(h.ExpectedDiffSourceLines), 0,
				"ExpectedDiffSourceLines should not be empty (L%d)", h.Line)
		}
	}

	assert.Greater(t, tildeHits, 0, "prose.tilde-refs should fire on PNAS fixture")
	t.Logf("PNAS tilde-refs: %d hits, all with ExpectedDiffSourceLines", tildeHits)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// stripWhitespace removes all whitespace characters from b and returns
// the result as a string. Used for Tier-1 whitespace-only verification.
func stripWhitespace(b []byte) string {
	var buf bytes.Buffer
	buf.Grow(len(b))
	for _, c := range b {
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			buf.WriteByte(c)
		}
	}
	return buf.String()
}
