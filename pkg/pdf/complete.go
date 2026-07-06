package pdf

import (
	"bytes"
	"os"
	"time"
)

// LooksComplete reports whether the file at path ends with a PDF %%EOF
// trailer (searched in the last 2 KiB). latexmk writes the PDF in place,
// so mid-recompile the file exists but is truncated — opening it then
// makes MuPDF spew parse warnings and can yield a zero-page document.
// Callers should treat an incomplete PDF as "still compiling" and retry,
// keeping the previous document on screen.
func LooksComplete(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil || st.Size() == 0 {
		return false
	}
	const tail = 2048
	off := st.Size() - tail
	if off < 0 {
		off = 0
	}
	buf := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil {
		return false
	}
	return bytes.Contains(buf, []byte("%%EOF"))
}

// WaitStable polls path until its size stops changing between consecutive
// polls (and is non-zero), or the timeout elapses. Catches the window where
// latexmk is still streaming bytes into the PDF: a stat mid-write reports a
// growing size, so two equal consecutive sizes are a cheap "writer is done
// or paused" signal. Returns true when the size stabilised.
func WaitStable(path string, timeout time.Duration) bool {
	const interval = 100 * time.Millisecond
	deadline := time.Now().Add(timeout)
	var lastSize int64 = -1
	for {
		st, err := os.Stat(path)
		if err == nil && st.Size() > 0 && st.Size() == lastSize {
			return true
		}
		if err == nil {
			lastSize = st.Size()
		} else {
			lastSize = -1
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(interval)
	}
}
