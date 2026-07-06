// Package main is the entry point for mrevdiff, a LaTeX-aware semantic
// diff review TUI: it aligns two versions of a paper block-by-block,
// shows old/new source next to the freshly built PDF, and supports
// edit-in-place of the new endpoint plus revdiff-style annotation
// emission on quit.
package main

import (
	"fmt"
	"io"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"mrevdiff/pkg/diffui"
	"mrevdiff/pkg/pdf"
	"mrevdiff/pkg/ui"
)

// version is the mrevdiff release version. Overridable at build time via -ldflags.
var version = "0.1.0"

func main() {
	os.Exit(runDiff(os.Args[1:], os.Stdout, os.Stderr))
}

// runTUI is overridable by tests to bypass tea.NewProgram (which requires a
// real TTY). It returns the final model (so the caller can read the sidecar
// state back out) plus any runtime error.
//
// The TUI draws to /dev/tty rather than the inherited stdout so that
// `mrevdiff --base HEAD paper.tex > review.md` works: TUI escape sequences
// land on the terminal while stdout stays a clean channel for the final
// markdown/JSON emit. When /dev/tty cannot be opened we refuse rather
// than scribbling escape sequences into a redirected stdout, which
// would silently corrupt the documented markdown/JSON sink.
//
// On exit we emit a kitty-delete APC to the TTY (when we have one and the
// terminal supports kitty) so any lingering PDF image is retired before
// control returns to the user's shell. The cleanup is deferred so panics
// and abnormal termination paths get the same treatment as a normal
// prog.Run() return — without this, kitty keeps painting the last crop
// under the shell prompt until the next TIOCGWINSZ clear.
var runTUI = func(model tea.Model, stdout, stderr io.Writer) (tea.Model, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return model, fmt.Errorf("no controlling terminal (cannot open /dev/tty); pipe interactively or use --stdout=none for headless use: %w", err)
	}
	defer func() { _ = tty.Close() }()
	defer func() {
		if ui.KittyGraphicsAvailable() {
			_, _ = fmt.Fprint(tty, pdf.KittyDeleteAll)
		}
	}()
	opts := []tea.ProgramOption{
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
		tea.WithFilter(newMouseInputFilter()),
		tea.WithInput(tty),
		tea.WithOutput(tty),
	}
	prog := tea.NewProgram(model, opts...)
	return prog.Run()
}

// mouseBufferDrainAfterKey is long enough to drain wheel/cell-motion messages
// already queued behind a keyboard event, but short enough that an intentional
// mouse action immediately afterwards still works on human time scales.
const mouseBufferDrainAfterKey = 150 * time.Millisecond

func newMouseInputFilter() func(tea.Model, tea.Msg) tea.Msg {
	return newMouseInputFilterWithClock(time.Now, mouseBufferDrainAfterKey)
}

func newMouseInputFilterWithClock(now func() time.Time, drain time.Duration) func(tea.Model, tea.Msg) tea.Msg {
	var dropMouseUntil time.Time
	return func(model tea.Model, msg tea.Msg) tea.Msg {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			dropMouseUntil = now().Add(drain)
			return msg
		case tea.MouseMsg:
			if !dropMouseUntil.IsZero() {
				if now().Before(dropMouseUntil) {
					return nil
				}
				dropMouseUntil = time.Time{}
			}
			return mouseWheelEdgeFilter(model, msg)
		default:
			return msg
		}
	}
}

func mouseWheelEdgeFilter(model tea.Model, mouse tea.MouseMsg) tea.Msg {
	if m, ok := model.(diffui.Model); ok {
		if m.ShouldDropMouseWheel(mouse) {
			return nil
		}
	}
	return mouse
}
