package format

import (
	"bytes"
	"fmt"

	"github.com/lenis2000/mrevdiff/pkg/parser"
)

// Options controls which rules the pipeline runs.
type Options struct {
	// PDFFix enables Tier-2 (PDF-fixing) rules in addition to Tier-1.
	PDFFix bool
	// DiagOnly enables Tier-3 diagnostics (no rewrites).
	Diag bool
	// Rules, if non-empty, restricts the run to only these rule IDs.
	Rules []string
	// SkipRules, if non-empty, removes these rule IDs from the run. Applied
	// after Rules-based filtering and the tier-based defaulting, so it works
	// equally with `--rule` whitelists and the default Safe/PDFFix selection.
	SkipRules []string
	// VerbatimEnvs adds caller-supplied environments to the protected-span
	// list (in addition to the built-in verbatim/Verbatim/lstlisting/minted/
	// comment defaults). Useful for user-defined listing wrappers.
	VerbatimEnvs []string
	// Indent controls the space.indent rule.
	Indent IndentOptions
	// Wrap controls the space.wrap rule.
	Wrap WrapOptions
	// Tilde controls the prose.tilde-refs rule.
	Tilde TildeOptions
	// MathAlign controls the math.align-columns rule.
	MathAlign MathAlignOptions
	// MathWrap controls the math.wrap-at-break-op rule.
	MathWrap MathWrapOptions
	// LineRange, when non-nil, restricts formatting to the given 1-based
	// inclusive line range [Start, End]. Line-count-changing rules are
	// force-disabled when set.
	LineRange *[2]int
}

// PipelineResult holds the output of a full Apply run.
type PipelineResult struct {
	Src   []byte // final (possibly rewritten) source
	Hits  []Hit
	Diags []Diag
}

// Apply runs the enabled rules from Registry against src and returns the
// (possibly rewritten) source together with all hits and diagnostics.
//
// The pipeline recomputes token/span/line indices after any rule that changes
// the source bytes (Protected spans and Lines are byte-offset-based, so any
// byte-level change — even one that preserves newline count — invalidates them).
// The Document (ctx.Doc) is parsed once before the first non-Safe rule that
// needs it (Tier-2 and Tier-3 rules reason about envs, labels, and refs).
func Apply(src []byte, opts Options) PipelineResult {
	var allHits []Hit
	var allDiags []Diag

	ctx := newCtxWithOpts(src, opts)

	enabled := enabledRules(opts)
	for _, rule := range enabled {
		// Parse the Document before the first rule that needs it.
		if rule.Tier >= PDFFix && ctx.Doc == nil {
			doc, _ := parser.Parse(ctx.Src)
			ctx.Doc = doc
		}

		result := rule.Apply(ctx)

		allHits = append(allHits, result.Hits...)
		// Drop Tier-3 diagnostics on lines silenced by % mrevdiff-fmt: skip/off/on.
		// Tier-1/2 rules already consult ctx.Skip themselves, so Hits are not
		// filtered here.
		for _, d := range result.Diags {
			if ctx.LineSkipped(d.Line) {
				continue
			}
			allDiags = append(allDiags, d)
		}

		if !bytes.Equal(result.Src, ctx.Src) {
			nlBefore := bytes.Count(ctx.Src, []byte{'\n'})
			nlAfter := bytes.Count(result.Src, []byte{'\n'})
			ctx.Src = result.Src
			// Always reindex: Protected spans and Lines are byte-offset-based,
			// so any source change (even tab→spaces with same newline count)
			// invalidates them.
			reindex(ctx)
			if nlBefore != nlAfter {
				// Invalidate Doc so it gets re-parsed with correct
				// line numbers before the next tier that needs it.
				ctx.Doc = nil
			}
		}
	}

	return PipelineResult{
		Src:   ctx.Src,
		Hits:  allHits,
		Diags: allDiags,
	}
}

// newCtx builds a fresh Ctx from source bytes with default options.
// Retained for tests and callers that don't need to extend the verbatim list.
func newCtx(src []byte) *Ctx {
	return newCtxWithOpts(src, Options{})
}

// newCtxWithOpts builds a fresh Ctx applying the verbatim-env extras and
// indent settings from opts.
func newCtxWithOpts(src []byte, opts Options) *Ctx {
	return &Ctx{
		Src:          src,
		Tokens:       parser.Tokenize(src),
		Protected:    parser.ProtectedSpansExtra(src, opts.VerbatimEnvs),
		Lines:        parser.LineOffsets(src),
		Skip:         BuildSkipMask(src),
		verbatimEnvs: append([]string(nil), opts.VerbatimEnvs...),
		Indent:       opts.Indent,
		Wrap:         opts.Wrap,
		Tilde:        opts.Tilde,
		MathAlign:    opts.MathAlign,
		MathWrap:     opts.MathWrap,
	}
}

// reindex recomputes the mutable indices on ctx after a source change.
func reindex(ctx *Ctx) {
	ctx.Tokens = parser.Tokenize(ctx.Src)
	ctx.Protected = parser.ProtectedSpansExtra(ctx.Src, ctx.verbatimEnvs)
	ctx.Lines = parser.LineOffsets(ctx.Src)
	ctx.Skip = BuildSkipMask(ctx.Src)
}

// ListRules returns a snapshot of every registered rule (id, tier label, doc).
// Order matches the registration order (= pipeline execution order).
func ListRules() []Rule {
	out := make([]Rule, len(Registry))
	for i, r := range Registry {
		out[i] = Rule{ID: r.ID, Tier: r.Tier, Doc: r.Doc}
	}
	return out
}

// ValidateRuleIDs checks that all rule IDs in ids exist in the Registry.
// Returns an error listing the first unknown ID, or nil if all are valid.
func ValidateRuleIDs(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	known := make(map[string]bool, len(Registry))
	for _, r := range Registry {
		known[r.ID] = true
	}
	for _, id := range ids {
		if !known[id] {
			return fmt.Errorf("unknown rule %q", id)
		}
	}
	return nil
}

// lineCountChangingRules is the set of rule IDs whose Apply may change the
// number of lines in the source. These are force-disabled under --lines.
var lineCountChangingRules = map[string]bool{
	"space.blank-runs":             true,
	"space.wrap":                   true,
	"space.item-per-line":          true,
	"space.proof-delim-per-line":   true,
	"space.display-delim-per-line": true,
	"display.style":                true,
	"math.paragraph-suppress":      true,
	"math.wrap-at-break-op":        true,
	"env.spacing":                  true,
}

// enabledRules filters Registry according to opts.
func enabledRules(opts Options) []Rule {
	ruleSet := make(map[string]bool, len(opts.Rules))
	for _, id := range opts.Rules {
		ruleSet[id] = true
	}
	skipSet := make(map[string]bool, len(opts.SkipRules))
	for _, id := range opts.SkipRules {
		skipSet[id] = true
	}

	var out []Rule
	for _, r := range Registry {
		// If explicit rule list given, filter to it.
		if len(ruleSet) > 0 {
			if !ruleSet[r.ID] {
				continue
			}
		} else {
			// Default filtering by tier.
			switch r.Tier {
			case Safe:
				// Always enabled.
			case PDFFix:
				if !opts.PDFFix {
					continue
				}
			case DiagOnly:
				if !opts.Diag {
					continue
				}
			}
		}
		// Skip-list overrides everything else.
		if skipSet[r.ID] {
			continue
		}
		// Force-disable line-count-changing rules when --lines is active.
		if opts.LineRange != nil && lineCountChangingRules[r.ID] {
			continue
		}
		out = append(out, r)
	}
	return out
}

// SkippedLineRangeRules returns the IDs of rules that were force-disabled
// because --lines is active.
func SkippedLineRangeRules(opts Options) []string {
	if opts.LineRange == nil {
		return nil
	}
	ruleSet := make(map[string]bool, len(opts.Rules))
	for _, id := range opts.Rules {
		ruleSet[id] = true
	}
	skipSet := make(map[string]bool, len(opts.SkipRules))
	for _, id := range opts.SkipRules {
		skipSet[id] = true
	}
	var skipped []string
	for _, r := range Registry {
		if !lineCountChangingRules[r.ID] {
			continue
		}
		// Only report skip if the rule would otherwise be enabled.
		if len(ruleSet) > 0 {
			if !ruleSet[r.ID] {
				continue
			}
		} else {
			switch r.Tier {
			case PDFFix:
				if !opts.PDFFix {
					continue
				}
			case DiagOnly:
				if !opts.Diag {
					continue
				}
			}
		}
		// Already skipped by SkipRules — don't double-report.
		if skipSet[r.ID] {
			continue
		}
		skipped = append(skipped, r.ID)
	}
	return skipped
}
