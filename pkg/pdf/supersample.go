package pdf

import (
	"os"
	"strings"
	"sync"
)

// fitMaxDPISupersampled bounds the DPI after the supersample multiplier.
// CropFitted renders the *whole page* pixmap at the chosen DPI before
// cropping, so an unbounded 2× on the normal 300 cap would allow ~134 MB
// RGBA pages into the LRU. 450 keeps the worst case near 75 MB while still
// doubling the typical (100-200 DPI) render.
const fitMaxDPISupersampled = 450.0

var superSampleOnce sync.Once
var superSampleValue float64

// SuperSampleFactor returns the DPI multiplier that compensates for
// terminals reporting *logical* (1x) pixel sizes on HiDPI displays.
//
// ghostty (and agterm, which embeds libghostty) report logical pixels via
// TIOCGWINSZ, so a render sized to the reported pane is upscaled 2× by the
// terminal on a retina display and looks blurry. Rendering at 2× the DPI
// while keeping the same cell footprint gives the terminal enough pixels
// to downsample into crisp glyphs. kitty proper reports physical pixels,
// so it gets factor 1.
//
// MREVDIFF_SUPERSAMPLE=1 or =2 overrides the detection.
func SuperSampleFactor() float64 {
	superSampleOnce.Do(func() {
		superSampleValue = detectSuperSample()
	})
	return superSampleValue
}

func detectSuperSample() float64 {
	switch os.Getenv("MREVDIFF_SUPERSAMPLE") {
	case "1":
		return 1.0
	case "2":
		return 2.0
	}
	prog := strings.ToLower(os.Getenv("TERM_PROGRAM"))
	if prog == "ghostty" || prog == "agterm" {
		return 2.0
	}
	if os.Getenv("GHOSTTY_RESOURCES_DIR") != "" {
		return 2.0
	}
	if strings.Contains(strings.ToLower(os.Getenv("TERM")), "ghostty") {
		return 2.0
	}
	return 1.0
}
