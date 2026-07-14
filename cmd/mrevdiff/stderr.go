package main

import (
	"os"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// redirectStderr points fd 2 at a temp file for the lifetime of the TUI and
// returns a restore func that puts it back and reports what was captured.
//
// This has to happen at the file-descriptor level rather than by swapping
// os.Stderr: the noise comes from MuPDF, a C library reached through go-fitz,
// which writes to fd 2 itself and never consults any Go writer. While Bubble
// Tea owns the alt-screen, one such line is drawn straight into the frame and
// shifts every row under it — panes duplicate, the source rows of another pair
// bleed in, and the status bar doubles. Rendering a page whose content stream
// has unbalanced marked content is enough to trigger it ("warning: invalid
// marked content and clip nesting"), so any paper can hit it at any time.
//
// Nothing is thrown away: the captured lines are handed back for the caller to
// print once the terminal is ours again.
func redirectStderr() (restore func() []string, err error) {
	f, err := os.CreateTemp("", "mrevdiff-stderr-*")
	if err != nil {
		return nil, err
	}
	cleanup := func() {
		name := f.Name()
		_ = f.Close()
		_ = os.Remove(name)
	}
	saved, err := syscall.Dup(syscall.Stderr)
	if err != nil {
		cleanup()
		return nil, err
	}
	if err := dup2(int(f.Fd()), syscall.Stderr); err != nil {
		_ = syscall.Close(saved)
		cleanup()
		return nil, err
	}
	return func() []string {
		_ = dup2(saved, syscall.Stderr)
		_ = syscall.Close(saved)
		name := f.Name()
		_ = f.Close()
		data, readErr := os.ReadFile(name)
		_ = os.Remove(name)
		if readErr != nil {
			return nil
		}
		return summarizeStderr(string(data))
	}, nil
}

// summarizeStderr collapses the captured output to unique lines in first-seen
// order, tagging repeats with a count. MuPDF re-emits the same warning for
// every page it renders, so without this a long review would dump hundreds of
// identical lines on quit.
func summarizeStderr(out string) []string {
	counts := map[string]int{}
	var order []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if counts[line] == 0 {
			order = append(order, line)
		}
		counts[line]++
	}
	sort.SliceStable(order, func(i, j int) bool { return counts[order[i]] > counts[order[j]] })
	lines := make([]string, 0, len(order))
	for _, line := range order {
		if n := counts[line]; n > 1 {
			line += " (x" + strconv.Itoa(n) + ")"
		}
		lines = append(lines, line)
	}
	return lines
}
