package diffui

import (
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
)

// agterm integration is opportunistic: it activates only when mrevdiff
// runs inside an agterm session (AGTERM_ENABLED=1 with a session id) and
// agtermctl is on PATH; everywhere else every hook is a no-op.
// MREVDIFF_AGTERM=0 disables it explicitly.
//
// Three hooks exist:
//   - the session flag mirrors "this review needs attention" (pending
//     annotations or a failed rebuild) into agterm's flagged working set;
//   - the session name gains a book icon for the length of the review, so
//     a paper under review is recognisable in agterm's sidebar and session
//     picker among sessions that are otherwise shells and agents;
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

// agtermOutput is a test seam over agtermctl invocations whose stdout we read.
var agtermOutput = func(args ...string) ([]byte, error) {
	return exec.Command(agtermCtl, args...).Output()
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

// agtermIcon marks a session as a review at a glance in a sidebar of
// sessions that are otherwise shells and agents. The red book reads at
// sidebar size; the open book (📖) is too pale against the dark sidebar.
const agtermIcon = "📕"

// agtermMarkedName is the name pushed to agterm at startup, "" when the
// session was left alone; agtermOrigName is the name to put back on exit.
var (
	agtermMarkedName string
	agtermOrigName   string
)

// sidebarDecoration matches what agterm prefixes to the name it reports: the
// "⌘3 " / "3· " position marker and a "[03]" activity counter. Both are display
// text rather than part of the name, so they have to come off before the name
// can be handed back to a rename — otherwise a restore bakes them in.
var sidebarDecoration = regexp.MustCompile(`^(?:(?:⌘\d+|\d+·)\s+)?(?:\[\d+\]\s+)?`)

// AgtermMarkSession prefixes the session's own name with the book icon and
// leaves the rest of it alone: a session the user named after the paper keeps
// that name and merely gains the icon. The cmd layer calls it before the TUI
// starts. A rename is sticky, so the name found here is kept for
// AgtermRestoreSessionName; an unreadable name leaves the session untouched
// rather than guessing at one. Best-effort throughout — a failed rename is
// irrelevant to the review.
func AgtermMarkSession() {
	if !agtermAvailable() {
		return
	}
	// One name covers the whole session, so a review in a split or scratch
	// pane would relabel a session whose main pane is running something else.
	// Only the main pane speaks for the session.
	if pane := os.Getenv("AGTERM_PANE"); pane != "" && pane != "left" {
		return
	}
	name := agtermSessionName()
	if name == "" || strings.HasPrefix(name, agtermIcon) {
		return
	}
	agtermOrigName = name
	agtermMarkedName = agtermIcon + " " + name
	_ = agtermRun("session", "rename", agtermMarkedName, "--target", agtermSession)
}

// AgtermRestoreSessionName drops the icon again when the review exits. The
// name is only put back when the session still carries the one we pushed:
// a rename by the user mid-review is theirs to keep.
func AgtermRestoreSessionName() {
	if agtermMarkedName == "" || !agtermAvailable() {
		return
	}
	if agtermSessionName() == agtermMarkedName {
		_ = agtermRun("session", "rename", agtermOrigName, "--target", agtermSession)
	}
	agtermMarkedName, agtermOrigName = "", ""
}

// agtermSessionName reads this session's current name out of the tree.
// Returns "" when agterm cannot be reached or does not know the session.
func agtermSessionName() string {
	out, err := agtermOutput("tree", "--json")
	if err != nil {
		return ""
	}
	var tree struct {
		Result struct {
			Tree struct {
				Workspaces []struct {
					Sessions []struct {
						ID   string `json:"id"`
						Name string `json:"name"`
					} `json:"sessions"`
				} `json:"workspaces"`
			} `json:"tree"`
		} `json:"result"`
	}
	if json.Unmarshal(out, &tree) != nil {
		return ""
	}
	for _, ws := range tree.Result.Tree.Workspaces {
		for _, s := range ws.Sessions {
			if strings.EqualFold(s.ID, agtermSession) {
				return strings.TrimSpace(sidebarDecoration.ReplaceAllString(s.Name, ""))
			}
		}
	}
	return ""
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
