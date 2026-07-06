package pdf

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestWaitStableZeroByteFileIsStable pins the fast-fail fix: a leftover
// 0-byte PDF (pdflatex killed before first shipout) must report stable
// quickly so the caller's LooksComplete re-check rejects it, instead of
// burning the full timeout on every open.
func TestWaitStableZeroByteFileIsStable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dead.pdf")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if !WaitStable(path, 2*time.Second) {
		t.Fatalf("a stable 0-byte file must count as stable")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("stability of a dead file must be detected quickly, took %v", elapsed)
	}
}

func TestWaitStableMissingFileTimesOut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "never.pdf")
	start := time.Now()
	if WaitStable(path, 300*time.Millisecond) {
		t.Fatalf("a missing file must not be stable")
	}
	if elapsed := time.Since(start); elapsed < 250*time.Millisecond {
		t.Fatalf("missing file should wait out the timeout, returned after %v", elapsed)
	}
}

func TestWaitStableSettledFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "done.pdf")
	if err := os.WriteFile(path, []byte("%PDF stuff %%EOF"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !WaitStable(path, 2*time.Second) {
		t.Fatalf("a settled non-empty file must be stable")
	}
}
