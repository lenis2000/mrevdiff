package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeFmtFixture writes a .tex file with trailing whitespace so the
// Tier-1 space.trailing rule has something to rewrite.
func writeFmtFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	paper := filepath.Join(dir, "paper.tex")
	// Trailing spaces on "hi  " line trigger space.trailing.
	body := "\\documentclass{amsart}\n\\begin{document}\nhi  \n\\end{document}\n"
	if err := os.WriteFile(paper, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return paper
}

func TestFmt_PrintWritesToStdout(t *testing.T) {
	paper := writeFmtFixture(t)
	original, _ := os.ReadFile(paper)

	var stdout, stderr bytes.Buffer
	code := run([]string{"fmt", "--print", paper}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", code, stderr.String())
	}
	// Stdout has the formatted source (trailing whitespace stripped).
	if !strings.Contains(stdout.String(), "hi\n") {
		t.Fatalf("expected formatted output on stdout, got %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "hi  \n") {
		t.Fatalf("trailing whitespace must be stripped in stdout, got %q", stdout.String())
	}
	// File must NOT have been modified.
	current, _ := os.ReadFile(paper)
	if !bytes.Equal(original, current) {
		t.Fatalf("--print must not modify the file; before=%q after=%q", original, current)
	}
}

func TestFmt_PrintNoChanges(t *testing.T) {
	paper := writeFmtFixtureClean(t)

	var stdout, stderr bytes.Buffer
	code := run([]string{"fmt", "--print", paper}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", code, stderr.String())
	}
	if stdout.Len() == 0 {
		t.Fatalf("--print must emit even unchanged source to stdout")
	}
}

func TestFmt_PrintRejectsConflictingFlags(t *testing.T) {
	paper := writeFmtFixture(t)

	cases := []struct {
		name string
		args []string
	}{
		{"print+diff", []string{"fmt", "--print", "--diff", paper}},
		{"print+check", []string{"fmt", "--print", "--check", paper}},
		{"diff+check", []string{"fmt", "--diff", "--check", paper}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(c.args, &stdout, &stderr)
			if code != 2 {
				t.Fatalf("expected exit 2 for %v, got %d", c.args, code)
			}
			if !strings.Contains(stderr.String(), "mutually exclusive") {
				t.Fatalf("expected mutually-exclusive message, got %q", stderr.String())
			}
		})
	}
}

// writeFmtFixtureClean writes a .tex file that requires no formatting changes.
func writeFmtFixtureClean(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	paper := filepath.Join(dir, "paper.tex")
	body := "\\documentclass{amsart}\n\\begin{document}\nhi\n\\end{document}\n"
	if err := os.WriteFile(paper, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return paper
}

func TestFmt_MissingArg(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"fmt"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit 2, got %d (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "missing paper") {
		t.Fatalf("expected missing-paper error, got %q", stderr.String())
	}
}

func TestFmt_MultiFile_BothMissing(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"fmt", "/nonexistent/a.tex", "/nonexistent/b.tex"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1 for missing files, got %d", code)
	}
	// Both paths must show a cannot-read error.
	if c := strings.Count(stderr.String(), "cannot read"); c != 2 {
		t.Fatalf("expected cannot-read for both files, got %d in %q", c, stderr.String())
	}
}

func TestFmt_MultiFile_RewritesEach(t *testing.T) {
	a := writeFmtFixture(t)
	b := writeFmtFixture(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{"fmt", "--allow-dirty", "--no-verify", "--no-report", a, b}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", code, stderr.String())
	}
	for _, p := range []string{a, b} {
		body, _ := os.ReadFile(p)
		if strings.Contains(string(body), "hi  ") {
			t.Fatalf("trailing whitespace must be stripped from %q, got %q", p, body)
		}
	}
	// Progress lines should appear for each file.
	if !strings.Contains(stderr.String(), "[1/2]") || !strings.Contains(stderr.String(), "[2/2]") {
		t.Fatalf("expected progress markers, got %q", stderr.String())
	}
}

func TestFmt_MultiFile_RejectedWithPrintDiffCheck(t *testing.T) {
	a := writeFmtFixture(t)
	b := writeFmtFixture(t)
	for _, flag := range []string{"--print", "--diff", "--check"} {
		var stdout, stderr bytes.Buffer
		code := run([]string{"fmt", flag, a, b}, &stdout, &stderr)
		if code != 2 {
			t.Fatalf("expected exit 2 for %s with multi-file, got %d", flag, code)
		}
		if !strings.Contains(stderr.String(), "accept only one file") {
			t.Fatalf("expected single-file error for %s, got %q", flag, stderr.String())
		}
	}
}

func TestFmt_MissingFile(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"fmt", "/nonexistent/paper.tex"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "cannot read") {
		t.Fatalf("expected cannot-read error, got %q", stderr.String())
	}
}

func TestFmt_HelpExitsZero(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"fmt", "--help"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0 for --help, got %d (stderr=%q)", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "--diff") {
		t.Fatalf("expected help to mention --diff, got %q", out)
	}
}

func TestFmt_UnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"fmt", "--bad-flag"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit 2, got %d", code)
	}
}

func TestFmt_Diff(t *testing.T) {
	paper := writeFmtFixture(t)

	var stdout, stderr bytes.Buffer
	code := run([]string{"fmt", "--diff", paper}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", code, stderr.String())
	}

	diff := stdout.String()
	if !strings.Contains(diff, "---") {
		t.Fatalf("expected unified diff header, got %q", diff)
	}
	if !strings.Contains(diff, "+++") {
		t.Fatalf("expected unified diff header, got %q", diff)
	}
	// The diff should show the trailing-space removal.
	if !strings.Contains(diff, "-hi  ") {
		t.Fatalf("expected removed trailing-space line in diff, got %q", diff)
	}
	if !strings.Contains(diff, "+hi") {
		t.Fatalf("expected clean line in diff, got %q", diff)
	}

	// File should NOT be modified (--diff is read-only).
	content, _ := os.ReadFile(paper)
	if !strings.Contains(string(content), "hi  ") {
		t.Fatalf("--diff should not modify file")
	}
}

func TestFmt_DiffNoChanges(t *testing.T) {
	paper := writeFmtFixtureClean(t)

	var stdout, stderr bytes.Buffer
	code := run([]string{"fmt", "--diff", paper}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if !strings.Contains(stderr.String(), "no changes") {
		t.Fatalf("expected no-changes message, got %q", stderr.String())
	}
}

func TestFmt_Check_ChangesNeeded(t *testing.T) {
	paper := writeFmtFixture(t)

	var stdout, stderr bytes.Buffer
	code := run([]string{"fmt", "--check", paper}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1 when changes needed, got %d", code)
	}
}

func TestFmt_Check_NoChanges(t *testing.T) {
	paper := writeFmtFixtureClean(t)

	var stdout, stderr bytes.Buffer
	code := run([]string{"fmt", "--check", paper}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0 when no changes needed, got %d", code)
	}
}

func TestFmt_WriteAllowDirty(t *testing.T) {
	paper := writeFmtFixture(t)

	var stdout, stderr bytes.Buffer
	// File is in a temp dir (not a git repo), so dirty check would error.
	// --allow-dirty skips the check.
	code := run([]string{"fmt", "--allow-dirty", "--no-verify", paper}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", code, stderr.String())
	}

	// File should be modified: trailing whitespace removed.
	content, _ := os.ReadFile(paper)
	if strings.Contains(string(content), "hi  ") {
		t.Fatalf("expected trailing whitespace to be removed")
	}
	if !strings.Contains(string(content), "hi\n") {
		t.Fatalf("expected clean 'hi' line, got %q", string(content))
	}
}

func TestFmt_WriteNoChanges(t *testing.T) {
	paper := writeFmtFixtureClean(t)

	var stdout, stderr bytes.Buffer
	code := run([]string{"fmt", "--allow-dirty", paper}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if !strings.Contains(stderr.String(), "no changes") {
		t.Fatalf("expected no-changes message, got %q", stderr.String())
	}
}

func TestFmt_DirtyTreeRefused(t *testing.T) {
	// Set up a git repo with a dirty file.
	dir := t.TempDir()
	paper := filepath.Join(dir, "paper.tex")
	body := "\\documentclass{amsart}\n\\begin{document}\nhi  \n\\end{document}\n"
	if err := os.WriteFile(paper, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	// Initialize a git repo, add and commit the file, then modify it.
	mustGit(t, dir, "init")
	mustGit(t, dir, "config", "user.email", "test@test.com")
	mustGit(t, dir, "config", "user.name", "Test")
	mustGit(t, dir, "add", "paper.tex")
	mustGit(t, dir, "commit", "-m", "init")

	// Modify the file so git status shows it as dirty.
	if err := os.WriteFile(paper, []byte(body+"% extra\n"), 0o644); err != nil {
		t.Fatalf("re-write fixture: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"fmt", paper}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1 for dirty tree, got %d (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "uncommitted changes") {
		t.Fatalf("expected dirty-tree refusal message, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "--allow-dirty") {
		t.Fatalf("expected hint about --allow-dirty, got %q", stderr.String())
	}
}

func TestFmt_CleanGitTreeWritesOK(t *testing.T) {
	// Set up a git repo with a committed (clean) file that needs formatting.
	dir := t.TempDir()
	paper := filepath.Join(dir, "paper.tex")
	body := "\\documentclass{amsart}\n\\begin{document}\nhi  \n\\end{document}\n"
	if err := os.WriteFile(paper, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	mustGit(t, dir, "init")
	mustGit(t, dir, "config", "user.email", "test@test.com")
	mustGit(t, dir, "config", "user.name", "Test")
	mustGit(t, dir, "add", "paper.tex")
	mustGit(t, dir, "commit", "-m", "init")

	var stdout, stderr bytes.Buffer
	code := run([]string{"fmt", "--no-verify", paper}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", code, stderr.String())
	}
	// File should be modified.
	content, _ := os.ReadFile(paper)
	if strings.Contains(string(content), "hi  ") {
		t.Fatalf("expected trailing whitespace to be removed")
	}
}

func TestFmt_RuleFilter(t *testing.T) {
	dir := t.TempDir()
	paper := filepath.Join(dir, "paper.tex")
	// Input with both trailing whitespace AND tabs.
	body := "\\documentclass{amsart}\n\\begin{document}\n\thi  \n\\end{document}\n"
	if err := os.WriteFile(paper, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	var stdout, stderr bytes.Buffer
	// Only run space.trailing — tabs should survive.
	code := run([]string{"fmt", "--diff", "--rule=space.trailing", paper}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", code, stderr.String())
	}

	diff := stdout.String()
	// Trailing whitespace should be in the diff.
	if !strings.Contains(diff, "-\thi  ") {
		t.Fatalf("expected trailing-space removal in diff, got %q", diff)
	}
	// But the tab should remain (not replaced with spaces).
	if !strings.Contains(diff, "+\thi") {
		t.Fatalf("expected tab to remain when only space.trailing is selected, got %q", diff)
	}
}

func TestFmt_WriteSummary(t *testing.T) {
	paper := writeFmtFixture(t)

	var stdout, stderr bytes.Buffer
	code := run([]string{"fmt", "--allow-dirty", "--no-verify", paper}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "wrote") {
		t.Fatalf("expected write summary on stderr, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "rewrite") {
		t.Fatalf("expected rewrite count in summary, got %q", stderr.String())
	}
}

func TestFmt_NonGitDirWarnAndProceed(t *testing.T) {
	// In a non-git temp dir, without --allow-dirty, should warn but still write.
	paper := writeFmtFixture(t)

	var stdout, stderr bytes.Buffer
	code := run([]string{"fmt", "--no-verify", paper}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0 (non-git dir should warn, not refuse), got %d (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "cannot check git status") {
		t.Fatalf("expected git-status warning, got %q", stderr.String())
	}
	// File should be modified.
	content, _ := os.ReadFile(paper)
	if strings.Contains(string(content), "hi  ") {
		t.Fatalf("expected trailing whitespace to be removed")
	}
}

// --- --fail-on-change tests ---

func TestFmt_FailOnChange_NoChanges(t *testing.T) {
	paper := writeFmtFixtureClean(t)

	var stdout, stderr bytes.Buffer
	code := run([]string{"fmt", "--fail-on-change", "--allow-dirty", "--no-verify", "--no-report", paper}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0 when no changes, got %d (stderr=%q)", code, stderr.String())
	}
}

func TestFmt_FailOnChange_WithChanges(t *testing.T) {
	paper := writeFmtFixture(t)

	var stdout, stderr bytes.Buffer
	code := run([]string{"fmt", "--fail-on-change", "--allow-dirty", "--no-verify", "--no-report", paper}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1 when changes applied, got %d (stderr=%q)", code, stderr.String())
	}
	// File should still be written (formatted in place).
	content, _ := os.ReadFile(paper)
	if strings.Contains(string(content), "hi  ") {
		t.Fatalf("expected trailing whitespace to be removed even with --fail-on-change")
	}
	if !strings.Contains(string(content), "hi\n") {
		t.Fatalf("expected clean 'hi' line, got %q", string(content))
	}
}

func TestFmt_FailOnChange_MultiFile(t *testing.T) {
	a := writeFmtFixture(t)
	b := writeFmtFixtureClean(t)

	var stdout, stderr bytes.Buffer
	code := run([]string{"fmt", "--fail-on-change", "--allow-dirty", "--no-verify", "--no-report", a, b}, &stdout, &stderr)
	// File a has changes -> exit 1 (worst code wins).
	if code != 1 {
		t.Fatalf("expected exit 1 when any file changed, got %d (stderr=%q)", code, stderr.String())
	}
	// File a should be formatted.
	contentA, _ := os.ReadFile(a)
	if strings.Contains(string(contentA), "hi  ") {
		t.Fatalf("expected file a to be formatted")
	}
}

func TestFmt_FailOnChange_MutualExclusion(t *testing.T) {
	paper := writeFmtFixture(t)
	cases := []struct {
		name string
		args []string
	}{
		{"fail-on-change+check", []string{"fmt", "--fail-on-change", "--check", paper}},
		{"fail-on-change+diff", []string{"fmt", "--fail-on-change", "--diff", paper}},
		{"fail-on-change+print", []string{"fmt", "--fail-on-change", "--print", paper}},
		{"fail-on-change+stdin", []string{"fmt", "--fail-on-change", "--stdin"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(c.args, &stdout, &stderr)
			if code != 2 {
				t.Fatalf("expected exit 2 for %v, got %d (stderr=%q)", c.args, code, stderr.String())
			}
			if !strings.Contains(stderr.String(), "mutually exclusive") {
				t.Fatalf("expected mutually-exclusive message, got %q", stderr.String())
			}
		})
	}
}

// --- --stdin tests ---

// withStdinReader replaces stdinReader for the duration of a test.
func withStdinReader(t *testing.T, data []byte) {
	t.Helper()
	saved := stdinReader
	stdinReader = bytes.NewReader(data)
	t.Cleanup(func() { stdinReader = saved })
}

func TestFmt_Stdin_HappyPath(t *testing.T) {
	// Input with trailing whitespace: --stdin should strip it and write to stdout.
	input := "\\documentclass{amsart}\n\\begin{document}\nhi  \n\\end{document}\n"
	withStdinReader(t, []byte(input))

	var stdout, stderr bytes.Buffer
	code := run([]string{"fmt", "--stdin"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", code, stderr.String())
	}
	// Trailing whitespace should be stripped.
	if strings.Contains(stdout.String(), "hi  \n") {
		t.Fatalf("expected trailing whitespace stripped, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "hi\n") {
		t.Fatalf("expected clean 'hi' line, got %q", stdout.String())
	}
}

func TestFmt_Stdin_NoChanges(t *testing.T) {
	// Already clean input: --stdin should pass through unchanged.
	input := "\\documentclass{amsart}\n\\begin{document}\nhi\n\\end{document}\n"
	withStdinReader(t, []byte(input))

	var stdout, stderr bytes.Buffer
	code := run([]string{"fmt", "--stdin"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", code, stderr.String())
	}
	if stdout.String() != input {
		t.Fatalf("expected passthrough for clean input, got %q", stdout.String())
	}
}

func TestFmt_Stdin_MutualExclusion(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"stdin+check", []string{"fmt", "--stdin", "--check"}},
		{"stdin+diff", []string{"fmt", "--stdin", "--diff"}},
		{"stdin+print", []string{"fmt", "--stdin", "--print"}},
		{"stdin+file", []string{"fmt", "--stdin", "/tmp/paper.tex"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			withStdinReader(t, []byte("x"))
			var stdout, stderr bytes.Buffer
			code := run(c.args, &stdout, &stderr)
			if code != 2 {
				t.Fatalf("expected exit 2 for %v, got %d (stderr=%q)", c.args, code, stderr.String())
			}
			if !strings.Contains(stderr.String(), "mutually exclusive") && !strings.Contains(stderr.String(), "does not accept") {
				t.Fatalf("expected exclusion error for %v, got %q", c.args, stderr.String())
			}
		})
	}
}

// --- --summary tests ---

func TestFmt_Summary_SingleFile(t *testing.T) {
	paper := writeFmtFixture(t)

	var stdout, stderr bytes.Buffer
	code := run([]string{"fmt", "--summary", paper}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", code, stderr.String())
	}
	// Stdout should be empty (no file content output).
	if stdout.Len() > 0 {
		t.Fatalf("--summary should not write to stdout, got %q", stdout.String())
	}
	// Stderr should contain the summary line.
	out := stderr.String()
	if !strings.Contains(out, "rewrites across") {
		t.Fatalf("expected summary line on stderr, got %q", out)
	}
	// Should report at least 1 rewrite across 1 file.
	if !strings.Contains(out, "1 files") && !strings.Contains(out, "1 rewrites") {
		// More flexible: just check the numbers are non-zero.
		if strings.Contains(out, "0 rewrites across 0 files") {
			t.Fatalf("expected non-zero rewrites, got %q", out)
		}
	}
	// File should NOT be modified (scan-only).
	content, _ := os.ReadFile(paper)
	if !strings.Contains(string(content), "hi  ") {
		t.Fatalf("--summary should not modify the file")
	}
}

func TestFmt_Summary_NoChanges(t *testing.T) {
	paper := writeFmtFixtureClean(t)

	var stdout, stderr bytes.Buffer
	code := run([]string{"fmt", "--summary", paper}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", code, stderr.String())
	}
	out := stderr.String()
	if !strings.Contains(out, "0 rewrites across 0 files") {
		t.Fatalf("expected 0 rewrites for clean file, got %q", out)
	}
}

func TestFmt_Summary_MultiFile(t *testing.T) {
	a := writeFmtFixture(t)      // needs formatting
	b := writeFmtFixture(t)      // needs formatting
	c := writeFmtFixtureClean(t) // clean

	var stdout, stderr bytes.Buffer
	code := run([]string{"fmt", "--summary", a, b, c}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", code, stderr.String())
	}
	out := stderr.String()
	if !strings.Contains(out, "rewrites across") {
		t.Fatalf("expected summary line, got %q", out)
	}
	// Should report rewrites across 2 files (a and b have changes, c doesn't).
	if !strings.Contains(out, "2 files") {
		t.Fatalf("expected 2 files with rewrites, got %q", out)
	}
	// Files should NOT be modified.
	contentA, _ := os.ReadFile(a)
	if !strings.Contains(string(contentA), "hi  ") {
		t.Fatalf("--summary should not modify files")
	}
}

func TestFmt_Summary_MutualExclusion(t *testing.T) {
	paper := writeFmtFixture(t)
	cases := []struct {
		name string
		args []string
	}{
		{"summary+diff", []string{"fmt", "--summary", "--diff", paper}},
		{"summary+print", []string{"fmt", "--summary", "--print", paper}},
		{"summary+check", []string{"fmt", "--summary", "--check", paper}},
		{"summary+fail-on-change", []string{"fmt", "--summary", "--fail-on-change", paper}},
		{"summary+stdin", []string{"fmt", "--summary", "--stdin"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(c.args, &stdout, &stderr)
			if code != 2 {
				t.Fatalf("expected exit 2 for %v, got %d (stderr=%q)", c.args, code, stderr.String())
			}
			if !strings.Contains(stderr.String(), "mutually exclusive") {
				t.Fatalf("expected mutually-exclusive message, got %q", stderr.String())
			}
		})
	}
}

// --- --lines tests ---

// writeFmtFixtureMultiLine writes a .tex file with trailing whitespace on
// multiple lines, useful for testing --lines range formatting.
func writeFmtFixtureMultiLine(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	paper := filepath.Join(dir, "paper.tex")
	// Lines 1-5: trailing whitespace on lines 3 and 4.
	body := "\\documentclass{amsart}\n\\begin{document}\nfoo  \nbar  \n\\end{document}\n"
	if err := os.WriteFile(paper, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return paper
}

func TestFmt_Lines_InRangeOnly(t *testing.T) {
	paper := writeFmtFixtureMultiLine(t)

	var stdout, stderr bytes.Buffer
	// Format only line 3 (which has trailing whitespace "foo  ").
	code := run([]string{"fmt", "--lines=3:3", "--allow-dirty", "--no-verify", "--no-report", paper}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", code, stderr.String())
	}
	content, _ := os.ReadFile(paper)
	// Line 3 should be trimmed to "foo\n".
	if !strings.Contains(string(content), "foo\n") {
		t.Fatalf("expected line 3 trimmed, got %q", string(content))
	}
	// Line 4 should keep trailing whitespace ("bar  \n") since it's out of range.
	if !strings.Contains(string(content), "bar  \n") {
		t.Fatalf("expected line 4 preserved, got %q", string(content))
	}
}

func TestFmt_Lines_PrintMode(t *testing.T) {
	paper := writeFmtFixtureMultiLine(t)

	var stdout, stderr bytes.Buffer
	// --lines with --print: should output the range-clipped result.
	code := run([]string{"fmt", "--lines=3:3", "--print", paper}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "foo\n") {
		t.Fatalf("expected trimmed foo in output, got %q", out)
	}
	if !strings.Contains(out, "bar  \n") {
		t.Fatalf("expected untrimmed bar in output (out of range), got %q", out)
	}
}

func TestFmt_Lines_DiffMode(t *testing.T) {
	paper := writeFmtFixtureMultiLine(t)

	var stdout, stderr bytes.Buffer
	code := run([]string{"fmt", "--lines=3:3", "--diff", paper}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", code, stderr.String())
	}
	diff := stdout.String()
	// Should show the change on line 3.
	if !strings.Contains(diff, "-foo  ") {
		t.Fatalf("expected diff showing trimmed line 3, got %q", diff)
	}
	// Should NOT show a change on line 4 (out of range).
	if strings.Contains(diff, "-bar  ") {
		t.Fatalf("diff should not show line 4 change (out of range), got %q", diff)
	}
}

func TestFmt_Lines_StdinMode(t *testing.T) {
	input := "\\documentclass{amsart}\n\\begin{document}\nfoo  \nbar  \n\\end{document}\n"
	withStdinReader(t, []byte(input))

	var stdout, stderr bytes.Buffer
	code := run([]string{"fmt", "--stdin", "--lines=3:3"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "foo\n") {
		t.Fatalf("expected trimmed foo, got %q", out)
	}
	if !strings.Contains(out, "bar  \n") {
		t.Fatalf("expected untrimmed bar (out of range), got %q", out)
	}
}

func TestFmt_Lines_MutualExclusion(t *testing.T) {
	paper := writeFmtFixtureMultiLine(t)
	cases := []struct {
		name string
		args []string
	}{
		{"lines+check", []string{"fmt", "--lines=1:5", "--check", paper}},
		{"lines+summary", []string{"fmt", "--lines=1:5", "--summary", paper}},
		{"lines+fail-on-change", []string{"fmt", "--lines=1:5", "--fail-on-change", paper}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(c.args, &stdout, &stderr)
			if code != 2 {
				t.Fatalf("expected exit 2 for %v, got %d (stderr=%q)", c.args, code, stderr.String())
			}
			if !strings.Contains(stderr.String(), "mutually exclusive") {
				t.Fatalf("expected mutually-exclusive message, got %q", stderr.String())
			}
		})
	}
}

func TestFmt_Lines_MultiFileRejected(t *testing.T) {
	a := writeFmtFixtureMultiLine(t)
	b := writeFmtFixtureMultiLine(t)

	var stdout, stderr bytes.Buffer
	code := run([]string{"fmt", "--lines=1:5", a, b}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit 2, got %d (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "accepts only one file") {
		t.Fatalf("expected multi-file rejection message, got %q", stderr.String())
	}
}

func TestFmt_Lines_InvalidFormat(t *testing.T) {
	paper := writeFmtFixtureMultiLine(t)

	cases := []struct {
		name  string
		lines string
	}{
		{"no colon", "1-5"},
		{"end < start", "10:5"},
		{"non-numeric", "abc:def"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run([]string{"fmt", "--lines=" + c.lines, paper}, &stdout, &stderr)
			if code != 2 {
				t.Fatalf("expected exit 2, got %d (stderr=%q)", code, stderr.String())
			}
		})
	}
}

func TestFmt_Lines_SkipsReported(t *testing.T) {
	paper := writeFmtFixtureMultiLine(t)

	var stdout, stderr bytes.Buffer
	code := run([]string{"fmt", "--lines=3:3", "--diff", paper}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", code, stderr.String())
	}
	// Should report that line-count-changing rules were skipped.
	if !strings.Contains(stderr.String(), "skipped under --lines") {
		t.Fatalf("expected skip report on stderr, got %q", stderr.String())
	}
}

// prose.tilde-refs is opt-in: skipped by default unless tilde_refs is set
// in config or --rule=prose.tilde-refs is explicitly passed.
func TestMergeSkipRulesWith_DefaultSkipsTilde(t *testing.T) {
	got := mergeSkipRulesWith(nil, nil, nil, nil)
	if !sliceContains(got, "prose.tilde-refs") {
		t.Fatalf("expected prose.tilde-refs in default skip, got %v", got)
	}
}

func TestMergeSkipRulesWith_ConfigOptInRunsTilde(t *testing.T) {
	got := mergeSkipRulesWith(nil, nil, []string{"cite", "ref"}, nil)
	if sliceContains(got, "prose.tilde-refs") {
		t.Fatalf("config opt-in should remove default skip; got %v", got)
	}
}

func TestMergeSkipRulesWith_ExplicitRuleRunsTilde(t *testing.T) {
	got := mergeSkipRulesWith(nil, nil, nil, []string{"prose.tilde-refs"})
	if sliceContains(got, "prose.tilde-refs") {
		t.Fatalf("explicit --rule should remove default skip; got %v", got)
	}
}

func TestMergeSkipRulesWith_PreservesUserSkips(t *testing.T) {
	got := mergeSkipRulesWith([]string{"space.wrap"}, []string{"math.align-columns"}, nil, nil)
	if !sliceContains(got, "space.wrap") || !sliceContains(got, "math.align-columns") {
		t.Fatalf("user-supplied skips dropped: %v", got)
	}
	if !sliceContains(got, "prose.tilde-refs") {
		t.Fatalf("default tilde skip should still be added: %v", got)
	}
}

func sliceContains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// mustGit runs a git command in dir, failing the test on error.
func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
