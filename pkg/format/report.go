// Package format holds the read side of mreview's fmt-report machinery.
// mrevdiff never runs the fmt rule engine; it only reads the
// `<paper>.tex.fmt-report.md` files that `mreview fmt` writes, so lint
// diagnostics surface as outline markers in the diff UI. The report
// header regex below deliberately says "mreview" — that is the header
// the producing tool writes.
package format

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"mrevdiff/pkg/persist"
)

// Report holds the structured content of a paper.tex.fmt-report.md file.
type Report struct {
	File     string         // base filename (e.g. "paper.tex")
	Date     time.Time      // when the report was generated
	Tier     string         // e.g. "safe", "safe+pdf-fix"
	Verify   string         // e.g. "text-layer (ok)", "skipped", "text-layer (FAILED)"
	Rewrites []RewriteGroup // per-rule hit summaries
	Warnings []string       // verifier warnings
	Diags    []ReportDiag   // Tier-3 diagnostics
}

// RewriteGroup summarises hits from a single rule.
type RewriteGroup struct {
	RuleID string
	Count  int
	Lines  []int // representative line numbers
}

// ReportDiag is a single diagnostic from the report file.
type ReportDiag struct {
	RuleID  string
	Line    int
	Message string
}

// WriteReport writes a paper.tex.fmt-report.md file at reportPath.
// The file is written atomically: a partial report cannot be observed by
// the TUI's external-issues loader after a crash mid-write. Kept in
// mrevdiff mainly for tests; production reports come from `mreview fmt`.
func WriteReport(reportPath string, rpt Report) error {
	w := &bytes.Buffer{}

	_, _ = fmt.Fprintf(w, "# mreview fmt report — %s\n", rpt.File)
	_, _ = fmt.Fprintf(w, "date: %s\n", rpt.Date.UTC().Format(time.RFC3339))
	_, _ = fmt.Fprintf(w, "tier: %s\n", rpt.Tier)
	_, _ = fmt.Fprintf(w, "verify: %s\n", rpt.Verify)

	// Rewrites section.
	totalHits := 0
	for _, g := range rpt.Rewrites {
		totalHits += g.Count
	}
	if len(rpt.Rewrites) > 0 {
		_, _ = fmt.Fprintf(w, "\n## Rewrites (%d)\n", totalHits)
		for _, g := range rpt.Rewrites {
			lineStrs := make([]string, 0, len(g.Lines))
			for _, ln := range g.Lines {
				lineStrs = append(lineStrs, fmt.Sprintf("L%d", ln))
			}
			if len(lineStrs) > 5 {
				lineStrs = append(lineStrs[:5], "…")
			}
			_, _ = fmt.Fprintf(w, "- %s — %d hits (%s)\n", g.RuleID, g.Count, strings.Join(lineStrs, ", "))
		}
	}

	// Verifier warnings.
	if len(rpt.Warnings) > 0 {
		_, _ = fmt.Fprintf(w, "\n## Verifier warnings (%d)\n", len(rpt.Warnings))
		for _, warn := range rpt.Warnings {
			_, _ = fmt.Fprintf(w, "- %s\n", warn)
		}
	}

	// Diagnostics section.
	if len(rpt.Diags) > 0 {
		_, _ = fmt.Fprintf(w, "\n## Diagnostics (Tier 3, %d issues)\n", len(rpt.Diags))
		for _, d := range rpt.Diags {
			_, _ = fmt.Fprintf(w, "- %s L%d — %s\n", d.RuleID, d.Line, d.Message)
		}
	}

	return persist.WriteFileAtomic(reportPath, w.Bytes())
}

// LoadReport parses a paper.tex.fmt-report.md file into a Report.
func LoadReport(path string) (*Report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseReport(string(data))
}

// ParseReport parses the markdown content of a report into a Report struct.
func ParseReport(content string) (*Report, error) {
	rpt := &Report{}
	lines := strings.Split(content, "\n")

	// Parse header. The literal "mreview" is the producing tool's name.
	if len(lines) > 0 {
		if m := regexp.MustCompile(`^# mreview fmt report — (.+)$`).FindStringSubmatch(lines[0]); m != nil {
			rpt.File = m[1]
		}
	}

	for _, line := range lines {
		if strings.HasPrefix(line, "date: ") {
			if t, err := time.Parse(time.RFC3339, strings.TrimPrefix(line, "date: ")); err == nil {
				rpt.Date = t
			}
		}
		if strings.HasPrefix(line, "tier: ") {
			rpt.Tier = strings.TrimPrefix(line, "tier: ")
		}
		if strings.HasPrefix(line, "verify: ") {
			rpt.Verify = strings.TrimPrefix(line, "verify: ")
		}
	}

	// Parse sections by tracking which section we're in.
	section := ""
	for _, line := range lines {
		if strings.HasPrefix(line, "## Rewrites") {
			section = "rewrites"
			continue
		}
		if strings.HasPrefix(line, "## Verifier warnings") {
			section = "warnings"
			continue
		}
		if strings.HasPrefix(line, "## Diagnostics") {
			section = "diags"
			continue
		}
		if strings.HasPrefix(line, "## ") {
			section = ""
			continue
		}

		if !strings.HasPrefix(line, "- ") {
			continue
		}
		item := strings.TrimPrefix(line, "- ")

		switch section {
		case "rewrites":
			g := parseRewriteLine(item)
			if g.RuleID != "" {
				rpt.Rewrites = append(rpt.Rewrites, g)
			}
		case "warnings":
			rpt.Warnings = append(rpt.Warnings, item)
		case "diags":
			d := parseDiagLine(item)
			if d.RuleID != "" {
				rpt.Diags = append(rpt.Diags, d)
			}
		}
	}

	return rpt, nil
}

// parseRewriteLine parses a line like: "space.trailing — 14 hits (L12, L88, L134, …)"
var rewriteLineRe = regexp.MustCompile(`^(\S+)\s+—\s+(\d+)\s+hits?\s+\(([^)]*)\)`)

func parseRewriteLine(s string) RewriteGroup {
	m := rewriteLineRe.FindStringSubmatch(s)
	if m == nil {
		return RewriteGroup{}
	}
	count, _ := strconv.Atoi(m[2])
	var lines []int
	for _, part := range strings.Split(m[3], ",") {
		part = strings.TrimSpace(part)
		if part == "…" || part == "" {
			continue
		}
		part = strings.TrimPrefix(part, "L")
		if n, err := strconv.Atoi(part); err == nil {
			lines = append(lines, n)
		}
	}
	return RewriteGroup{
		RuleID: m[1],
		Count:  count,
		Lines:  lines,
	}
}

// parseDiagLine parses a line like: "lint.label-unused L612 — `eq:tilde-w-extra` declared at L612, never referenced."
// Also handles the legacy format without a structured line number: "lint.label-unused — message".
var diagLineReNew = regexp.MustCompile(`^(\S+)\s+L(\d+)\s+—\s+(.+)`)
var diagLineReLegacy = regexp.MustCompile(`^(\S+)\s+—\s+(.+)`)
var diagLineNumRe = regexp.MustCompile(`L(\d+)`)

func parseDiagLine(s string) ReportDiag {
	// Try the new format first: "ruleID L<line> — message"
	if m := diagLineReNew.FindStringSubmatch(s); m != nil {
		line, _ := strconv.Atoi(m[2])
		return ReportDiag{
			RuleID:  m[1],
			Line:    line,
			Message: m[3],
		}
	}
	// Fall back to legacy format: "ruleID — message"
	m := diagLineReLegacy.FindStringSubmatch(s)
	if m == nil {
		return ReportDiag{}
	}
	d := ReportDiag{
		RuleID:  m[1],
		Message: m[2],
	}
	// Try to extract a line number from the message body.
	if lm := diagLineNumRe.FindStringSubmatch(d.Message); lm != nil {
		d.Line, _ = strconv.Atoi(lm[1])
	}
	return d
}

// ReportPath returns the fmt-report.md path for a given paper path.
// e.g. "/path/to/paper.tex" -> "/path/to/paper.tex.fmt-report.md"
func ReportPath(paperPath string) string {
	return paperPath + ".fmt-report.md"
}
