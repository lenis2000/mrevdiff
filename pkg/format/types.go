// Package format implements mrevdiff's LaTeX source normalizer ("mrevdiff fmt").
// It applies a pipeline of rules to the source, optionally verifying that the
// rendered PDF is preserved, and writes the result back.
package format

import "github.com/lenis2000/mrevdiff/pkg/parser"

// Tier classifies a formatting rule by its impact on the rendered PDF.
type Tier int

const (
	// Safe rules produce byte-identical PDFs (e.g. trailing whitespace removal).
	Safe Tier = iota
	// PDFFix rules intentionally change the PDF to fix known author bugs
	// (e.g. spurious paragraph breaks around display math). Off by default.
	PDFFix
	// DiagOnly rules emit diagnostics only; they never rewrite the source.
	DiagOnly
)

// String returns a short label for a Tier.
func (t Tier) String() string {
	switch t {
	case Safe:
		return "safe"
	case PDFFix:
		return "pdf-fix"
	case DiagOnly:
		return "diag"
	}
	return "unknown"
}

// Rule is a single formatting/diagnostic rule registered in the pipeline.
type Rule struct {
	ID    string
	Tier  Tier
	Doc   string
	Apply func(*Ctx) Result
}

// Ctx is the input context for a rule's Apply function. Rules read Src and
// the precomputed indices; they return a possibly-rewritten Src in Result.
type Ctx struct {
	Src       []byte
	Tokens    []parser.Token
	Doc       *parser.Document // nil for early Tier-1 passes that run before Parse
	Protected []parser.ProtectedSpan
	Lines     []int  // line-start byte offsets (from parser.LineOffsets)
	Skip      []bool // 1-indexed; Skip[L]==true means line L is silenced by % mrevdiff-fmt: skip/off/on

	// Carried over from Options so reindex() can rebuild the protected-span
	// list with the same extras after each rewrite.
	verbatimEnvs []string

	// Indent controls space.indent.
	Indent IndentOptions
	// Wrap controls space.wrap.
	Wrap WrapOptions
	// Tilde controls prose.tilde-refs.
	Tilde TildeOptions
	// MathAlign controls math.align-columns.
	MathAlign MathAlignOptions
	// MathWrap controls math.wrap-at-break-op.
	MathWrap MathWrapOptions
}

// LineSkipped reports whether the 1-based source line is silenced by a
// `% mrevdiff-fmt: skip / off / on` directive. Out-of-range line numbers are
// treated as not-skipped (defensive).
func (c *Ctx) LineSkipped(line int) bool {
	if line <= 0 || line >= len(c.Skip) {
		return false
	}
	return c.Skip[line]
}

// RangeSkipped reports whether the byte range [start, end) overlaps any
// line that the skip mask silences. Convenient for rules that work in byte
// space rather than per-line.
func (c *Ctx) RangeSkipped(start, end int) bool {
	if len(c.Skip) <= 1 || end <= start {
		return false
	}
	first := lineAt(c.Lines, start)
	last := lineAt(c.Lines, end-1)
	for L := first; L <= last; L++ {
		if c.LineSkipped(L) {
			return true
		}
	}
	return false
}

// Result is the output of a rule's Apply function.
type Result struct {
	Src   []byte // possibly rewritten source
	Hits  []Hit  // per-rewrite metadata (Tier 1/2); verifier whitelist input
	Diags []Diag // Tier-3 only; ignored for Safe/PDFFix
}

// Hit records a single rewrite site for the verifier.
type Hit struct {
	RuleID                  string
	Line                    int   // 1-based source line of the rewrite, in the BEFORE source
	ExpectedDiffSourceLines []int // source lines whose PDF rendering legitimately changes; nil for Tier-1
	Excerpt                 string
}

// Diag records a diagnostic (Tier-3 only) — no source rewrite, just a message.
type Diag struct {
	RuleID  string
	Line    int
	Message string
}

// ParanoidResult holds the outcome of a pixel-level diff-pdf verification.
type ParanoidResult struct {
	OK          bool
	Message     string
	DiffPDFPath string // path to the diff PDF (empty when OK or unavailable)
}
