package ui

import (
	"os"

	"github.com/lenis2000/mrevdiff/pkg/format"
	"github.com/lenis2000/mrevdiff/pkg/parser"
)

// LoadExternalIssues loads a fmt-report.md file (produced by `mrevdiff fmt`)
// and maps its diagnostics to owning blocks by line number. Returns nil
// (not an error) when the report file does not exist.
func LoadExternalIssues(reportPath string, doc *parser.Document) (map[string][]format.ReportDiag, error) {
	if _, err := os.Stat(reportPath); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	rpt, err := format.LoadReport(reportPath)
	if err != nil {
		return nil, err
	}
	if len(rpt.Diags) == 0 {
		return nil, nil
	}
	return mapDiagsToBlocks(rpt.Diags, doc), nil
}

// mapDiagsToBlocks maps each ReportDiag to the block whose [StartLine, EndLine]
// range contains the diagnostic's line. Diagnostics that don't fall in any
// block are mapped to the nearest block by line distance.
func mapDiagsToBlocks(diags []format.ReportDiag, doc *parser.Document) map[string][]format.ReportDiag {
	if doc == nil {
		return nil
	}
	m := make(map[string][]format.ReportDiag)
	for _, d := range diags {
		bid := findOwningBlock(d.Line, doc)
		m[bid] = append(m[bid], d)
	}
	return m
}

// findOwningBlock returns the ID of the deepest block whose line range
// contains line. When no block's range contains the line (e.g. a diagnostic
// in the preamble or after \end{document}), the function falls back to the
// nearest block by line distance so the diagnostic remains visible in the
// outline's issues filter. Returns "root" only when the document has no
// non-root blocks at all.
func findOwningBlock(line int, doc *parser.Document) string {
	if doc == nil || line <= 0 {
		return "root"
	}
	// Walk all blocks and find the narrowest (deepest) one containing line.
	bestID := "root"
	bestSpan := int(^uint(0) >> 1) // max int
	for _, b := range doc.Blocks {
		if b == nil || b.ID == "root" {
			continue
		}
		if b.StartLine <= line && line <= b.EndLine {
			span := b.EndLine - b.StartLine
			if span < bestSpan {
				bestSpan = span
				bestID = b.ID
			}
		}
	}
	if bestID != "root" {
		return bestID
	}
	// Fallback: assign to the nearest block by line distance so the
	// diagnostic is visible in the issues filter (the outline never
	// renders the synthetic root node).
	nearestID := "root"
	nearestDist := int(^uint(0) >> 1)
	for _, b := range doc.Blocks {
		if b == nil || b.ID == "root" {
			continue
		}
		var dist int
		if line < b.StartLine {
			dist = b.StartLine - line
		} else {
			dist = line - b.EndLine
		}
		if dist < nearestDist {
			nearestDist = dist
			nearestID = b.ID
		}
	}
	return nearestID
}
