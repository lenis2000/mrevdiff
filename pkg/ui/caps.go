package ui

import "os"

// KittyGraphicsAvailable reports whether the current terminal is
// believed to handle the kitty graphics protocol. We use env-var
// heuristics rather than a DA/DSR probe because probing would block
// on terminal response at startup and complicate the non-interactive
// test harness.
//
// Recognised hosts:
//   - kitty itself — sets KITTY_WINDOW_ID in every terminal/tab.
//   - ghostty — sets GHOSTTY_RESOURCES_DIR and supports the kitty
//     graphics protocol.
//
// Conservatively disabled under tmux: kitty passthrough inside tmux
// requires the user to opt in via tmux config + the kitty allow-
// passthrough setting, and without that the APC sequences render as
// literal garbage. Users who *have* set up passthrough can force
// rendering with MREVDIFF_FORCE_KITTY=1 (MREVIEW_FORCE_KITTY=1 is
// honoured as a compatibility fallback).
//
// Everything else (iTerm2, Terminal.app, alacritty, xterm, foot,
// wezterm's non-kitty mode, etc.) gets a placeholder in the PDF
// pane instead of raw APC escape garbage.
func KittyGraphicsAvailable() bool {
	if os.Getenv("MREVDIFF_FORCE_KITTY") == "1" || os.Getenv("MREVIEW_FORCE_KITTY") == "1" {
		return true
	}
	if os.Getenv("TMUX") != "" {
		return false
	}
	if os.Getenv("KITTY_WINDOW_ID") != "" {
		return true
	}
	if os.Getenv("GHOSTTY_RESOURCES_DIR") != "" {
		return true
	}
	return false
}
