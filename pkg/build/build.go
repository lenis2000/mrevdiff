// Package build wraps the LaTeX build pipeline (latexmk by default) and
// surfaces a strict pass/fail signal: a build is considered failed not only
// when the tool exits non-zero, but also when the .log contains a TeX-style
// "!" error line or an undefined-reference / undefined-citation warning that
// has survived to the final pass. The latter conditions are what callers care
// about because the review workflow assumes refs and labels resolve.
package build

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Result holds the file paths produced (or expected) by a build.
type Result struct {
	PDFPath     string
	SyncTeXPath string
	AuxPath     string
	BBLPath     string
	LogPath     string
}

// Options controls a single Run invocation. The zero value is valid; Dir
// defaults to the directory of TexPath, BuildCmd defaults to the standard
// latexmk invocation, and Stdout/Stderr default to io.Discard.
type Options struct {
	// TexPath is the path to the .tex file to compile. Required.
	TexPath string
	// BuildCmd, if non-empty, is run via "sh -c" inside Dir. The tex
	// basename (no extension) is exposed as the env vars $MREVDIFF_BASENAME
	// and $MREVIEW_BASENAME (the latter for compatibility with build
	// scripts written for mreview) so custom commands can interpolate it.
	BuildCmd string
	// Dir is the working directory for the command. Defaults to
	// filepath.Dir(TexPath).
	Dir string
	// Stdout and Stderr receive the raw command output. They may be nil.
	Stdout io.Writer
	Stderr io.Writer
	// Ctx is the optional context used for the command.
	Ctx context.Context
	// IgnoreUndefinedRefs disables the undefined-reference / undefined-citation
	// log gate. TeX `!` errors and non-zero exit codes still fail the build.
	// Used by the format verifier, which only needs identical before/after PDFs
	// and does not care if the source has pre-existing unresolved refs.
	IgnoreUndefinedRefs bool
}

// Run compiles texPath using buildCmd (or the default latexmk invocation if
// buildCmd is empty) and returns the expected output paths. A non-nil error
// indicates a build failure; the returned *Result is still populated so
// callers can inspect log/aux paths for diagnostics.
func Run(texPath, buildCmd string) (*Result, error) {
	return RunWith(Options{TexPath: texPath, BuildCmd: buildCmd})
}

// RunWith is the explicit-options form of Run.
//
// After the build command exits, the freshest <base>.pdf on disk wins:
// rediscovery scans the source dir and the conventional outdir
// candidates (build/, out/, _build/, latex.out/) and re-points every
// artefact path at whichever directory holds the most recently written
// PDF. This happens BEFORE the .log scan and the IgnoreUndefinedRefs
// PDF-presence check, so a custom BuildCmd that writes only to build/
// gets its log read from build/ — not from a stale or missing root
// log — and the rest of the application (sidecar metadata, PDF-pane
// open, SyncTeX open, stale-artefact detection) tracks the user's
// chosen layout.
func RunWith(opts Options) (*Result, error) {
	if opts.TexPath == "" {
		return nil, errors.New("build: empty tex path")
	}
	res := ResolveBuildOutputs(opts.TexPath)
	dir := opts.Dir
	if dir == "" {
		dir = filepath.Dir(opts.TexPath)
	}
	base := strings.TrimSuffix(filepath.Base(opts.TexPath), filepath.Ext(opts.TexPath))

	cmdline := opts.BuildCmd
	if cmdline == "" {
		cmdline = fmt.Sprintf(
			"latexmk -pdf -synctex=1 -interaction=nonstopmode -halt-on-error -file-line-error %s",
			shellQuote(base),
		)
	}

	ctx := opts.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", cmdline)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "MREVDIFF_BASENAME="+base, "MREVIEW_BASENAME="+base)

	stdout := opts.Stdout
	if stdout == nil {
		stdout = io.Discard
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	runErr := cmd.Run()

	// Rediscover BEFORE reading any artefact: a custom BuildCmd or a
	// project latexmkrc with `out_dir` may have written everything to
	// build/ and we need the log scan to follow.
	if found := discoverOutputs(filepath.Dir(opts.TexPath), basenameNoExt(opts.TexPath)); found != nil {
		res = found
	}

	logTail, _ := tailLines(res.LogPath, 40)
	logIssue := scanLogForErrors(res.LogPath)
	if opts.IgnoreUndefinedRefs && logIssue != "" && isUndefinedRefWarning(logIssue) {
		logIssue = ""
	}

	if runErr != nil {
		// In IgnoreUndefinedRefs mode, treat a non-zero latexmk exit as
		// success when the PDF was produced and the only log issue (if any)
		// was an undefined-ref/cite warning we have already filtered.
		// latexmk returns non-zero when it sees undefined refs even though
		// the PDF is valid; the verifier cares only about the produced PDF.
		if opts.IgnoreUndefinedRefs && logIssue == "" {
			if st, statErr := os.Stat(res.PDFPath); statErr == nil && !st.IsDir() && st.Size() > 0 {
				return res, nil
			}
		}
		return res, wrapBuildErr(opts.TexPath, fmt.Sprintf("command failed: %v", runErr), logIssue, logTail)
	}
	if logIssue != "" {
		return res, wrapBuildErr(opts.TexPath, "log reported issues", logIssue, logTail)
	}
	return res, nil
}

// ResolveBuildOutputs returns the conventional output paths next to texPath,
// without invoking the build. Used by --no-build mode and as a base for Run.
func ResolveBuildOutputs(texPath string) *Result {
	return resolveAt(filepath.Dir(texPath), basenameNoExt(texPath))
}

// outdirCandidates lists the subdirectories searched (in order) for build
// outputs when a custom BuildCmd writes them somewhere other than next
// to the source. The list covers the conventional latexmk -outdir
// targets and a few common Makefile patterns.
var outdirCandidates = []string{"build", "out", "_build", "latex.out"}

// ResolveBuildOutputsOnDisk returns the actual on-disk locations of the
// build artefacts. If <base>.pdf is missing next to the source, common
// outdir candidates (build/, out/, _build/, latex.out/) are searched and
// the first directory containing the PDF wins. Used by callers that
// observe an already-built project (--no-build, lmkf-driven reloads)
// where the user's BuildCmd may have written outputs to a custom dir.
func ResolveBuildOutputsOnDisk(texPath string) *Result {
	dir := filepath.Dir(texPath)
	base := basenameNoExt(texPath)
	if found := discoverOutputs(dir, base); found != nil {
		return found
	}
	return resolveAt(dir, base)
}

// discoverOutputs returns a *Result whose PDFPath actually exists, by
// searching dir and the outdirCandidates beneath it. Among directories
// that have a <base>.pdf, the one with the most recently modified PDF
// wins — that's the file the most recent build produced, regardless of
// whether stale artefacts from a previous out_dir layout still sit in
// the source dir or in another candidate. Returns nil if no candidate
// has the expected <base>.pdf at all; callers should then fall back to
// the conventional next-to-source paths.
func discoverOutputs(dir, base string) *Result {
	type cand struct {
		searchDir string
		mtime     time.Time
	}
	var found []cand
	for _, sub := range append([]string{""}, outdirCandidates...) {
		searchDir := dir
		if sub != "" {
			searchDir = filepath.Join(dir, sub)
		}
		pdf := filepath.Join(searchDir, base+".pdf")
		st, err := os.Stat(pdf)
		if err != nil || st.IsDir() || st.Size() == 0 {
			continue
		}
		found = append(found, cand{searchDir, st.ModTime()})
	}
	if len(found) == 0 {
		return nil
	}
	best := found[0]
	for _, c := range found[1:] {
		if c.mtime.After(best.mtime) {
			best = c
		}
	}
	return resolveAt(best.searchDir, base)
}

func resolveAt(dir, base string) *Result {
	return &Result{
		PDFPath:     filepath.Join(dir, base+".pdf"),
		SyncTeXPath: filepath.Join(dir, base+".synctex.gz"),
		AuxPath:     filepath.Join(dir, base+".aux"),
		BBLPath:     filepath.Join(dir, base+".bbl"),
		LogPath:     filepath.Join(dir, base+".log"),
	}
}

func basenameNoExt(p string) string {
	return strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
}

// scanLogForErrors returns the first offending line in the .log file, or "" if
// the log appears clean. It looks for TeX `!` error lines and undefined-ref /
// undefined-citation warnings (the latter only matter once they have survived
// the final compilation pass — latexmk reruns until they're stable, so any
// surviving warning is final).
func scanLogForErrors(logPath string) string {
	f, err := os.Open(logPath)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	return scanLogReader(f)
}

// ScanLogBytes runs the same error/warning heuristics as the per-path
// scanner against an in-memory log buffer. Exported so the lmkf reload
// path (which has already read the log to check for the completion
// marker) can reuse the same policy without re-reading the file or
// duplicating the rule set.
func ScanLogBytes(data []byte) string {
	return scanLogReader(bytes.NewReader(data))
}

// scanLogReader is the shared scan loop for both the path-based and
// byte-based entry points. Its rules are the contract: any change to
// what counts as a build failure happens here.
func scanLogReader(r io.Reader) string {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "!") {
			return trimmed
		}
		if isUndefinedRefWarning(line) {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// isUndefinedRefWarning matches LaTeX's standard warnings for unresolved
// labels and citations. Examples:
//
//	LaTeX Warning: Reference `foo' on page 1 undefined on input line 12.
//	LaTeX Warning: Citation `bar' on page 1 undefined on input line 13.
//	Package natbib Warning: Citation `baz' on page 1 undefined on input line 14.
func isUndefinedRefWarning(line string) bool {
	if !strings.Contains(line, "undefined") {
		return false
	}
	low := strings.ToLower(line)
	if !strings.Contains(low, "warning") {
		return false
	}
	return strings.Contains(line, "Reference `") ||
		strings.Contains(line, "Citation `") ||
		strings.Contains(low, "reference '") ||
		strings.Contains(low, "citation '")
}

// tailLines reads up to n trailing lines from path. Missing files yield an
// empty slice and a non-nil error which the caller may safely ignore.
func tailLines(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	ring := make([]string, 0, n)
	for sc.Scan() {
		if len(ring) == n {
			ring = ring[1:]
		}
		ring = append(ring, sc.Text())
	}
	return ring, sc.Err()
}

// BuildError is returned when latexmk fails or the .log contains errors. It
// carries the captured log tail and the first detected issue (a `!`-prefixed
// TeX error line, or an undefined-reference / undefined-citation warning).
type BuildError struct {
	TexPath  string
	Reason   string
	LogIssue string
	LogTail  []string
}

func (e *BuildError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "build failed for %s: %s", e.TexPath, e.Reason)
	if e.LogIssue != "" {
		fmt.Fprintf(&b, "\n  first issue: %s", e.LogIssue)
	}
	if len(e.LogTail) > 0 {
		b.WriteString("\n  log tail:\n")
		for _, line := range e.LogTail {
			b.WriteString("    ")
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func wrapBuildErr(texPath, reason, issue string, tail []string) error {
	return &BuildError{
		TexPath:  texPath,
		Reason:   reason,
		LogIssue: issue,
		LogTail:  tail,
	}
}

// shellQuote wraps s in single quotes for safe inclusion in a sh -c command.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
