package diffui

import (
	"os"
	"os/exec"
	"strings"
	"sync"
)

// agterm integration is opportunistic: it activates only when mrevdiff
// runs inside an agterm session (AGTERM_ENABLED=1 with a session id) and
// agtermctl is on PATH; everywhere else every hook is a no-op.
// MREVDIFF_AGTERM=0 disables it explicitly.
//
// Two hooks exist:
//   - the session flag mirrors "this review needs attention" (pending
//     annotations or a failed rebuild) into agterm's flagged working set;
//   - E external edits run in an agterm overlay on top of the session
//     instead of suspending the TUI, so the PDF frame stays painted while
//     the editor is open.

var (
	agtermOnce    sync.Once
	agtermCtl     string // resolved agtermctl path; "" = integration off
	agtermSession string
)

// agtermRun is a test seam over agtermctl invocations.
var agtermRun = func(args ...string) error {
	return exec.Command(agtermCtl, args...).Run()
}

func agtermSetup() {
	agtermOnce.Do(func() {
		if os.Getenv("MREVDIFF_AGTERM") == "0" {
			return
		}
		if os.Getenv("AGTERM_ENABLED") != "1" {
			return
		}
		sid := strings.TrimSpace(os.Getenv("AGTERM_SESSION_ID"))
		if sid == "" {
			return
		}
		path, err := exec.LookPath("agtermctl")
		if err != nil {
			return
		}
		agtermCtl = path
		agtermSession = sid
	})
}

func agtermAvailable() bool {
	agtermSetup()
	return agtermCtl != ""
}

// agtermSetFlag flips this session's flag in agterm's flagged working-set
// view. Failures are irrelevant to the review — best-effort by design.
func agtermSetFlag(on bool) {
	if !agtermAvailable() {
		return
	}
	state := "off"
	if on {
		state = "on"
	}
	_ = agtermRun("session", "flag", state, "--target", agtermSession)
}

// AgtermClearFlag clears the session flag; the cmd layer calls it after
// the TUI exits so a stale flag never points at a plain shell.
func AgtermClearFlag() {
	agtermSetFlag(false)
}

// syncAgtermFlag reconciles the desired flag state after an Update pass
// and returns a fire-and-forget command when it changed. Flag semantics:
// the session carries pending review feedback (annotations) or a rebuild
// failure that needs the user.
func (m *Model) syncAgtermFlag() func() {
	if !agtermAvailable() {
		return nil
	}
	want := m.buildFailed
	if !want && m.Sidecar != nil && len(m.Sidecar.Annotations) > 0 {
		want = true
	}
	if want == m.agtermFlagged {
		return nil
	}
	m.agtermFlagged = want
	return func() { agtermSetFlag(want) }
}

// agtermOverlayEdit runs shellCmd (a fully quoted editor invocation) in a
// blocking overlay on top of this session. agterm runs overlay commands
// with the GUI PATH (no /opt/homebrew/bin), so the invocation is wrapped
// in a login shell. Returns when the editor exits.
func agtermOverlayEdit(shellCmd, dir string) error {
	args := []string{"session", "overlay", "open", "zsh -lc " + shellQuoteArg(shellCmd),
		"--target", agtermSession, "--block"}
	if dir != "" {
		args = append(args, "--cwd", dir)
	}
	return agtermRun(args...)
}
