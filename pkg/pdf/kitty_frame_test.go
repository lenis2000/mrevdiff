package pdf

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatalf("encode test png: %v", err)
	}
	return buf.Bytes()
}

func TestNextKittyImageID(t *testing.T) {
	a := NextKittyImageID()
	b := NextKittyImageID()
	if a == 0 || b == 0 {
		t.Fatalf("image ids must be non-zero, got %d, %d", a, b)
	}
	if a == b {
		t.Fatalf("consecutive ids must differ, got %d twice", a)
	}
}

func TestKittyDeleteByID(t *testing.T) {
	if got := KittyDeleteByID(42); got != "\x1b_Ga=d,d=I,i=42\x1b\\" {
		t.Fatalf("unexpected delete escape %q", got)
	}
	if got := KittyDeleteByID(0); got != KittyDeleteAll {
		t.Fatalf("id 0 must fall back to delete-all, got %q", got)
	}
}

// TestRenderKittyFrameInline pins the flicker-free contract: the frame
// carries an explicit image id and does NOT start with a delete-all (the
// caller appends a targeted delete of the previous id after the draw).
func TestRenderKittyFrameInline(t *testing.T) {
	pngBytes := testPNG(t, 100, 50)
	esc, err := RenderKittyFrame(pngBytes, 20, 10, 7, "")
	if err != nil {
		t.Fatalf("RenderKittyFrame: %v", err)
	}
	if strings.HasPrefix(esc, KittyDeleteAll) {
		t.Fatalf("frame must not start with delete-all (that reintroduces the blank-pane flicker)")
	}
	if !strings.Contains(esc, ",i=7,") && !strings.Contains(esc, ",i=7;") {
		t.Fatalf("frame must transmit under its image id, got %q", esc[:80])
	}
	if !strings.Contains(esc, "a=T,f=100,C=1") {
		t.Fatalf("frame must keep the C=1 no-cursor-move transmission, got %q", esc[:80])
	}
}

func TestRenderKittyFrameRejectsZeroID(t *testing.T) {
	if _, err := RenderKittyFrame(testPNG(t, 4, 4), 4, 4, 0, ""); err == nil {
		t.Fatalf("id 0 must be rejected")
	}
}

// TestRenderKittyFrameFileTransfer checks t=f mode: the PNG lands on disk
// and the escape carries only the base64 of the absolute path.
func TestRenderKittyFrameFileTransfer(t *testing.T) {
	pngBytes := testPNG(t, 100, 50)
	path := filepath.Join(t.TempDir(), "frame.png")
	esc, err := RenderKittyFrame(pngBytes, 20, 10, 9, path)
	if err != nil {
		t.Fatalf("RenderKittyFrame t=f: %v", err)
	}
	if !strings.Contains(esc, "t=f") {
		t.Fatalf("expected t=f escape, got %q", esc)
	}
	abs, _ := filepath.Abs(path)
	wantPayload := base64.StdEncoding.EncodeToString([]byte(abs))
	if !strings.Contains(esc, ";"+wantPayload+"\x1b\\") {
		t.Fatalf("escape must carry base64 of the path %q, got %q", abs, esc)
	}
	if len(esc) > 512 {
		t.Fatalf("t=f escape should be tiny (path only), got %d bytes", len(esc))
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read transfer file: %v", err)
	}
	if !bytes.Equal(onDisk, pngBytes) {
		t.Fatalf("transfer file must contain the exact PNG bytes")
	}
	// No stray temp files from the atomic write.
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".kitty-frame-") {
			t.Fatalf("atomic write left temp file %s behind", e.Name())
		}
	}
}

func TestKittyFileTransferOK(t *testing.T) {
	clear := func(t *testing.T) {
		for _, k := range []string{"MREVDIFF_KITTY_XFER", "SSH_CONNECTION", "SSH_TTY", "TMUX", "KITTY_WINDOW_ID", "GHOSTTY_RESOURCES_DIR"} {
			t.Setenv(k, "")
		}
	}
	t.Run("ghostty local", func(t *testing.T) {
		clear(t)
		t.Setenv("GHOSTTY_RESOURCES_DIR", "/opt/ghostty")
		if !KittyFileTransferOK() {
			t.Fatalf("local ghostty should use t=f")
		}
	})
	t.Run("ssh disables", func(t *testing.T) {
		clear(t)
		t.Setenv("KITTY_WINDOW_ID", "1")
		t.Setenv("SSH_CONNECTION", "10.0.0.1 1 10.0.0.2 22")
		if KittyFileTransferOK() {
			t.Fatalf("t=f must be off over SSH — the terminal cannot read local paths")
		}
	})
	t.Run("override wins", func(t *testing.T) {
		clear(t)
		t.Setenv("SSH_CONNECTION", "x")
		t.Setenv("MREVDIFF_KITTY_XFER", "file")
		if !KittyFileTransferOK() {
			t.Fatalf("explicit override must win")
		}
		t.Setenv("MREVDIFF_KITTY_XFER", "direct")
		t.Setenv("SSH_CONNECTION", "")
		t.Setenv("KITTY_WINDOW_ID", "1")
		if KittyFileTransferOK() {
			t.Fatalf("direct override must win")
		}
	})
	t.Run("unknown terminal off", func(t *testing.T) {
		clear(t)
		if KittyFileTransferOK() {
			t.Fatalf("no kitty/ghostty indicators — t=f must be off")
		}
	})
}

func TestSuperSampleDetection(t *testing.T) {
	clear := func(t *testing.T) {
		for _, k := range []string{"MREVDIFF_SUPERSAMPLE", "TERM_PROGRAM", "GHOSTTY_RESOURCES_DIR", "TERM"} {
			t.Setenv(k, "")
		}
	}
	cases := []struct {
		name string
		env  map[string]string
		want float64
	}{
		{"plain terminal", nil, 1.0},
		{"ghostty by TERM_PROGRAM", map[string]string{"TERM_PROGRAM": "ghostty"}, 2.0},
		{"agterm", map[string]string{"TERM_PROGRAM": "agterm"}, 2.0},
		{"ghostty by resources dir", map[string]string{"GHOSTTY_RESOURCES_DIR": "/x"}, 2.0},
		{"override off", map[string]string{"GHOSTTY_RESOURCES_DIR": "/x", "MREVDIFF_SUPERSAMPLE": "1"}, 1.0},
		{"override on", map[string]string{"MREVDIFF_SUPERSAMPLE": "2"}, 2.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clear(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			if got := detectSuperSample(); got != tc.want {
				t.Fatalf("detectSuperSample() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLooksComplete(t *testing.T) {
	dir := t.TempDir()
	complete := filepath.Join(dir, "done.pdf")
	if err := os.WriteFile(complete, []byte("%PDF-1.5\nstuff\n%%EOF\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !LooksComplete(complete) {
		t.Fatalf("file ending in %%%%EOF must look complete")
	}
	partial := filepath.Join(dir, "partial.pdf")
	if err := os.WriteFile(partial, []byte("%PDF-1.5\nstill being writ"), 0o600); err != nil {
		t.Fatal(err)
	}
	if LooksComplete(partial) {
		t.Fatalf("truncated file must not look complete")
	}
	if LooksComplete(filepath.Join(dir, "missing.pdf")) {
		t.Fatalf("missing file must not look complete")
	}
	// %%EOF beyond the last 2 KiB does not count — only the tail matters.
	big := filepath.Join(dir, "big.pdf")
	body := append([]byte("%%EOF"), bytes.Repeat([]byte("x"), 4096)...)
	if err := os.WriteFile(big, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if LooksComplete(big) {
		t.Fatalf("%%%%EOF outside the tail window must not count")
	}
}
