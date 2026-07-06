package format

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteAndLoadReport_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "paper.tex.fmt-report.md")

	rpt := Report{
		File:   "paper.tex",
		Date:   time.Date(2026, 4, 24, 15, 32, 11, 0, time.UTC),
		Tier:   "safe+pdf-fix",
		Verify: "text-layer (ok)",
		Rewrites: []RewriteGroup{
			{RuleID: "space.trailing", Count: 14, Lines: []int{12, 88, 134, 200, 310}},
			{RuleID: "math.paragraph-suppress", Count: 3, Lines: []int{308, 330, 417}},
		},
		Warnings: []string{
			"math.paragraph-suppress hit at L1188 produced no PDF change — heuristic may be too aggressive",
		},
		Diags: []ReportDiag{
			{RuleID: "lint.label-unused", Line: 612, Message: "`eq:tilde-w-extra` declared at L612, never referenced."},
			{RuleID: "lint.thm-no-proof", Line: 451, Message: "Theorem 4.2 at L451 has no following proof in next 5 blocks."},
		},
	}

	err := WriteReport(path, rpt)
	require.NoError(t, err)

	// Read back.
	loaded, err := LoadReport(path)
	require.NoError(t, err)
	require.NotNil(t, loaded)

	assert.Equal(t, "paper.tex", loaded.File)
	assert.Equal(t, rpt.Date, loaded.Date)
	assert.Equal(t, "safe+pdf-fix", loaded.Tier)
	assert.Equal(t, "text-layer (ok)", loaded.Verify)

	// Rewrites.
	require.Len(t, loaded.Rewrites, 2)
	assert.Equal(t, "space.trailing", loaded.Rewrites[0].RuleID)
	assert.Equal(t, 14, loaded.Rewrites[0].Count)
	assert.Equal(t, []int{12, 88, 134, 200, 310}, loaded.Rewrites[0].Lines)

	assert.Equal(t, "math.paragraph-suppress", loaded.Rewrites[1].RuleID)
	assert.Equal(t, 3, loaded.Rewrites[1].Count)

	// Warnings.
	require.Len(t, loaded.Warnings, 1)
	assert.Contains(t, loaded.Warnings[0], "math.paragraph-suppress")

	// Diagnostics.
	require.Len(t, loaded.Diags, 2)
	assert.Equal(t, "lint.label-unused", loaded.Diags[0].RuleID)
	assert.Equal(t, 612, loaded.Diags[0].Line)
	assert.Contains(t, loaded.Diags[0].Message, "eq:tilde-w-extra")

	assert.Equal(t, "lint.thm-no-proof", loaded.Diags[1].RuleID)
	assert.Equal(t, 451, loaded.Diags[1].Line)
}

func TestLoadReport_FileNotFound(t *testing.T) {
	_, err := LoadReport("/nonexistent/path.md")
	assert.Error(t, err)
}

func TestParseReport_EmptyContent(t *testing.T) {
	rpt, err := ParseReport("")
	require.NoError(t, err)
	assert.Equal(t, "", rpt.File)
	assert.Empty(t, rpt.Rewrites)
	assert.Empty(t, rpt.Diags)
}

func TestParseReport_OnlyHeader(t *testing.T) {
	content := `# mreview fmt report — main.tex
date: 2026-04-24T10:00:00Z
tier: safe
verify: skipped
`
	rpt, err := ParseReport(content)
	require.NoError(t, err)
	assert.Equal(t, "main.tex", rpt.File)
	assert.Equal(t, "safe", rpt.Tier)
	assert.Equal(t, "skipped", rpt.Verify)
}

func TestWriteReport_TruncatesLongLineList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.fmt-report.md")

	rpt := Report{
		File:   "test.tex",
		Date:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Tier:   "safe",
		Verify: "skipped",
		Rewrites: []RewriteGroup{
			{RuleID: "space.trailing", Count: 10, Lines: []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}},
		},
	}

	err := WriteReport(path, rpt)
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	content := string(data)

	// Should have at most 5 line numbers plus "…"
	assert.Contains(t, content, "L5")
	assert.Contains(t, content, "…")
	assert.NotContains(t, content, "L6")
}

func TestReportPath(t *testing.T) {
	assert.Equal(t, "/path/to/paper.tex.fmt-report.md", ReportPath("/path/to/paper.tex"))
}

func TestParseRewriteLine(t *testing.T) {
	g := parseRewriteLine("space.trailing — 14 hits (L12, L88, L134, L200, L310)")
	assert.Equal(t, "space.trailing", g.RuleID)
	assert.Equal(t, 14, g.Count)
	assert.Equal(t, []int{12, 88, 134, 200, 310}, g.Lines)
}

func TestParseRewriteLine_WithEllipsis(t *testing.T) {
	g := parseRewriteLine("space.blank-runs — 4 hits (L221, L408, …)")
	assert.Equal(t, "space.blank-runs", g.RuleID)
	assert.Equal(t, 4, g.Count)
	assert.Equal(t, []int{221, 408}, g.Lines)
}

func TestParseDiagLine(t *testing.T) {
	d := parseDiagLine("lint.label-unused — `eq:tilde-w-extra` declared at L612, never referenced.")
	assert.Equal(t, "lint.label-unused", d.RuleID)
	assert.Equal(t, 612, d.Line)
	assert.Contains(t, d.Message, "eq:tilde-w-extra")
}

func TestParseDiagLine_NoLineNumber(t *testing.T) {
	d := parseDiagLine("lint.todo-marker — found TODO marker")
	assert.Equal(t, "lint.todo-marker", d.RuleID)
	assert.Equal(t, 0, d.Line)
	assert.Equal(t, "found TODO marker", d.Message)
}

func TestWriteReport_NoDiags(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")

	rpt := Report{
		File:   "test.tex",
		Date:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Tier:   "safe",
		Verify: "skipped",
	}

	err := WriteReport(path, rpt)
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	content := string(data)

	// Should not contain diagnostics section.
	assert.False(t, strings.Contains(content, "## Diagnostics"))
	// Should not contain rewrites section.
	assert.False(t, strings.Contains(content, "## Rewrites"))
}
