package pdf

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/png"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

// kittyChunkSize is the per-APC payload size for kitty graphics. 4096 base64
// chars is the standard safe ceiling that all kitty implementations accept.
const kittyChunkSize = 4096

// KittyDeleteAll is the APC sequence that removes every visible kitty graphics
// placement from the terminal and frees its backing image data. It's idempotent
// (safe to emit when no image exists) and is exported so UI transitions away
// from an image (to status text, to a placeholder, to quit) can explicitly
// retire the bitmap — without this, the previous crop stays painted because
// the kitty plane is independent of Bubble Tea's text buffer.
const KittyDeleteAll = "\x1b_Ga=d,d=A\x1b\\"

// RenderKitty converts PNG bytes into a kitty-graphics escape sequence sized
// to fit inside a (widthCells × heightCells) terminal cell region.
//
// Two non-obvious choices the escape encodes:
//   - C=1 ("do not move cursor"). Without it the terminal advances the cursor
//     to the bottom-right of the drawn image, which throws off any subsequent
//     space-padding lipgloss writes to fill the pane and shreds the layout.
//   - c/r are computed from the image's *actual* aspect ratio against detected
//     cell pixel size, not the full target rectangle. Kitty stretches the
//     image to (c×cellW, r×cellH) by default, so passing the raw target box
//     produces wildly distorted text on wide-and-short crops (e.g. a single
//     line of LaTeX prose). Aspect-fit shrinks one dimension to preserve the
//     PDF's true proportions.
//
// Kitty-only: mrevdiff does not support sixel or iTerm2 fallbacks.
func RenderKitty(pngBytes []byte, widthCells, heightCells int) (string, error) {
	if len(pngBytes) == 0 {
		return "", fmt.Errorf("pdf: empty png bytes")
	}
	if widthCells < 1 || heightCells < 1 {
		return "", fmt.Errorf("pdf: target cells must be positive (got %dx%d)", widthCells, heightCells)
	}
	img, _, err := image.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return "", fmt.Errorf("pdf: decode png: %w", err)
	}
	bounds := img.Bounds()
	imgPxW := float64(bounds.Dx())
	imgPxH := float64(bounds.Dy())
	if imgPxW < 1 || imgPxH < 1 {
		return "", fmt.Errorf("pdf: image has zero extent")
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

	encoded := base64.StdEncoding.EncodeToString(pngBytes)
	var sb strings.Builder
	// Clear any previously-displayed kitty images first. Without this, the
	// previous block's crop stays painted on top of the cells when the new
	// image is smaller (aspect-fit can shrink a wide-and-short crop down to
	// just a few rows, leaving the old image's lower portion visible).
	sb.WriteString(KittyDeleteAll)
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
			fmt.Fprintf(&sb, "\x1b_Ga=T,f=100,C=1,c=%d,r=%d,q=2,m=%d;%s\x1b\\",
				fitW, fitH, more, chunk)
		} else {
			fmt.Fprintf(&sb, "\x1b_Gm=%d;%s\x1b\\", more, chunk)
		}
	}
	return sb.String(), nil
}

// DetectCellPixelSize is the exported wrapper around detectCellPixelSize
// used by callers (notably the UI's pane→pixel math) outside this package.
func DetectCellPixelSize() (float64, float64) {
	return detectCellPixelSize()
}

// detectCellPixelSize returns the pixel dimensions of one terminal cell.
// Tries TIOCGWINSZ first; falls back to typical kitty defaults if the kernel
// doesn't report pixel sizes (some non-kitty terminals leave Xpixel/Ypixel
// zeroed even though they support kitty graphics via passthrough).
func detectCellPixelSize() (float64, float64) {
	pixW, pixH := terminalPixelSize()
	colW, rowH := terminalCellSize()
	if pixW > 0 && pixH > 0 && colW > 0 && rowH > 0 {
		cw := float64(pixW) / float64(colW)
		ch := float64(pixH) / float64(rowH)
		if cw > 4 && ch > 8 {
			return cw, ch
		}
	}
	return 9.0, 18.0
}

func terminalPixelSize() (int, int) {
	var ws struct {
		Row, Col, Xpixel, Ypixel uint16
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL,
		uintptr(syscall.Stdout),
		uintptr(syscall.TIOCGWINSZ),
		uintptr(unsafe.Pointer(&ws))); errno == 0 && ws.Xpixel > 0 && ws.Ypixel > 0 {
		return int(ws.Xpixel), int(ws.Ypixel)
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDONLY, 0)
	if err != nil {
		return 0, 0
	}
	defer func() { _ = tty.Close() }()
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL,
		uintptr(tty.Fd()),
		uintptr(syscall.TIOCGWINSZ),
		uintptr(unsafe.Pointer(&ws))); errno == 0 && ws.Xpixel > 0 && ws.Ypixel > 0 {
		return int(ws.Xpixel), int(ws.Ypixel)
	}
	return 0, 0
}

func terminalCellSize() (int, int) {
	var ws struct {
		Row, Col, Xpixel, Ypixel uint16
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL,
		uintptr(syscall.Stdout),
		uintptr(syscall.TIOCGWINSZ),
		uintptr(unsafe.Pointer(&ws))); errno == 0 && ws.Col > 0 && ws.Row > 0 {
		return int(ws.Col), int(ws.Row)
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDONLY, 0)
	if err != nil {
		return 0, 0
	}
	defer func() { _ = tty.Close() }()
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL,
		uintptr(tty.Fd()),
		uintptr(syscall.TIOCGWINSZ),
		uintptr(unsafe.Pointer(&ws))); errno == 0 && ws.Col > 0 && ws.Row > 0 {
		return int(ws.Col), int(ws.Row)
	}
	return 0, 0
}

// NoRegionPlaceholder is the body text shown in the PDF pane when the cursor
// block has no SyncTeX mapping (e.g. a block that lives outside any page,
// such as an outer `\section` header with only whitespace inside).
const NoRegionPlaceholder = "[no region — block outside PDF]"
