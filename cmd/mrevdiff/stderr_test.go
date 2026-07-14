package main

import (
	"os"
	"strings"
	"syscall"
	"testing"
)

// TestRedirectStderrCapturesRawFDWrites pins the fix for the frame corruption:
// MuPDF writes its warnings to fd 2 from C, so the redirect must work at the
// file-descriptor level. A write straight to fd 2 (bypassing os.Stderr, exactly
// as the C library does) must be captured, not land on the terminal where Bubble
// Tea would draw it into the frame and shift every row below it.
func TestRedirectStderrCapturesRawFDWrites(t *testing.T) {
	restore, err := redirectStderr()
	if err != nil {
		t.Skipf("stderr redirection unsupported here: %v", err)
	}

	// The C library's route: straight at the descriptor, no Go writer involved.
	if _, err := syscall.Write(syscall.Stderr, []byte("warning: invalid marked content and clip nesting\n")); err != nil {
		restore()
		t.Fatalf("write to fd 2: %v", err)
	}
	// And the Go route, which shares the same descriptor.
	if _, err := os.Stderr.WriteString("mrevdiff go-level noise\n"); err != nil {
		restore()
		t.Fatalf("write to os.Stderr: %v", err)
	}

	got := restore()
	joined := strings.Join(got, "\n")
	for _, want := range []string{"invalid marked content", "go-level noise"} {
		if !strings.Contains(joined, want) {
			t.Errorf("captured stderr is missing %q; got %q", want, joined)
		}
	}
}

// TestSummarizeStderrCollapsesRepeats pins that a warning re-emitted per page
// does not dump hundreds of identical lines when the review quits.
func TestSummarizeStderrCollapsesRepeats(t *testing.T) {
	raw := strings.Repeat("warning: invalid marked content and clip nesting\n", 40) +
		"warning: something else\n\n"
	got := summarizeStderr(raw)

	if len(got) != 2 {
		t.Fatalf("want 2 unique lines, got %d: %q", len(got), got)
	}
	if !strings.Contains(got[0], "(x40)") {
		t.Errorf("repeated warning not collapsed with a count: %q", got[0])
	}
	if strings.Contains(got[1], "(x") {
		t.Errorf("a line seen once must not carry a count: %q", got[1])
	}
}
