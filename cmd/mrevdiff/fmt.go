package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/jessevdk/go-flags"
	"github.com/pmezard/go-difflib/difflib"

	"github.com/lenis2000/mrevdiff/pkg/format"
	"github.com/lenis2000/mrevdiff/pkg/ui"
)

// fmtOpts holds flags for the "mrevdiff fmt" subcommand.
//
// Defaults are aggressive: Tier-2 PDF-fix rules on, paranoid pixel-level
// verification on, and a fmt-report.md emitted next to the paper. Persistent
// behaviour is configured in ~/.config/mrevdiff/config.toml or .mrevdiff.toml
// under the [fmt] sub-table; the CLI exposes only one-off escape hatches
// (--no-verify, --no-report) and per-invocation modes (--diff, --print,
// --check, --rule).
type fmtOpts struct {
	Diff         bool     `long:"diff" description:"show unified diff to stdout, do not write"`
	Print        bool     `long:"print" short:"p" description:"print formatted source to stdout, do not write"`
	Check        bool     `long:"check" description:"exit 1 if changes needed (CI / pre-commit)"`
	Stdin        bool     `long:"stdin" description:"read source from stdin, write formatted to stdout"`
	FailOnChange bool     `long:"fail-on-change" description:"format in place AND exit 1 when changed (CI/pre-commit)"`
	Summary      bool     `long:"summary" description:"scan only; print N rewrites across M files to stderr"`
	Lines        string   `long:"lines" description:"format only lines START:END (1-based, inclusive)"`
	Rule         []string `long:"rule" description:"restrict to these rule IDs (repeatable)"`
	SkipRule     []string `long:"skip-rule" description:"disable these rule IDs even when otherwise enabled (repeatable)"`
	ListRules    bool     `long:"list-rules" description:"print all registered rule IDs (tier, id, doc) and exit"`
	AllowDirty   bool     `long:"allow-dirty" description:"skip dirty-tree check before writing"`
	NoVerify     bool     `long:"no-verify" description:"skip PDF verification entirely (one-off escape hatch)"`
	NoReport     bool     `long:"no-report" description:"do not write paper.tex.fmt-report.md (one-off)"`
	CleanTempdir bool     `long:"clean-tempdir" description:"remove all mr-fmt-* verification tempdirs"`
	Config       string   `long:"config" description:"path to config file"`
	NoConfig     bool     `long:"noconfig" description:"ignore config files; use built-in defaults"`
}

// runFmt implements "mrevdiff fmt [FLAGS] paper.tex".
func runFmt(args []string, stdout, stderr io.Writer) int {
	var o fmtOpts
	p := flags.NewParser(&o, flags.HelpFlag|flags.PassDoubleDash)
	p.Name = "mrevdiff fmt"
	p.Usage = "[OPTIONS] paper.tex"

	rest, err := p.ParseArgs(args)
	if err != nil {
		var flagsErr *flags.Error
		if errors.As(err, &flagsErr) && flagsErr.Type == flags.ErrHelp {
			_, _ = fmt.Fprintln(stdout, err.Error())
			return 0
		}
		_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: %v\n", err)
		return 2
	}

	// --print is mutually exclusive with --diff and --check.
	if (o.Print && o.Diff) || (o.Print && o.Check) || (o.Diff && o.Check) {
		_, _ = fmt.Fprintln(stderr, "mrevdiff fmt: --diff, --print, and --check are mutually exclusive")
		return 2
	}

	// --lines is mutually exclusive with --check, --summary, --fail-on-change, multi-file.
	if o.Lines != "" && (o.Check || o.Summary || o.FailOnChange) {
		_, _ = fmt.Fprintln(stderr, "mrevdiff fmt: --lines is mutually exclusive with --check, --summary, and --fail-on-change")
		return 2
	}

	// --summary is mutually exclusive with --diff, --print, --check, --fail-on-change, --stdin, --lines.
	if o.Summary && (o.Diff || o.Print || o.Check || o.FailOnChange || o.Stdin || o.Lines != "") {
		_, _ = fmt.Fprintln(stderr, "mrevdiff fmt: --summary is mutually exclusive with --diff, --print, --check, --fail-on-change, --stdin, and --lines")
		return 2
	}

	// --fail-on-change is mutually exclusive with --check, --diff, --print, --stdin, --summary.
	if o.FailOnChange && (o.Check || o.Diff || o.Print || o.Stdin || o.Summary) {
		_, _ = fmt.Fprintln(stderr, "mrevdiff fmt: --fail-on-change is mutually exclusive with --check, --diff, --print, --stdin, and --summary")
		return 2
	}

	// --stdin is mutually exclusive with file args, --check, --diff, --print, --fail-on-change, --summary.
	if o.Stdin {
		if o.Check || o.Diff || o.Print {
			_, _ = fmt.Fprintln(stderr, "mrevdiff fmt: --stdin is mutually exclusive with --check, --diff, and --print")
			return 2
		}
		if len(rest) > 0 {
			_, _ = fmt.Fprintln(stderr, "mrevdiff fmt: --stdin does not accept file arguments")
			return 2
		}
	}

	// --list-rules: print rule IDs and exit. Runs before any other validation
	// so it works even when no file argument is given.
	if o.ListRules {
		printRulesList(stdout)
		return 0
	}

	// --clean-tempdir: remove all verification tempdirs and exit.
	if o.CleanTempdir {
		if err := format.CleanTempDirs(); err != nil {
			_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: clean tempdirs: %v\n", err)
			return 1
		}
		_, _ = fmt.Fprintln(stderr, "mrevdiff fmt: cleaned verification tempdirs")
		return 0
	}

	// --stdin: read from stdin, format, write to stdout.
	if o.Stdin {
		return runFmtStdin(&o, stdout, stderr)
	}

	if len(rest) == 0 {
		_, _ = fmt.Fprintln(stderr, "mrevdiff fmt: missing paper argument")
		_, _ = fmt.Fprintln(stderr, "usage: mrevdiff fmt [OPTIONS] paper.tex [paper.tex...]")
		return 2
	}

	// --print / --diff / --check have no sensible interpretation across many
	// files: refuse so output isn't accidentally interleaved.
	if (o.Print || o.Diff || o.Check) && len(rest) > 1 {
		_, _ = fmt.Fprintln(stderr, "mrevdiff fmt: --print, --diff, and --check accept only one file")
		return 2
	}

	// --lines accepts only one file.
	if o.Lines != "" && len(rest) > 1 {
		_, _ = fmt.Fprintln(stderr, "mrevdiff fmt: --lines accepts only one file")
		return 2
	}

	// Validate --rule IDs once before opening any file.
	if err := format.ValidateRuleIDs(o.Rule); err != nil {
		_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: %v\n", err)
		return 2
	}
	if err := format.ValidateRuleIDs(o.SkipRule); err != nil {
		_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: --skip-rule: %v\n", err)
		return 2
	}
	// Catch the contradictory case `--rule X --skip-rule X`: the skip
	// list silently wins, leaving an empty rule set that does no work.
	if contradicting := intersect(o.Rule, o.SkipRule); len(contradicting) > 0 {
		_, _ = fmt.Fprintf(stderr,
			"mrevdiff fmt: --rule and --skip-rule both name %q — nothing will run\n",
			strings.Join(contradicting, ", "))
		return 2
	}
	// Note: config skip_rules is validated lazily after LoadConfig below.

	// Parse --lines early so we fail fast on bad input.
	var lineRange *[2]int
	if o.Lines != "" {
		lr, lrErr := format.ParseLineRange(o.Lines)
		if lrErr != nil {
			_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: %v\n", lrErr)
			return 2
		}
		lineRange = &lr
	}

	// Load config once; defaults are shared across all files.
	cfg, cfgErr := ui.LoadConfig(o.Config, o.NoConfig)
	if cfgErr != nil {
		_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: %v\n", cfgErr)
		return 1
	}
	if err := format.ValidateRuleIDs(cfg.Fmt.SkipRules); err != nil {
		_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: config skip_rules: %v\n", err)
		return 2
	}

	// Resolve config-driven settings. CLI escape hatches override (only
	// --no-verify and --no-report); everything else comes from [fmt] in
	// the config file or the built-in defaults.
	resolved := resolveFormatOpts(cfg.Fmt)
	noVerify := resolveBool(o.NoVerify, cfg.Fmt.NoVerify, false)
	wantReport := !resolveBool(o.NoReport, cfg.Fmt.NoReport, false)
	verifyMode := cfg.Fmt.VerifyPDF
	if verifyMode == "" {
		verifyMode = "visual"
	}

	// --summary: scan-only mode; accumulate rewrites/diags across files.
	if o.Summary {
		return runFmtSummary(rest, &o, resolved.pdfFix, resolved.indent, resolved.wrap, resolved.tilde, resolved.mathAlign, resolved.mathWrap, cfg, stderr)
	}

	// Plumb a SIGINT-aware context through verifier and build subprocesses
	// so a long latexmk/pdftotext can be interrupted cleanly with Ctrl-C.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Loop the per-file work; aggregate exit codes.
	worst := 0
	for i, paperPath := range rest {
		if len(rest) > 1 {
			_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: [%d/%d] %s\n", i+1, len(rest), filepath.Base(paperPath))
		}
		code := runFmtOne(ctx, paperPath, &o, cfg, resolved.pdfFix, noVerify, wantReport, verifyMode, resolved.indent, resolved.wrap, resolved.tilde, resolved.mathAlign, resolved.mathWrap, lineRange, stdout, stderr)
		if code > worst {
			worst = code
		}
	}
	return worst
}

// runFmtOne runs the format pipeline for a single .tex file. Returns 0 on
// success, 1 on per-file errors, 2 on usage errors. Caller pre-validates
// shared inputs (--rule, config) and resolves the aggressive defaults.
//
// ctx is the SIGINT-aware context propagated to verifier subprocesses so
// the user can interrupt a long latexmk / pdftotext / diff-pdf with C-c.
func runFmtOne(
	ctx context.Context,
	paperPath string,
	o *fmtOpts,
	cfg *ui.Config,
	pdfFix, noVerify, wantReport bool,
	verifyMode string,
	indentOpts format.IndentOptions,
	wrapOpts format.WrapOptions,
	tildeOpts format.TildeOptions,
	mathAlignOpts format.MathAlignOptions,
	mathWrapOpts format.MathWrapOptions,
	lineRange *[2]int,
	stdout, stderr io.Writer,
) int {
	fileInfo, statErr := os.Stat(paperPath)
	if statErr != nil {
		_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: cannot read %q: %v\n", paperPath, statErr)
		return 1
	}

	src, readErr := os.ReadFile(paperPath)
	if readErr != nil {
		_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: read %q: %v\n", paperPath, readErr)
		return 1
	}

	// Build pipeline options.
	opts := format.Options{
		PDFFix:       pdfFix,
		Rules:        o.Rule,
		SkipRules:    mergeSkipRulesWith(cfg.Fmt.SkipRules, o.SkipRule, cfg.Fmt.TildeRefs, o.Rule),
		Diag:         wantReport, // enable diagnostics when a report will be written
		VerbatimEnvs: cfg.Fmt.VerbatimEnvs,
		Indent:       indentOpts,
		Wrap:         wrapOpts,
		Tilde:        tildeOpts,
		MathAlign:    mathAlignOpts,
		MathWrap:     mathWrapOpts,
		LineRange:    lineRange,
	}

	// Report rules skipped under --lines.
	if skipped := format.SkippedLineRangeRules(opts); len(skipped) > 0 {
		for _, id := range skipped {
			_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: note: %s skipped under --lines\n", id)
		}
	}

	result := format.Apply(src, opts)

	// When --lines is set, clip the result to the requested range:
	// in-range lines get the formatted version, out-of-range lines keep
	// the original text.
	if lineRange != nil {
		clipped, clipErr := format.ClipToRange(src, result.Src, *lineRange)
		if clipErr != nil {
			_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: --lines: %v\n", clipErr)
			return 1
		}
		result.Src = clipped
	}

	// Write report early — both --check and no-changes paths benefit.
	writeReportIfNeeded := func(verifyResult *format.VerifyResult) {
		if !wantReport {
			return
		}
		reportPath := format.ReportPath(paperPath)
		if len(result.Diags) == 0 && len(result.Hits) == 0 {
			// Clean up stale report so the UI doesn't show outdated diagnostics.
			if rmErr := os.Remove(reportPath); rmErr != nil && !os.IsNotExist(rmErr) {
				_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: remove stale report: %v\n", rmErr)
			}
			return
		}
		rpt := format.BuildReport(filepath.Base(paperPath), opts, result, verifyResult)
		if rptErr := format.WriteReport(reportPath, rpt); rptErr != nil {
			_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: write report: %v\n", rptErr)
			return
		}
		_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: wrote %s\n", filepath.Base(reportPath))
	}

	// No changes?
	if string(result.Src) == string(src) {
		writeReportIfNeeded(nil)
		if o.Check {
			return 0
		}
		if o.Print {
			if _, werr := stdout.Write(result.Src); werr != nil {
				_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: write stdout: %v\n", werr)
				return 1
			}
			return 0
		}
		if !wantReport || len(result.Diags) == 0 {
			_, _ = fmt.Fprintln(stderr, "mrevdiff fmt: no changes")
		}
		return 0
	}

	// --check: exit 1 if changes are needed.
	if o.Check {
		writeReportIfNeeded(nil)
		return 1
	}

	// --diff: print unified diff to stdout, no write.
	if o.Diff {
		writeReportIfNeeded(nil)
		return printDiff(stdout, paperPath, src, result.Src)
	}

	// --print: write formatted source to stdout, no file write, no verify, no report.
	if o.Print {
		if _, werr := stdout.Write(result.Src); werr != nil {
			_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: write stdout: %v\n", werr)
			return 1
		}
		return 0
	}

	// Write mode: check dirty tree unless --allow-dirty.
	if !o.AllowDirty {
		dirty, dirtyErr := isGitDirty(paperPath)
		if dirtyErr != nil {
			// Not a git repo or git not available — proceed with a warning.
			_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: warning: cannot check git status: %v\n", dirtyErr)
		} else if dirty {
			_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: %s has uncommitted changes; refusing to overwrite\n", filepath.Base(paperPath))
			_, _ = fmt.Fprintln(stderr, "hint: commit or stash first, or pass --allow-dirty")
			return 1
		}
	}

	// Verify: build before/after PDFs and compare text layer.
	var verifyResult *format.VerifyResult
	if !noVerify {
		tree, treeErr := format.DiscoverTree(paperPath)
		if treeErr != nil {
			_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: discover build inputs: %v\n", treeErr)
			return 1
		}

		_, _ = fmt.Fprintln(stderr, "mrevdiff fmt: verifying PDF text layer...")
		vr, verifyErr := format.Verify(ctx, *tree, src, result.Src, result.Hits)
		if verifyErr != nil {
			_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: verification error: %v\n", verifyErr)
			_, _ = fmt.Fprintf(stderr, "hint: pass --no-verify to skip, or inspect %s\n", format.LastTempDir())
			return 1
		}
		if !vr.OK {
			_, _ = fmt.Fprintln(stderr, "mrevdiff fmt: verification FAILED — unexpected PDF text diffs:")
			format.FormatDiffs(stderr, vr.Unexpected)
			_, _ = fmt.Fprintf(stderr, "tempdir preserved at %s for inspection\n", format.LastTempDir())
			// Persist the verifier's unexpected-diff list to the fmt-report
			// so the user can inspect it after rollback (stderr scrolls away).
			writeReportIfNeeded(vr)
			_, _ = fmt.Fprintln(stderr, "hint: pass --no-verify to skip verification")
			return 1
		}
		for _, w := range vr.Warnings {
			_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: warning: %s\n", w)
		}
		_, _ = fmt.Fprintln(stderr, "mrevdiff fmt: verification ok (text layer)")
		verifyResult = vr

		// Paranoid mode: pixel-level diff-pdf comparison. Default; opt out
		// with --verify-pdf=text.
		if verifyMode == "visual" {
			if !format.ParanoidAvailable {
				_, _ = fmt.Fprintln(stderr, "mrevdiff fmt: paranoid verifier not available — rebuild with -tags=pdfverify")
				return 1
			}
			_, _ = fmt.Fprintln(stderr, "mrevdiff fmt: running paranoid pixel-level verification...")
			pr, prErr := format.VerifyParanoid(ctx, vr.BeforePDF, vr.AfterPDF)
			if prErr != nil {
				_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: paranoid verification error: %v\n", prErr)
				return 1
			}
			if !pr.OK {
				_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: paranoid verification FAILED — %s\n", pr.Message)
				if pr.DiffPDFPath != "" {
					_, _ = fmt.Fprintf(stderr, "diff PDF saved to %s\n", pr.DiffPDFPath)
				}
				_, _ = fmt.Fprintf(stderr, "tempdir preserved at %s for inspection\n", format.LastTempDir())
				return 1
			}
			_, _ = fmt.Fprintln(stderr, "mrevdiff fmt: paranoid verification ok (pixel-identical)")
		}
	}

	// Write the rewritten source, preserving original file permissions.
	if writeErr := os.WriteFile(paperPath, result.Src, fileInfo.Mode().Perm()); writeErr != nil {
		_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: write %q: %v\n", paperPath, writeErr)
		return 1
	}

	// Write report if --report is set (with verifier result).
	writeReportIfNeeded(verifyResult)

	// Summary.
	nHits := len(result.Hits)
	if nHits == 1 {
		_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: wrote %s (1 rewrite)\n", filepath.Base(paperPath))
	} else {
		_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: wrote %s (%d rewrites)\n", filepath.Base(paperPath), nHits)
	}

	// --fail-on-change: exit 1 when changes were applied (for CI/pre-commit).
	if o.FailOnChange && nHits > 0 {
		return 1
	}

	return 0
}

// resolvedFmtOpts holds the formatting options resolved from FmtConfig.
// This avoids duplicating the resolution logic between runFmt and runFmtStdin.
type resolvedFmtOpts struct {
	pdfFix    bool
	indent    format.IndentOptions
	wrap      format.WrapOptions
	tilde     format.TildeOptions
	mathAlign format.MathAlignOptions
	mathWrap  format.MathWrapOptions
}

// resolveFormatOpts computes the formatting option structs from the merged
// FmtConfig. Callers still resolve verify/report/check-specific settings
// themselves — this helper covers only the format.Options fields shared
// between file and stdin paths.
func resolveFormatOpts(fc ui.FmtConfig) resolvedFmtOpts {
	pdfFix := true
	if fc.NoPDFFix != nil {
		pdfFix = !*fc.NoPDFFix
	}

	indentEnabled := true
	if fc.Indent != nil {
		indentEnabled = *fc.Indent
	}
	indentChar := fc.IndentChar
	if indentChar == "" {
		indentChar = "tab"
	}
	indentSize := fc.IndentSize
	if indentSize <= 0 {
		if indentChar == "tab" {
			indentSize = 1
		} else {
			indentSize = 2
		}
	}

	wrapMode := fc.Wrap
	if wrapMode == "" {
		wrapMode = "sentence+column"
	}
	wrapCol := fc.WrapCol
	if wrapCol <= 0 {
		wrapCol = 80
	}

	mathAlignEnabled := true
	if fc.MathAlign != nil {
		mathAlignEnabled = *fc.MathAlign
	}

	mathWrapEnabled := false
	if fc.MathWrap != nil {
		mathWrapEnabled = *fc.MathWrap
	}

	return resolvedFmtOpts{
		pdfFix: pdfFix,
		indent: format.IndentOptions{
			Enabled: indentEnabled,
			UseTab:  indentChar == "tab",
			Size:    indentSize,
			Rules:   fc.IndentRules,
		},
		wrap: format.WrapOptions{
			Mode: wrapMode,
			Col:  wrapCol,
		},
		tilde: format.TildeOptions{
			Refs: fc.TildeRefs,
		},
		mathAlign: format.MathAlignOptions{
			Enabled: mathAlignEnabled,
			Envs:    fc.MathAlignEnvs,
			Skip:    fc.MathAlignSkip,
		},
		mathWrap: format.MathWrapOptions{
			Enabled: mathWrapEnabled,
			Col:     fc.MathWrapCol,
		},
	}
}

// stdinReader is the source for --stdin. It defaults to os.Stdin but tests
// can replace it with a bytes.Reader.
var stdinReader io.Reader = os.Stdin

// runFmtStdin implements the --stdin path: read all of stdin, run format.Apply,
// write formatted source to stdout. Implies --no-verify and --no-report; no
// dirty-tree check, no path argument.
func runFmtStdin(o *fmtOpts, stdout, stderr io.Writer) int {
	src, err := io.ReadAll(stdinReader)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: <stdin>: read: %v\n", err)
		return 1
	}

	// Load config (needed for indent/wrap defaults).
	cfg, cfgErr := ui.LoadConfig(o.Config, o.NoConfig)
	if cfgErr != nil {
		_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: <stdin>: %v\n", cfgErr)
		return 1
	}

	resolved := resolveFormatOpts(cfg.Fmt)

	// Validate --rule and --skip-rule IDs.
	if err := format.ValidateRuleIDs(o.Rule); err != nil {
		_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: <stdin>: %v\n", err)
		return 2
	}
	if err := format.ValidateRuleIDs(o.SkipRule); err != nil {
		_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: <stdin>: --skip-rule: %v\n", err)
		return 2
	}
	if err := format.ValidateRuleIDs(cfg.Fmt.SkipRules); err != nil {
		_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: <stdin>: config skip_rules: %v\n", err)
		return 2
	}

	// Parse --lines for stdin mode.
	var stdinLineRange *[2]int
	if o.Lines != "" {
		lr, lrErr := format.ParseLineRange(o.Lines)
		if lrErr != nil {
			_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: <stdin>: %v\n", lrErr)
			return 2
		}
		stdinLineRange = &lr
	}

	opts := format.Options{
		PDFFix:       resolved.pdfFix,
		Rules:        o.Rule,
		SkipRules:    mergeSkipRulesWith(cfg.Fmt.SkipRules, o.SkipRule, cfg.Fmt.TildeRefs, o.Rule),
		Diag:         false, // no report for stdin
		VerbatimEnvs: cfg.Fmt.VerbatimEnvs,
		Indent:       resolved.indent,
		Wrap:         resolved.wrap,
		Tilde:        resolved.tilde,
		MathAlign:    resolved.mathAlign,
		MathWrap:     resolved.mathWrap,
		LineRange:    stdinLineRange,
	}

	// Report rules skipped under --lines.
	if skipped := format.SkippedLineRangeRules(opts); len(skipped) > 0 {
		for _, id := range skipped {
			_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: <stdin>: note: %s skipped under --lines\n", id)
		}
	}

	result := format.Apply(src, opts)

	// Clip to range if --lines is active.
	if stdinLineRange != nil {
		clipped, clipErr := format.ClipToRange(src, result.Src, *stdinLineRange)
		if clipErr != nil {
			_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: <stdin>: --lines: %v\n", clipErr)
			return 1
		}
		result.Src = clipped
	}

	if _, werr := stdout.Write(result.Src); werr != nil {
		_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: <stdin>: write stdout: %v\n", werr)
		return 1
	}
	return 0
}

// runFmtSummary implements --summary: scan each file, accumulate hit/diag
// counts, and print a single line to stderr. Implies --no-verify, --no-report,
// no dirty-tree check, no file write. Exit code is always 0.
func runFmtSummary(
	paths []string,
	o *fmtOpts,
	pdfFix bool,
	indentOpts format.IndentOptions,
	wrapOpts format.WrapOptions,
	tildeOpts format.TildeOptions,
	mathAlignOpts format.MathAlignOptions,
	mathWrapOpts format.MathWrapOptions,
	cfg *ui.Config,
	stderr io.Writer,
) int {
	totalHits := 0
	totalDiags := 0
	filesWithHits := 0
	filesWithDiags := 0

	for _, paperPath := range paths {
		src, readErr := os.ReadFile(paperPath)
		if readErr != nil {
			_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: read %q: %v\n", paperPath, readErr)
			return 1
		}

		opts := format.Options{
			PDFFix:       pdfFix,
			Rules:        o.Rule,
			SkipRules:    mergeSkipRulesWith(cfg.Fmt.SkipRules, o.SkipRule, cfg.Fmt.TildeRefs, o.Rule),
			Diag:         true, // need diags for the count
			VerbatimEnvs: cfg.Fmt.VerbatimEnvs,
			Indent:       indentOpts,
			Wrap:         wrapOpts,
			Tilde:        tildeOpts,
			MathAlign:    mathAlignOpts,
			MathWrap:     mathWrapOpts,
		}

		result := format.Apply(src, opts)

		nHits := len(result.Hits)
		nDiags := len(result.Diags)
		totalHits += nHits
		totalDiags += nDiags
		if nHits > 0 {
			filesWithHits++
		}
		if nDiags > 0 {
			filesWithDiags++
		}
	}

	if totalDiags > 0 {
		_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: %d rewrites across %d files (%d with diagnostics)\n",
			totalHits, filesWithHits, filesWithDiags)
	} else {
		_, _ = fmt.Fprintf(stderr, "mrevdiff fmt: %d rewrites across %d files\n",
			totalHits, filesWithHits)
	}
	return 0
}

// printRulesList writes a table of every registered rule to w (tier, id, doc).
// Used by `mrevdiff fmt --list-rules`.
func printRulesList(w io.Writer) {
	rules := format.ListRules()
	maxID := len("RULE ID")
	maxTier := len("TIER")
	for _, r := range rules {
		if l := len(r.ID); l > maxID {
			maxID = l
		}
		if l := len(r.Tier.String()); l > maxTier {
			maxTier = l
		}
	}
	_, _ = fmt.Fprintf(w, "%-*s  %-*s  %s\n", maxTier, "TIER", maxID, "RULE ID", "DESCRIPTION")
	for _, r := range rules {
		_, _ = fmt.Fprintf(w, "%-*s  %-*s  %s\n", maxTier, r.Tier.String(), maxID, r.ID, r.Doc)
	}
}

// mergeSkipRulesWith is mergeSkipRules with the additional ability to skip
// the implicit `prose.tilde-refs` default when the user has opted in.
//   - tildeRefs: from config (non-empty means user configured custom refs)
//   - explicitRules: from --rule (when user explicitly requests tilde-refs)
func mergeSkipRulesWith(fromCfg, fromCLI []string, tildeRefs, explicitRules []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range fromCfg {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, id := range fromCLI {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	// Implicit default: tilde-refs is opt-in.
	tildeOptedIn := len(tildeRefs) > 0
	for _, id := range explicitRules {
		if id == "prose.tilde-refs" {
			tildeOptedIn = true
		}
	}
	if !tildeOptedIn && !seen["prose.tilde-refs"] {
		out = append(out, "prose.tilde-refs")
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// intersect returns the values appearing in both a and b, preserving the
// order from a and dropping duplicates. Used to spot --rule/--skip-rule
// contradictions before they swallow the whole pipeline silently.
func intersect(a, b []string) []string {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	bSet := make(map[string]struct{}, len(b))
	for _, v := range b {
		bSet[v] = struct{}{}
	}
	seen := map[string]struct{}{}
	var out []string
	for _, v := range a {
		if _, ok := bSet[v]; !ok {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// resolveBool returns the effective bool from (flag, config, default).
//
// Flag wins when true (go-flags can't distinguish "passed false" from "not
// passed"). Otherwise config wins when explicitly set. Otherwise the built-in
// default is used.
func resolveBool(flag bool, cfg *bool, def bool) bool {
	if flag {
		return true
	}
	if cfg != nil {
		return *cfg
	}
	return def
}

// printDiff writes a unified diff of before/after to w, returning exit code.
func printDiff(w io.Writer, path string, before, after []byte) int {
	diff := difflib.UnifiedDiff{
		A:        difflib.SplitLines(string(before)),
		B:        difflib.SplitLines(string(after)),
		FromFile: "a/" + filepath.Base(path),
		ToFile:   "b/" + filepath.Base(path),
		Context:  3,
	}
	text, err := difflib.GetUnifiedDiffString(diff)
	if err != nil {
		return 1
	}
	_, _ = fmt.Fprint(w, text)
	return 0
}

// isGitDirty reports whether path has uncommitted changes in git.
// Returns an error if git is not available or path is not in a git repo.
func isGitDirty(path string) (bool, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false, err
	}
	dir := filepath.Dir(absPath)
	base := filepath.Base(absPath)

	cmd := exec.Command("git", "status", "--porcelain", "--", base)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("git status: %w", err)
	}
	return len(strings.TrimSpace(string(out))) > 0, nil
}
