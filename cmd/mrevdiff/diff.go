package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jessevdk/go-flags"

	"mrevdiff/pkg/diffreview"
	"mrevdiff/pkg/diffui"
	"mrevdiff/pkg/format"
	"mrevdiff/pkg/pdf"
	"mrevdiff/pkg/ui"
)

// exitCodeAnnotations mirrors revdiff's convention: with
// --exit-code-on-annotations, a quit that produced annotations exits 10 so
// launcher scripts can distinguish "reviewed, has feedback" from "reviewed,
// clean" without parsing stdout.
const exitCodeAnnotations = 10

// diffOpts holds the mrevdiff CLI flags.
type diffOpts struct {
	Base    string `long:"base" description:"compare REV:<path> against the working-tree path"`
	NoBuild bool   `long:"no-build" description:"skip latexmk build for the new endpoint"`
	Draft   bool   `long:"draft" description:"open TUI even when the new build fails"`

	BuildCmd string `long:"build-cmd" description:"override latexmk command for the new endpoint"`
	Sidecar  string `long:"sidecar" description:"path to diff sidecar file"`
	Stdout   string `long:"stdout" default:"md" choice:"md" choice:"json" choice:"none" description:"format for diff annotations emitted on quit"`
	Config   string `long:"config" description:"path to config file"`
	NoConfig bool   `long:"noconfig" description:"ignore config files; use built-in defaults"`

	OpenCompare        bool `long:"open-compare" description:"open old and new sources in external compare editor after startup"`
	AllowModifications bool `long:"allow-modifications" description:"allow e/E edits to the new endpoint when it is a real file"`

	ExitCodeOnAnnotations bool   `long:"exit-code-on-annotations" env:"MREVDIFF_EXIT_CODE_ON_ANNOTATIONS" description:"exit 10 when the review produced annotations"`
	HistoryDir            string `long:"history-dir" env:"MREVDIFF_HISTORY_DIR" description:"override the review history directory (default ~/.config/mrevdiff/history)"`
	NoHistory             bool   `long:"no-history" description:"disable review history auto-save"`
	Version               bool   `long:"version" short:"V" description:"print version and exit"`
}

var runDiffTUI = func(model tea.Model, stdout, stderr io.Writer) (tea.Model, error) {
	return runTUI(model, stdout, stderr)
}

// runDiff implements "mrevdiff [FLAGS] --base REV paper.tex",
// "mrevdiff [FLAGS] OLD NEW", and the bare convenience form
// "mrevdiff paper.tex" (equivalent to --base HEAD paper.tex).
func runDiff(args []string, stdout, stderr io.Writer) int {
	var o diffOpts
	p := flags.NewParser(&o, flags.HelpFlag|flags.PassDoubleDash)
	p.Name = "mrevdiff"
	p.Usage = "[OPTIONS] paper.tex | --base REV paper.tex | OLD NEW"

	rest, err := p.ParseArgs(args)
	if err != nil {
		var flagsErr *flags.Error
		if errors.As(err, &flagsErr) && flagsErr.Type == flags.ErrHelp {
			_, _ = fmt.Fprintln(stdout, err.Error())
			return 0
		}
		_, _ = fmt.Fprintf(stderr, "mrevdiff: %v\n", err)
		return 2
	}
	if o.Version {
		_, _ = fmt.Fprintf(stdout, "mrevdiff %s\n", version)
		return 0
	}

	// Bare convenience form: a single existing file with no --base reviews
	// the uncommitted changes of that file, like a bare `revdiff` reviews
	// the working tree. A single argument that fails to stat gets a
	// file-access diagnostic here — falling through would misreport the
	// documented primary form as an incomplete OLD NEW invocation.
	if o.Base == "" && len(rest) == 1 {
		st, statErr := os.Stat(rest[0])
		switch {
		case statErr == nil && !st.IsDir():
			o.Base = "HEAD"
		case statErr == nil:
			_, _ = fmt.Fprintf(stderr, "mrevdiff: %q is a directory, not a .tex file\n", rest[0])
			return 2
		default:
			_, _ = fmt.Fprintf(stderr, "mrevdiff: cannot read %q: %v (single-file form needs an existing file; for two endpoints pass OLD NEW)\n", rest[0], statErr)
			return 2
		}
	}

	if usageErr := validateDiffArgs(o, rest); usageErr != "" {
		_, _ = fmt.Fprintf(stderr, "mrevdiff: %s\n", usageErr)
		_, _ = fmt.Fprintln(stderr, "usage: mrevdiff [OPTIONS] paper.tex | --base REV paper.tex | OLD NEW")
		return 2
	}

	cfg, cfgErr := ui.LoadConfig(o.Config, o.NoConfig)
	if cfgErr != nil {
		_, _ = fmt.Fprintf(stderr, "mrevdiff: %v\n", cfgErr)
		return 1
	}
	cfg = ui.ApplyThemeEnv(cfg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	oldEndpoint, newEndpoint, resolveErr := resolveDiffEndpoints(ctx, o, rest)
	if resolveErr != nil {
		_, _ = fmt.Fprintf(stderr, "mrevdiff: %v\n", resolveErr)
		return 1
	}

	review, reviewErr := diffreview.BuildReview(oldEndpoint, newEndpoint)
	if reviewErr != nil {
		_, _ = fmt.Fprintf(stderr, "mrevdiff: %v\n", reviewErr)
		return 1
	}
	buildCmd := o.BuildCmd
	if buildCmd == "" {
		buildCmd = cfg.BuildCmd
	}
	pdfArtifacts, pdfErr := diffui.PrepareNewPDF(review, diffui.PDFOptions{
		NoBuild:  o.NoBuild,
		Draft:    o.Draft,
		BuildCmd: buildCmd,
		Stderr:   stderr,
		Ctx:      ctx,
	})
	if pdfErr != nil {
		_, _ = fmt.Fprintf(stderr, "mrevdiff: %v\n", pdfErr)
		return 1
	}
	if pdfArtifacts == nil {
		pdfArtifacts = &diffui.PDFArtifacts{}
	}
	finalPDF := pdfArtifacts.PDF
	defer func() {
		if finalPDF != nil {
			_ = finalPDF.Close()
		}
	}()

	stdoutFmt, fmtErr := diffreview.ParseStdoutFormat(o.Stdout)
	if fmtErr != nil {
		_, _ = fmt.Fprintf(stderr, "mrevdiff: %v\n", fmtErr)
		return 2
	}

	sidecarPath := o.Sidecar
	if sidecarPath == "" {
		sidecarPath = diffreview.DefaultSidecarPath(review)
	}
	loadedSidecar, sideErr := diffreview.LoadSidecar(sidecarPath)
	if sideErr != nil {
		_, _ = fmt.Fprintf(stderr, "mrevdiff: load sidecar %q: %v\n", sidecarPath, sideErr)
		return 1
	}
	loadedSidecarMTime := sidecarModTime(sidecarPath)
	sidecar := diffreview.RemapSidecar(loadedSidecar, review)
	sidecarBase := diffreview.CloneSidecar(sidecar)
	issues, issuesErr := diffIssuesForReview(review)
	if issuesErr != nil {
		_, _ = fmt.Fprintf(stderr, "mrevdiff: warning: load fmt-report: %v\n", issuesErr)
	}

	// kitty t=f file transmission: frames go to a session temp dir and the
	// escape carries only the path — megabytes of base64 never cross the
	// PTY. Local kitty/ghostty only; see pdf.KittyFileTransferOK.
	kittyAvailable := ui.KittyGraphicsAvailable()
	kittyXferDir := ""
	waitRenders := func(time.Duration) bool { return true }
	if kittyAvailable && pdf.KittyFileTransferOK() {
		if dir, dirErr := os.MkdirTemp("", "mrevdiff-kitty-"); dirErr == nil {
			kittyXferDir = dir
			defer func() {
				// Bubble Tea does not await in-flight Cmd goroutines on
				// quit, so drain render/prefetch work before removing the
				// directory they write into; retry once for stragglers.
				waitRenders(2 * time.Second)
				if rmErr := os.RemoveAll(dir); rmErr != nil {
					time.Sleep(time.Second)
					_ = os.RemoveAll(dir)
				}
			}()
		}
	}

	allowEdits := o.AllowModifications && review.New.Editable
	model := diffui.New(review, diffui.Options{
		Config:             cfg,
		Styles:             ui.StylesForTheme(cfg.Theme),
		Sidecar:            sidecar,
		SidecarBase:        sidecarBase,
		AllowModifications: allowEdits,
		RequestedAllowMods: o.AllowModifications,
		NoBuild:            o.NoBuild,
		Draft:              o.Draft,
		BuildCmd:           buildCmd,
		SidecarPath:        sidecarPath,
		StdoutFormat:       o.Stdout,
		Issues:             issues,
		OpenCompare:        o.OpenCompare,
		PDF:                pdfArtifacts.PDF,
		Synctex:            pdfArtifacts.Synctex,
		BuildStale:         pdfArtifacts.BuildStale,
		PDFStatus:          pdfArtifactPDFStatus(pdfArtifacts),
		KittyAvailable:     kittyAvailable,
		KittyXferDir:       kittyXferDir,
		Status:             joinStatus(initialDiffStatus(o, review), pdfArtifacts.Status),
	})

	waitRenders = model.WaitPDFRenders

	final, err := runDiffTUI(model, stdout, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "mrevdiff: tui: %v\n", err)
		return 1
	}
	finalSidecar := model.FinalSidecar()
	finalSidecarBase := sidecarBase
	finalReview := review
	discarded := false
	if fm, ok := final.(diffui.Model); ok {
		finalSidecar = fm.FinalSidecar()
		finalSidecarBase = fm.SidecarBase
		finalReview = fm.Review
		finalPDF = fm.PDF
		discarded = fm.Discarded
	}
	if discarded {
		// Q-discard: leave the on-disk sidecar untouched and emit nothing.
		return 0
	}
	// A failed sidecar save must NOT gate the history net or the stdout
	// emit — at this point the session's annotations exist only in memory,
	// and losing all three sinks in the exact failure mode the safety net
	// exists for (read-only dir, full disk, corrupt on-disk sidecar) would
	// discard the user's work. Save what we can, then report the failure.
	saveErr := diffreview.SaveSidecarMerging(sidecarPath, finalSidecarBase, loadedSidecarMTime, finalSidecar)
	if saveErr != nil {
		_, _ = fmt.Fprintf(stderr, "mrevdiff: save sidecar %q: %v\n", sidecarPath, saveErr)
	}
	if !o.NoHistory {
		if histErr := saveHistory(o.HistoryDir, finalSidecar, finalReview); histErr != nil {
			_, _ = fmt.Fprintf(stderr, "mrevdiff: warning: save history: %v\n", histErr)
		}
	}
	if err := diffreview.Emit(stdout, finalSidecar, finalReview, stdoutFmt); err != nil {
		_, _ = fmt.Fprintf(stderr, "mrevdiff: emit: %v\n", err)
		return 1
	}
	if saveErr != nil {
		return 1
	}
	// Detached annotations count as feedback: Emit just printed them, so
	// the exit code must agree with the output (revdiff derives exit 10
	// from the same string it emits).
	if o.ExitCodeOnAnnotations && sidecarHasFeedback(finalSidecar) {
		return exitCodeAnnotations
	}
	return 0
}

// sidecarHasFeedback reports whether the sidecar carries any review
// feedback the emit output includes — attached or detached annotations.
func sidecarHasFeedback(s *diffreview.Sidecar) bool {
	return s != nil && (len(s.Annotations) > 0 || len(s.Detached) > 0)
}

func sidecarModTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// saveHistory writes the review's markdown emit into
// <historyDir>/<project>/<timestamp>.md, mirroring revdiff's always-on
// silent history net. Skipped entirely when the review has no
// annotations. Failures become the caller's stderr warning, never fatal.
func saveHistory(historyDir string, sidecar *diffreview.Sidecar, review *diffreview.Review) error {
	if !sidecarHasFeedback(sidecar) {
		return nil
	}
	if historyDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		historyDir = filepath.Join(home, ".config", "mrevdiff", "history")
	}
	// Bucket by project. A materialized NEW endpoint (git-spec form) lives
	// under <repo>/.mrevdiff/<session>/<rev>/..., so its parent directory
	// is the rev name, not the project — use the repo root instead.
	project := "unknown"
	switch {
	case review == nil:
	case review.New.Materialized && review.New.RepoRoot != "":
		project = filepath.Base(review.New.RepoRoot)
	case review.New.Path != "":
		project = filepath.Base(filepath.Dir(review.New.Path))
	}
	dir := filepath.Join(historyDir, project)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := diffreview.Emit(&buf, sidecar, review, diffreview.StdoutMarkdown); err != nil {
		return err
	}
	name := time.Now().Format("2006-01-02T15-04-05.000") + ".md"
	return os.WriteFile(filepath.Join(dir, name), buf.Bytes(), 0o600)
}

func pdfArtifactPDFStatus(a *diffui.PDFArtifacts) string {
	if a == nil || (a.PDF != nil && a.Synctex != nil) {
		return ""
	}
	if a.BuildStale {
		return "(new PDF needs rebuild)"
	}
	return ""
}

func joinStatus(parts ...string) string {
	out := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		if out == "" {
			out = part
		} else {
			out += " | " + part
		}
	}
	return out
}

func validateDiffArgs(o diffOpts, rest []string) string {
	if o.Base != "" {
		switch len(rest) {
		case 0:
			return "--base requires one filesystem path"
		case 1:
			return ""
		default:
			return "--base cannot be combined with explicit OLD NEW endpoints"
		}
	}
	switch len(rest) {
	case 0:
		return "missing endpoints"
	case 1:
		return "missing NEW endpoint"
	case 2:
		return ""
	default:
		return fmt.Sprintf("unexpected extra endpoint %q", rest[2])
	}
}

func resolveDiffEndpoints(ctx context.Context, o diffOpts, rest []string) (diffreview.Endpoint, diffreview.Endpoint, error) {
	resolver := diffreview.Resolver{}
	if o.Base != "" {
		oldEndpoint, newEndpoint, err := resolver.ResolveBase(ctx, o.Base, rest[0])
		if err != nil {
			return diffreview.Endpoint{}, diffreview.Endpoint{}, fmt.Errorf("resolve --base endpoints: %w", err)
		}
		return oldEndpoint, newEndpoint, nil
	}
	oldEndpoint, newEndpoint, err := resolver.ResolveEndpoints(ctx, rest[0], rest[1])
	if err != nil {
		return diffreview.Endpoint{}, diffreview.Endpoint{}, fmt.Errorf("resolve endpoints: %w", err)
	}
	return oldEndpoint, newEndpoint, nil
}

func initialDiffStatus(o diffOpts, review *diffreview.Review) string {
	if review == nil {
		return ""
	}
	if o.AllowModifications && !review.New.Editable {
		return "new endpoint is read-only; run from the branch and use --base REV path.tex"
	}
	return ""
}

func diffIssuesForReview(review *diffreview.Review) (map[string][]string, error) {
	if review == nil || review.NewDoc == nil || review.New.Kind != diffreview.WorkingFile || review.New.Materialized || review.New.Path == "" {
		return nil, nil
	}
	ext, err := ui.LoadExternalIssues(format.ReportPath(review.New.Path), review.NewDoc)
	if err != nil || len(ext) == 0 {
		return nil, err
	}
	issues := make(map[string][]string)
	for _, pair := range review.Pairs {
		if pair.New == nil {
			continue
		}
		diags := ext[pair.New.ID]
		if len(diags) == 0 {
			continue
		}
		for _, diag := range diags {
			issues[pair.ID] = append(issues[pair.ID], diffIssueText(diag))
		}
	}
	return issues, nil
}

func diffIssueText(diag format.ReportDiag) string {
	switch {
	case diag.RuleID != "" && diag.Message != "":
		return diag.RuleID + ": " + diag.Message
	case diag.RuleID != "":
		return diag.RuleID
	default:
		return diag.Message
	}
}
