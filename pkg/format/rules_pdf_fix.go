package format

import (
	"bytes"
	"regexp"
	"sort"

	"github.com/lenis2000/mrevdiff/pkg/parser"
)

// registerPDFFixRules appends the Tier-2 rules to the registry.
// Called from registry.go's init() to guarantee ordering after Safe rules.
func registerPDFFixRules() {
	Registry = append(Registry,
		Rule{
			ID:    "math.paragraph-suppress",
			Tier:  PDFFix,
			Doc:   "Remove blank lines around display-math envs when no strong paragraph signal exists.",
			Apply: applyMathParagraphSuppress,
		},
		Rule{
			ID:    "env.spacing",
			Tier:  PDFFix,
			Doc:   "Ensure exactly one blank line above theorem-like envs and section commands.",
			Apply: applyEnvSpacing,
		},
	)
}

// displayMathEnvs is the set of environment names that constitute display math.
var displayMathEnvs = map[string]bool{
	"equation":  true,
	"equation*": true,
	"align":     true,
	"align*":    true,
	"gather":    true,
	"gather*":   true,
	"multline":  true,
	"multline*": true,
	"flalign":   true,
	"flalign*":  true,
	"alignat":   true,
	"alignat*":  true,
}

// strongParagraphEndRe matches a line ending with sentence-final punctuation.
var strongParagraphEndRe = regexp.MustCompile(`[.?!]\s*$`)

// startsUpperRe matches a line starting with an uppercase letter (after optional whitespace).
var startsUpperRe = regexp.MustCompile(`^\s*[A-Z]`)

// ---------------------------------------------------------------------------
// math.paragraph-suppress
// ---------------------------------------------------------------------------

// applyMathParagraphSuppress removes blank lines around display-math
// environments unless a strong paragraph signal is detected.
//
// Algorithm:
//  1. Build a list of display-math regions (lines) from tokens.
//  2. Merge adjacent regions separated only by blank lines into chains.
//  3. For each chain, check the outer boundaries:
//     - If the line above ends with sentence-final punctuation AND the line
//     below starts with uppercase, leave alone (strong paragraph signal).
//     - Otherwise, collapse the blank lines at both ends.
//  4. Inner gaps (between consecutive display-math envs in a chain) always
//     collapse.
func applyMathParagraphSuppress(ctx *Ctx) Result {
	regions := findDisplayMathRegions(ctx)
	if len(regions) == 0 {
		return Result{Src: ctx.Src}
	}

	chains := mergeIntoChains(regions, ctx.Src, ctx.Lines)
	if len(chains) == 0 {
		return Result{Src: ctx.Src}
	}

	// Collect all blank-line ranges to remove, sorted by position.
	type removal struct {
		start, end int // byte range of blanks to collapse (\n\n+ -> \n)
		srcLine    int // 1-based line for the hit
	}
	var removals []removal

	for _, ch := range chains {
		// Process inner gaps first (always collapse).
		for _, gap := range ch.innerGaps {
			removals = append(removals, removal{
				start:   gap.start,
				end:     gap.end,
				srcLine: lineAt(ctx.Lines, gap.start),
			})
		}

		// Check for strong paragraph signal. Both conditions must hold:
		// 1. The content line above the chain ends with sentence-final punctuation (. ? !)
		// 2. The content line below the chain starts with an uppercase letter
		// If BOTH hold, leave the outer blank lines alone. Otherwise, collapse them.
		hasAboveBlanks := ch.blankAbove.end > ch.blankAbove.start
		hasBelowBlanks := ch.blankBelow.end > ch.blankBelow.start

		aboveSignal := hasAboveBlanks && lineAboveEndsSentence(ctx.Src, ctx.Lines, ch.firstRegionStartLine)
		belowSignal := hasBelowBlanks && lineBelowStartsUpper(ctx.Src, ctx.Lines, ch.lastRegionEndLine)
		strongSignal := aboveSignal && belowSignal

		if hasAboveBlanks && !strongSignal {
			removals = append(removals, removal{
				start:   ch.blankAbove.start,
				end:     ch.blankAbove.end,
				srcLine: lineAt(ctx.Lines, ch.blankAbove.start),
			})
		}
		if hasBelowBlanks && !strongSignal {
			removals = append(removals, removal{
				start:   ch.blankBelow.start,
				end:     ch.blankBelow.end,
				srcLine: lineAt(ctx.Lines, ch.blankBelow.start),
			})
		}
	}

	// Drop any removal that overlaps a `% mrevdiff-fmt: skip/off/on`-masked line.
	if len(removals) > 0 && len(ctx.Skip) > 1 {
		filtered := removals[:0]
		for _, r := range removals {
			if ctx.RangeSkipped(r.start, r.end) {
				continue
			}
			filtered = append(filtered, r)
		}
		removals = filtered
	}

	if len(removals) == 0 {
		return Result{Src: ctx.Src}
	}

	// Sort removals by start position.
	sort.Slice(removals, func(i, j int) bool {
		return removals[i].start < removals[j].start
	})

	// Apply removals: delete each blank-line byte range entirely.
	// The content line above (or the display-math close line below) already
	// has its own \n, so removing the blank-line bytes joins the lines
	// with exactly one \n between them.
	//
	// Track the output byte position corresponding to each r.end so we can
	// compute ExpectedDiffSourceLines using the AFTER-rule line table.
	// The verifier resolves these line numbers against the AFTER synctex.
	var out []byte
	type hitInfo struct {
		srcLine    int
		outBytePos int // position of r.end content in output
	}
	var hitInfos []hitInfo
	prev := 0
	for _, r := range removals {
		if r.start < prev {
			continue // overlapping removal, skip
		}
		out = append(out, ctx.Src[prev:r.start]...)
		outPos := len(out) // byte position in output where r.end content begins
		prev = r.end

		hitInfos = append(hitInfos, hitInfo{
			srcLine:    r.srcLine,
			outBytePos: outPos,
		})
	}
	out = append(out, ctx.Src[prev:]...)

	// Recompute expected-diff lines using the output's line offsets.
	outLines := parser.LineOffsets(out)
	var hits []Hit
	for _, hi := range hitInfos {
		expectedLine := lineAt(outLines, hi.outBytePos)
		if expectedLine < 1 {
			expectedLine = 1
		}
		hits = append(hits, Hit{
			RuleID:                  "math.paragraph-suppress",
			Line:                    hi.srcLine,
			ExpectedDiffSourceLines: []int{expectedLine},
			Excerpt:                 "collapsed blank lines around display math",
		})
	}

	return Result{Src: out, Hits: hits}
}

// displayMathRegion represents one display-math block in the source.
type displayMathRegion struct {
	startLine int // 1-based line of the \begin or \[
	endLine   int // 1-based line of the \end or \]
}

// findDisplayMathRegions scans tokens for display-math environments and \[...\].
func findDisplayMathRegions(ctx *Ctx) []displayMathRegion {
	var regions []displayMathRegion

	// Track \begin{equation} etc.
	for i := 0; i < len(ctx.Tokens); i++ {
		tok := ctx.Tokens[i]
		switch tok.Kind {
		case parser.TokBeginEnv:
			if !displayMathEnvs[tok.EnvName] {
				continue
			}
			startLine := tok.Line
			// Find matching \end.
			endLine := startLine
			for j := i + 1; j < len(ctx.Tokens); j++ {
				if ctx.Tokens[j].Kind == parser.TokEndEnv && ctx.Tokens[j].EnvName == tok.EnvName {
					endLine = ctx.Tokens[j].Line
					i = j
					break
				}
			}
			// Check if inside protected region using the token's actual byte
			// position (not the line start) so inline protected content like
			// \verb|...| is correctly detected.
			tokOff := tokenByteOffset(ctx.Lines, tok)
			if parser.OverlapsProtected(tokOff, tokOff+1, ctx.Protected) {
				continue
			}
			regions = append(regions, displayMathRegion{startLine: startLine, endLine: endLine})

		case parser.TokDisplayOpen:
			startLine := tok.Line
			// Find matching TokDisplayClose.
			endLine := startLine
			for j := i + 1; j < len(ctx.Tokens); j++ {
				if ctx.Tokens[j].Kind == parser.TokDisplayClose {
					endLine = ctx.Tokens[j].Line
					i = j
					break
				}
			}
			tokOff := tokenByteOffset(ctx.Lines, tok)
			if parser.OverlapsProtected(tokOff, tokOff+1, ctx.Protected) {
				continue
			}
			regions = append(regions, displayMathRegion{startLine: startLine, endLine: endLine})
		}
	}
	return regions
}

// byteRange represents a byte range in the source.
type byteRange struct {
	start, end int
}

// chain represents a chain of display-math regions separated by blank lines.
type chain struct {
	firstRegionStartLine int
	lastRegionEndLine    int
	blankAbove           byteRange   // blank lines above the first region
	blankBelow           byteRange   // blank lines below the last region
	innerGaps            []byteRange // blank lines between consecutive regions
}

// mergeIntoChains groups adjacent display-math regions separated only by blank lines.
func mergeIntoChains(regions []displayMathRegion, src []byte, lines []int) []chain {
	var chains []chain

	i := 0
	for i < len(regions) {
		c := chain{
			firstRegionStartLine: regions[i].startLine,
			lastRegionEndLine:    regions[i].endLine,
		}

		// Try to extend the chain by merging subsequent regions.
		j := i + 1
		for j < len(regions) {
			// Check if the gap between regions[j-1].endLine and regions[j].startLine
			// consists only of blank lines.
			gapStart := regions[j-1].endLine
			gapEnd := regions[j].startLine
			if gapEnd <= gapStart+1 {
				// Adjacent or overlapping lines, merge without gap.
				c.lastRegionEndLine = regions[j].endLine
				j++
				continue
			}
			if allBlankLines(src, lines, gapStart+1, gapEnd-1) {
				// Record the inner gap for collapsing.
				blankStart := lineEndOffset(src, lines, gapStart)
				blankEnd := lineStartOffset(lines, gapEnd)
				if blankEnd > blankStart {
					c.innerGaps = append(c.innerGaps, byteRange{start: blankStart, end: blankEnd})
				}
				c.lastRegionEndLine = regions[j].endLine
				j++
				continue
			}
			break // non-blank content between regions, can't merge
		}

		// Find blank lines above the first region.
		c.blankAbove = findBlankRunAbove(src, lines, c.firstRegionStartLine)
		// Find blank lines below the last region.
		c.blankBelow = findBlankRunBelow(src, lines, c.lastRegionEndLine)

		chains = append(chains, c)
		i = j
	}

	return chains
}

// tokenByteOffset returns the byte offset of a token in the source, computed
// from the token's 1-based Line and Col and the line offset table.
func tokenByteOffset(lines []int, tok parser.Token) int {
	return lineStartOffset(lines, tok.Line) + tok.Col - 1
}

// lineStartOffset returns the byte offset of the start of 1-based line n.
func lineStartOffset(lines []int, n int) int {
	if n < 1 {
		return 0
	}
	if n >= len(lines) {
		return lines[len(lines)-1]
	}
	return lines[n]
}

// lineEndOffset returns the byte offset just after the newline terminating 1-based line n.
// If n is the last line with no trailing newline, returns len(src).
func lineEndOffset(src []byte, lines []int, n int) int {
	if n+1 < len(lines) {
		return lines[n+1]
	}
	return len(src)
}

// allBlankLines reports whether all lines in [fromLine, toLine] (inclusive, 1-based)
// contain only whitespace.
func allBlankLines(src []byte, lines []int, fromLine, toLine int) bool {
	for l := fromLine; l <= toLine; l++ {
		start := lineStartOffset(lines, l)
		end := lineEndOffset(src, lines, l)
		line := src[start:end]
		if len(bytes.TrimSpace(line)) > 0 {
			return false
		}
	}
	return true
}

// findBlankRunAbove finds the contiguous run of blank lines immediately above
// the given 1-based line number. Returns the byte range of the blank lines
// (the newlines that would be collapsed).
func findBlankRunAbove(src []byte, lines []int, startLine int) byteRange {
	if startLine <= 1 {
		return byteRange{}
	}
	// Walk backwards from the line above startLine.
	topBlank := startLine - 1
	for topBlank >= 1 {
		start := lineStartOffset(lines, topBlank)
		end := lineEndOffset(src, lines, topBlank)
		line := src[start:end]
		if len(bytes.TrimSpace(line)) > 0 {
			break
		}
		topBlank--
	}
	topBlank++ // first blank line

	if topBlank > startLine-1 {
		return byteRange{} // no blank lines above
	}

	// Byte range: from end of line (topBlank-1) to start of startLine.
	rangeStart := lineEndOffset(src, lines, topBlank-1)
	rangeEnd := lineStartOffset(lines, startLine)
	// We want to keep one newline (the one ending the content line above),
	// so the blank run is from rangeStart to rangeEnd.
	// But we actually want: the blank lines form a \n\n+ run. We need to
	// identify the byte range that, when collapsed to \n, removes the blanks.
	// rangeStart points to the byte just after the newline of the last content line.
	// rangeEnd points to the start of the display-math line.
	// The bytes in [rangeStart, rangeEnd) are the blank lines (each is just \n or whitespace+\n).
	// We want to replace them with nothing (the content line already ends with \n).
	// Actually the content line's \n is at rangeStart-1. So the blank-line bytes
	// are src[rangeStart:rangeEnd]. Collapsing means: remove these bytes entirely,
	// keeping only the \n at the end of the content line.
	if rangeEnd <= rangeStart {
		return byteRange{}
	}
	return byteRange{start: rangeStart, end: rangeEnd}
}

// findBlankRunBelow finds the contiguous run of blank lines immediately below
// the given 1-based line number (the end line of a display-math region).
func findBlankRunBelow(src []byte, lines []int, endLine int) byteRange {
	maxLine := len(lines) - 1
	if endLine >= maxLine {
		return byteRange{}
	}
	// Walk forward from the line below endLine.
	botBlank := endLine + 1
	for botBlank <= maxLine {
		start := lineStartOffset(lines, botBlank)
		end := lineEndOffset(src, lines, botBlank)
		line := src[start:end]
		if len(bytes.TrimSpace(line)) > 0 {
			break
		}
		botBlank++
	}
	botBlank-- // last blank line

	if botBlank < endLine+1 {
		return byteRange{} // no blank lines below
	}

	// Byte range: from end of endLine to start of first non-blank line below.
	rangeStart := lineEndOffset(src, lines, endLine)
	rangeEnd := lineStartOffset(lines, botBlank+1)
	// The \n at end of endLine stays. The blank-line bytes are the range
	// from rangeStart to rangeEnd. But rangeEnd is the start of the first
	// content line below. Hmm - we need to check: rangeEnd might be past the
	// last blank line but before the content.
	// Actually: rangeStart = byte after \n of endLine. rangeEnd = start of first
	// non-blank line below. These bytes are the blank lines. Collapsing means
	// removing them entirely (the endLine already has its \n).
	if rangeEnd <= rangeStart {
		return byteRange{}
	}
	return byteRange{start: rangeStart, end: rangeEnd}
}

// lineAboveEndsSentence reports whether the last non-blank content line above
// the given display-math start line ends with sentence-final punctuation (. ? !).
func lineAboveEndsSentence(src []byte, lines []int, regionStartLine int) bool {
	contentLine := regionStartLine - 1
	for contentLine >= 1 {
		start := lineStartOffset(lines, contentLine)
		end := lineEndOffset(src, lines, contentLine)
		line := src[start:end]
		if len(bytes.TrimSpace(line)) > 0 {
			break
		}
		contentLine--
	}
	if contentLine < 1 {
		return false
	}
	content := getLineContent(src, lines, contentLine)
	return strongParagraphEndRe.Match(bytes.TrimSpace(content))
}

// lineBelowStartsUpper reports whether the first non-blank content line below
// the given display-math end line starts with an uppercase letter.
func lineBelowStartsUpper(src []byte, lines []int, regionEndLine int) bool {
	maxLine := len(lines) - 1
	contentLine := regionEndLine + 1
	for contentLine <= maxLine {
		start := lineStartOffset(lines, contentLine)
		end := lineEndOffset(src, lines, contentLine)
		line := src[start:end]
		if len(bytes.TrimSpace(line)) > 0 {
			break
		}
		contentLine++
	}
	if contentLine > maxLine {
		return false
	}
	content := getLineContent(src, lines, contentLine)
	return startsUpperRe.Match(bytes.TrimSpace(content))
}

// getLineContent returns the content of 1-based line n as a byte slice.
func getLineContent(src []byte, lines []int, n int) []byte {
	start := lineStartOffset(lines, n)
	end := lineEndOffset(src, lines, n)
	// Strip trailing newline if present.
	if end > start && src[end-1] == '\n' {
		end--
	}
	return src[start:end]
}

// ---------------------------------------------------------------------------
// env.spacing
// ---------------------------------------------------------------------------

// spacingEnvs is the set of environment names that should have a blank line above.
var spacingEnvs = map[string]bool{
	"theorem":     true,
	"lemma":       true,
	"proposition": true,
	"corollary":   true,
	"definition":  true,
	"conjecture":  true,
	"figure":      true,
	"abstract":    true,
}

// applyEnvSpacing ensures exactly one blank line above theorem-like environments
// and section commands. If zero blank lines exist, it inserts one. If >= 1 blank
// lines exist, it leaves alone (collapsing is space.blank-runs's job).
func applyEnvSpacing(ctx *Ctx) Result {
	// Collect insertion points: byte offsets where we need to insert a \n.
	type insertion struct {
		offset  int // byte offset just before the env/section line where we insert \n
		srcLine int // 1-based line of the env/section
	}
	var insertions []insertion

	for _, tok := range ctx.Tokens {
		var targetLine int
		switch tok.Kind {
		case parser.TokBeginEnv:
			if !spacingEnvs[tok.EnvName] {
				continue
			}
			targetLine = tok.Line
		case parser.TokSection:
			targetLine = tok.Line
		default:
			continue
		}

		// Check if inside a protected region using the token's actual
		// byte position (not the line start) so inline protected content
		// like \verb|...| is correctly detected.
		tokOff := tokenByteOffset(ctx.Lines, tok)
		if parser.OverlapsProtected(tokOff, tokOff+1, ctx.Protected) {
			continue
		}

		// Check if the env/section is at the start of the file (no line above).
		if targetLine <= 1 {
			continue
		}

		// Honour `% mrevdiff-fmt: skip/off/on` on the env line or the line
		// above (we'd otherwise be inserting a blank line into a masked
		// region).
		if ctx.LineSkipped(targetLine) || ctx.LineSkipped(targetLine-1) {
			continue
		}

		// Check the line above: is it blank?
		lineAbove := targetLine - 1
		aboveStart := lineStartOffset(ctx.Lines, lineAbove)
		aboveEnd := lineEndOffset(ctx.Src, ctx.Lines, lineAbove)
		aboveLine := ctx.Src[aboveStart:aboveEnd]
		if len(bytes.TrimSpace(aboveLine)) == 0 {
			continue // already has at least one blank line
		}

		// Need to insert a blank line. Insert at the start of targetLine
		// (which means adding a \n before the existing content).
		lineOff := lineStartOffset(ctx.Lines, targetLine)
		insertions = append(insertions, insertion{
			offset:  lineOff,
			srcLine: targetLine,
		})
	}

	if len(insertions) == 0 {
		return Result{Src: ctx.Src}
	}

	// Deduplicate insertions at the same offset.
	seen := make(map[int]bool)
	var deduped []insertion
	for _, ins := range insertions {
		if !seen[ins.offset] {
			seen[ins.offset] = true
			deduped = append(deduped, ins)
		}
	}
	insertions = deduped

	// Sort by offset.
	sort.Slice(insertions, func(i, j int) bool {
		return insertions[i].offset < insertions[j].offset
	})

	// Build output with insertions. Track output byte positions so we
	// can compute ExpectedDiffSourceLines using the AFTER-rule line table.
	// The verifier resolves these line numbers against the AFTER synctex.
	var out []byte
	type insInfo struct {
		srcLine    int
		outBytePos int // position of the env/section line in output (after the inserted \n)
	}
	var insInfos []insInfo
	prev := 0
	for _, ins := range insertions {
		out = append(out, ctx.Src[prev:ins.offset]...)
		out = append(out, '\n') // insert blank line
		outPos := len(out)      // byte position of the env/section line in output
		prev = ins.offset

		insInfos = append(insInfos, insInfo{
			srcLine:    ins.srcLine,
			outBytePos: outPos,
		})
	}
	out = append(out, ctx.Src[prev:]...)

	// Recompute expected-diff lines using the output's line offsets.
	outLines := parser.LineOffsets(out)
	var hits []Hit
	for _, ii := range insInfos {
		envLine := lineAt(outLines, ii.outBytePos)
		if envLine < 1 {
			envLine = 1
		}
		aboveLine := envLine - 1
		if aboveLine < 1 {
			aboveLine = 1
		}
		hits = append(hits, Hit{
			RuleID:                  "env.spacing",
			Line:                    ii.srcLine,
			ExpectedDiffSourceLines: []int{aboveLine, envLine},
			Excerpt:                 "inserted blank line above env/section",
		})
	}

	return Result{Src: out, Hits: hits}
}
