package pdf

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// kittyIDCounter allocates process-unique kitty image ids. Seeded from the
// pid and wall clock so ids from a previous mrevdiff run still held by the
// terminal are never reused (and therefore never wrongly deleted).
var kittyIDCounter atomic.Uint32

func init() {
	seed := uint32(os.Getpid())<<16 | uint32(time.Now().UnixNano()&0xffff)
	if seed == 0 {
		seed = 1
	}
	kittyIDCounter.Store(seed)
}

// NextKittyImageID returns a fresh image id for a kitty a=T transmission.
// Never returns 0 (0 means "no id" throughout this package).
func NextKittyImageID() uint32 {
	for {
		id := kittyIDCounter.Add(1)
		if id != 0 {
			return id
		}
	}
}

// KittyDeleteByID returns the APC that deletes one image by id and frees
// its backing data (d=I, uppercase). Emitting it *after* the replacement
// frame's a=T is the flicker-free swap: the new image paints over the old
// one, then the old bitmap is retired — the pane is never blank in between.
func KittyDeleteByID(id uint32) string {
	if id == 0 {
		return KittyDeleteAll
	}
	return fmt.Sprintf("\x1b_Ga=d,d=I,i=%d\x1b\\", id)
}

// KittyFileTransferOK reports whether t=f file transmission is safe to use:
// the terminal must support the kitty graphics protocol's file medium AND
// be able to open local paths (ruled out over SSH, where the terminal runs
// on a different machine, and under tmux passthrough). kitty and ghostty
// (incl. agterm) both implement t=f.
//
// MREVDIFF_KITTY_XFER=file|direct overrides the detection.
func KittyFileTransferOK() bool {
	switch os.Getenv("MREVDIFF_KITTY_XFER") {
	case "file":
		return true
	case "direct":
		return false
	}
	if os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" {
		return false
	}
	if os.Getenv("TMUX") != "" {
		return false
	}
	return os.Getenv("KITTY_WINDOW_ID") != "" || os.Getenv("GHOSTTY_RESOURCES_DIR") != ""
}

// kittyFitCells aspect-fits a decoded PNG into a (widthCells × heightCells)
// region, mirroring RenderKitty's c/r math (see that doc comment for why
// aspect-fit against detected cell pixel size matters).
func kittyFitCells(pngBytes []byte, widthCells, heightCells int) (int, int, error) {
	img, _, err := image.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return 0, 0, fmt.Errorf("pdf: decode png: %w", err)
	}
	bounds := img.Bounds()
	return kittyFitCellsSized(bounds.Dx(), bounds.Dy(), widthCells, heightCells)
}

// kittyFitCellsSized is kittyFitCells for callers that already know the
// image's pixel dimensions (CropFitted/RenderPageFitted return them), so
// the interactive path never decodes the PNG it just encoded.
func kittyFitCellsSized(pxW, pxH, widthCells, heightCells int) (int, int, error) {
	imgPxW := float64(pxW)
	imgPxH := float64(pxH)
	if imgPxW < 1 || imgPxH < 1 {
		return 0, 0, fmt.Errorf("pdf: image has zero extent")
	}
	cellW, cellH := detectCellPixelSize()
	targetPxW := float64(widthCells) * cellW
	targetPxH := float64(heightCells) * cellH
	aspect := imgPxH / imgPxW
	finalPxW := targetPxW
	finalPxH := finalPxW * aspect
	if finalPxH > targetPxH {
		finalPxH = targetPxH
		finalPxW = finalPxH / aspect
	}
	fitW := int(finalPxW / cellW)
	fitH := int(finalPxH / cellH)
	if fitW < 1 {
		fitW = 1
	}
	if fitH < 1 {
		fitH = 1
	}
	if fitW > widthCells {
		fitW = widthCells
	}
	if fitH > heightCells {
		fitH = heightCells
	}
	return fitW, fitH, nil
}

// RenderKittyFrame converts PNG bytes into a kitty-graphics escape for one
// frame of the PDF pane. Unlike RenderKitty it does NOT prepend a delete-all:
// the frame carries an explicit image id, and the caller swaps frames
// flicker-free by emitting this escape followed by KittyDeleteByID(prevID).
//
// When transferPath is non-empty the PNG is written to that file (atomic
// tmp+rename in the same directory) and transmitted as t=f — the escape then
// carries only the base64 of the *path* (~200 bytes) instead of megabytes of
// base64 pixel data through the PTY, and the terminal reads the file itself.
// The file must outlive the terminal's parse of the escape; the caller owns
// its lifecycle (mrevdiff keys files to its render cache and deletes them on
// eviction or exit).
func RenderKittyFrame(pngBytes []byte, widthCells, heightCells int, id uint32, transferPath string) (string, error) {
	return renderKittyFrame(pngBytes, 0, 0, widthCells, heightCells, id, transferPath)
}

// RenderKittyFrameSized is RenderKittyFrame for callers that already know
// the PNG's pixel dimensions, skipping the decode-for-dims round trip.
func RenderKittyFrameSized(pngBytes []byte, pxW, pxH, widthCells, heightCells int, id uint32, transferPath string) (string, error) {
	return renderKittyFrame(pngBytes, pxW, pxH, widthCells, heightCells, id, transferPath)
}

func renderKittyFrame(pngBytes []byte, pxW, pxH, widthCells, heightCells int, id uint32, transferPath string) (string, error) {
	if len(pngBytes) == 0 {
		return "", fmt.Errorf("pdf: empty png bytes")
	}
	if widthCells < 1 || heightCells < 1 {
		return "", fmt.Errorf("pdf: target cells must be positive (got %dx%d)", widthCells, heightCells)
	}
	if id == 0 {
		return "", fmt.Errorf("pdf: image id must be non-zero")
	}
	var fitW, fitH int
	var err error
	if pxW > 0 && pxH > 0 {
		fitW, fitH, err = kittyFitCellsSized(pxW, pxH, widthCells, heightCells)
	} else {
		fitW, fitH, err = kittyFitCells(pngBytes, widthCells, heightCells)
	}
	if err != nil {
		return "", err
	}

	if transferPath != "" {
		abs, err := filepath.Abs(transferPath)
		if err != nil {
			return "", fmt.Errorf("pdf: transfer path: %w", err)
		}
		if err := writeFileAtomicSameDir(abs, pngBytes); err != nil {
			return "", fmt.Errorf("pdf: write transfer file: %w", err)
		}
		encodedPath := base64.StdEncoding.EncodeToString([]byte(abs))
		return fmt.Sprintf("\x1b_Ga=T,f=100,t=f,C=1,c=%d,r=%d,q=2,i=%d;%s\x1b\\",
			fitW, fitH, id, encodedPath), nil
	}

	encoded := base64.StdEncoding.EncodeToString(pngBytes)
	var sb strings.Builder
	for i := 0; i < len(encoded); i += kittyChunkSize {
		end := i + kittyChunkSize
		if end > len(encoded) {
			end = len(encoded)
		}
		chunk := encoded[i:end]
		more := 1
		if end >= len(encoded) {
			more = 0
		}
		if i == 0 {
			fmt.Fprintf(&sb, "\x1b_Ga=T,f=100,C=1,c=%d,r=%d,q=2,i=%d,m=%d;%s\x1b\\",
				fitW, fitH, id, more, chunk)
		} else {
			fmt.Fprintf(&sb, "\x1b_Gm=%d;%s\x1b\\", more, chunk)
		}
	}
	return sb.String(), nil
}

// writeFileAtomicSameDir writes data to path via a temp file + rename in the
// same directory, so the terminal can never observe a half-written PNG.
func writeFileAtomicSameDir(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".kitty-frame-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}
