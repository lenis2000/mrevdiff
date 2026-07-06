package pdf

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"

	"mrevdiff/pkg/synctex"
)

// FitOptions describes the target pane that a crop should fill. CropFitted
// uses these to choose render DPI, vertical context (vpad), and horizontal
// crop bounds so the output PNG matches the pane's pixel dimensions and
// aspect — kitty then aspect-fits without significant up- or downscale.
type FitOptions struct {
	// PaneWidthPx, PaneHeightPx are the pane's inner pixel dimensions
	// (cells × cell pixel size). When either is non-positive CropFitted
	// falls back to a sensible default (DefaultCropDPI, 80pt vpad,
	// full-width).
	PaneWidthPx  int
	PaneHeightPx int

	// MultiColumn = true crops horizontally to the region's column plus
	// a small horizontal padding. For single-column pages, leave false
	// to render the full page width.
	MultiColumn bool

	// VpadPt is the minimum vertical padding on each side of the SyncTeX
	// region — just enough breathing room that descenders on the last
	// line and tops of the first line don't get shaved off by SyncTeX's
	// baseline-ish bounds. Default 10 pt when zero. For single-column
	// crops the actual vpad may grow beyond this so the crop aspect
	// matches the pane aspect (no letterboxing of short regions into
	// big panes); for multi-column crops the vpad stays tight because
	// growing it vertically in a narrow column pulls in unrelated
	// material from adjacent paragraphs.
	VpadPt float64
	// MaxVpadPt ceilings the adaptive vpad so a small region in a
	// short-aspect pane can't inflate the crop to near-page-size.
	// Default 250 pt when zero.
	MaxVpadPt float64

	// HpadPt is the horizontal padding around a column-cropped region.
	// Default 20pt when zero.
	HpadPt float64

	// MarkRegion draws a marker around the exact SyncTeX region inside
	// the crop: crops deliberately include context (adaptive vpad, full
	// column width), so without an anchor the eye has to hunt for the
	// lines that actually correspond to the cursor block.
	MarkRegion bool
}

const (
	// fitVpadDefault is the small fixed vpad applied around the SyncTeX
	// region. Enough breathing room that glyph extrema survive the crop
	// without pulling in surrounding paragraphs.
	fitVpadDefault = 10.0
	// fitMaxVpadDefault ceilings any explicit VpadPt the caller passes.
	// Retained so a caller asking for a wildly large vpad doesn't blow
	// the crop out to the whole page by accident.
	fitMaxVpadDefault = 250.0
	// fitVpadFloorPt is the minimum cap the region-relative vpad bound
	// is allowed to reach. Below this, a one- or two-line paragraph would
	// get a crop with no breathing room above/below.
	fitVpadFloorPt = 60.0
	// fitHpadDefault is the column-crop horizontal margin — wide enough
	// to keep `\item` bullets and inline-math overhang inside the crop,
	// plus glyph left-bearing slack so the leading character of each
	// line survives OCR (tesseract drops first-letter when ink touches
	// the crop edge).
	fitHpadDefault = 20.0
	// fitMinDPI floors the chosen DPI; below ~100 fitz's font hinting
	// produces visibly blurry text even when the pane is tiny.
	fitMinDPI = 100.0
	// fitMaxDPI caps the chosen DPI; a letter page at 300 dpi is
	// ~22 MB in RGBA, multiplied by the page LRU cache. Anything
	// higher costs memory faster than it improves visible sharpness.
	fitMaxDPI = 300.0
	// fitDPIBucket rounds the chosen DPI so cache keys (pageKey.dpi)
	// dedupe near-identical requests.
	fitDPIBucket = 25.0
)

// SuggestDPI returns the render DPI that would make a (cropWPt × cropHPt)
// PDF region fill a (paneWPx × paneHPx) terminal pane after aspect-fit.
// The choice depends on which axis is the limit:
//
//   - aspectCrop > aspectPane (image taller-than-pane shape) → height
//     limited; DPI = paneHPx × 72 / cropHPt;
//   - otherwise → width limited; DPI = paneWPx × 72 / cropWPt.
//
// Result is clamped to [fitMinDPI, fitMaxDPI] and rounded to the nearest
// fitDPIBucket so the page pixmap LRU dedupes near-identical requests.
func SuggestDPI(cropWPt, cropHPt float64, paneWPx, paneHPx int) float64 {
	if cropWPt <= 0 || cropHPt <= 0 || paneWPx <= 0 || paneHPx <= 0 {
		return DefaultCropDPI
	}
	cropAspect := cropHPt / cropWPt
	paneAspect := float64(paneHPx) / float64(paneWPx)
	var dpi float64
	if cropAspect > paneAspect {
		dpi = float64(paneHPx) * 72.0 / cropHPt
	} else {
		dpi = float64(paneWPx) * 72.0 / cropWPt
	}
	if dpi < fitMinDPI {
		dpi = fitMinDPI
	}
	if dpi > fitMaxDPI {
		dpi = fitMaxDPI
	}
	return math.Ceil(dpi/fitDPIBucket) * fitDPIBucket
}

// CropFitted produces a PNG sized to fill (or come close to filling) the
// given pane. Three improvements over CropWithContext:
//
//  1. Adaptive DPI — the page is rendered at the lowest DPI that produces
//     a crop ≥ pane size in the limiting dimension, so kitty doesn't
//     upscale (blur) on big panes or waste memory on small ones.
//  2. Adaptive vpad — vertical context grows or shrinks so the crop's
//     aspect matches the pane's. Wide panes get more context above/
//     below; tall panes get tighter framing. Bounded by MinVpadPt /
//     MaxVpadPt.
//  3. Clamp rebalancing — when the region is near a page edge and one
//     side of the crop would clip, the lost padding is added to the
//     opposite side so the total context window stays roughly constant.
//
// Column awareness is delegated to the caller via MultiColumn — page-
// level layout detection (e.g. median region width) belongs in the UI
// layer that owns the doc/synctex pair.
func CropFitted(d *Doc, r synctex.Region, opts FitOptions) ([]byte, error) {
	if d == nil {
		return nil, fmt.Errorf("pdf: nil doc")
	}
	if !HasExtent(r) {
		return nil, fmt.Errorf("pdf: region has zero extent")
	}
	if opts.VpadPt <= 0 {
		opts.VpadPt = fitVpadDefault
	}
	if opts.MaxVpadPt <= 0 {
		opts.MaxVpadPt = fitMaxVpadDefault
	}
	if opts.HpadPt <= 0 {
		opts.HpadPt = fitHpadDefault
	}
	if opts.VpadPt > opts.MaxVpadPt {
		opts.VpadPt = opts.MaxVpadPt
	}

	// Page bounds in points (= big points; PDF user space).
	bounds, err := d.Bounds(r.Page - 1)
	if err != nil {
		return nil, err
	}
	pageWPt := float64(bounds.Dx())
	pageHPt := float64(bounds.Dy())
	if pageWPt < 1 || pageHPt < 1 {
		return nil, fmt.Errorf("pdf: page %d has zero bounds", r.Page)
	}

	// Horizontal extent in points: full page or column slice.
	cropX0Pt, cropX1Pt := 0.0, pageWPt
	if opts.MultiColumn {
		cropX0Pt = r.X - opts.HpadPt
		cropX1Pt = r.X + r.W + opts.HpadPt
		if cropX0Pt < 0 {
			cropX0Pt = 0
		}
		if cropX1Pt > pageWPt {
			cropX1Pt = pageWPt
		}
	}
	cropWPt := cropX1Pt - cropX0Pt
	if cropWPt < 1 {
		return nil, fmt.Errorf("pdf: degenerate horizontal crop (%.2f pt)", cropWPt)
	}

	// Adaptive vpad — single-column crops grow vertical context so the
	// crop aspect approaches the pane aspect, filling the pane instead
	// of letterboxing a thin strip. Multi-column crops keep the minimum
	// vpad since growing vertically in a narrow column pulls in
	// unrelated material from the same column above/below.
	vpad := opts.VpadPt
	if !opts.MultiColumn && opts.PaneWidthPx > 0 && opts.PaneHeightPx > 0 {
		paneAspect := float64(opts.PaneWidthPx) / float64(opts.PaneHeightPx)
		if paneAspect > 0 {
			targetH := cropWPt / paneAspect
			wantVpad := (targetH - r.H) / 2
			if wantVpad > vpad {
				vpad = wantVpad
			}
			if vpad > opts.MaxVpadPt {
				vpad = opts.MaxVpadPt
			}
			// Region-relative cap: a small region (one paragraph) in a
			// tall pane shouldn't end up dwarfed by hundreds of points
			// of surrounding context. Allow at most one region-height
			// of vpad on each side, with a floor so very small regions
			// still get a usable margin.
			regionCap := r.H
			if regionCap < fitVpadFloorPt {
				regionCap = fitVpadFloorPt
			}
			if vpad > regionCap {
				vpad = regionCap
			}
		}
	}

	// Vertical extent in points, with clamp rebalancing.
	cropY0Pt := r.Y - vpad
	cropY1Pt := r.Y + r.H + vpad
	if cropY0Pt < 0 {
		// Region too close to the page top — recover the lost padding
		// on the bottom so total crop height stays consistent.
		cropY1Pt += -cropY0Pt
		cropY0Pt = 0
	}
	if cropY1Pt > pageHPt {
		excess := cropY1Pt - pageHPt
		cropY0Pt -= excess
		cropY1Pt = pageHPt
	}
	if cropY0Pt < 0 {
		cropY0Pt = 0
	}
	cropHPt := cropY1Pt - cropY0Pt
	if cropHPt < 1 {
		return nil, fmt.Errorf("pdf: degenerate vertical crop (%.2f pt)", cropHPt)
	}

	// Choose render DPI for pane size. On logical-pixel terminals
	// (ghostty/agterm) the pane pixel math undercounts the physical
	// display by 2×, so multiply the DPI back up — same cell footprint,
	// denser pixels, no retina blur.
	dpi := DefaultCropDPI
	if opts.PaneWidthPx > 0 && opts.PaneHeightPx > 0 {
		dpi = SuggestDPI(cropWPt, cropHPt, opts.PaneWidthPx, opts.PaneHeightPx)
	}
	if ss := SuperSampleFactor(); ss > 1 {
		dpi *= ss
		if dpi > fitMaxDPISupersampled {
			dpi = fitMaxDPISupersampled
		}
	}

	img, err := d.Page(r.Page-1, dpi)
	if err != nil {
		return nil, err
	}
	imgBounds := img.Bounds()
	scale := dpi / 72.0

	// Convert crop bounds to pixels, anchored to the rendered image's
	// origin (which need not be (0,0)).
	px0 := imgBounds.Min.X + int(cropX0Pt*scale)
	py0 := imgBounds.Min.Y + int(cropY0Pt*scale)
	px1 := imgBounds.Min.X + int(cropX1Pt*scale)
	py1 := imgBounds.Min.Y + int(cropY1Pt*scale)
	if px0 < imgBounds.Min.X {
		px0 = imgBounds.Min.X
	}
	if py0 < imgBounds.Min.Y {
		py0 = imgBounds.Min.Y
	}
	if px1 > imgBounds.Max.X {
		px1 = imgBounds.Max.X
	}
	if py1 > imgBounds.Max.Y {
		py1 = imgBounds.Max.Y
	}
	if px1 <= px0 || py1 <= py0 {
		return nil, fmt.Errorf("pdf: degenerate pixel crop after clamp")
	}

	cropped := subImage(img, image.Rect(px0, py0, px1, py1))
	if opts.MarkRegion {
		// The region rect in the same pixel space as the (sub)image.
		regionRect := image.Rect(
			imgBounds.Min.X+int(r.X*scale),
			imgBounds.Min.Y+int(r.Y*scale),
			imgBounds.Min.X+int((r.X+r.W)*scale),
			imgBounds.Min.Y+int((r.Y+r.H)*scale),
		)
		cropped = markRegion(cropped, regionRect, scale)
	}
	var buf bytes.Buffer
	if err := fastPNG.Encode(&buf, cropped); err != nil {
		return nil, fmt.Errorf("pdf: encode png: %w", err)
	}
	return buf.Bytes(), nil
}

// markRegionColor is the marker ink: amber, readable on both the white
// page and dark figures without masking the text underneath.
var markRegionColor = color.RGBA{R: 255, G: 165, B: 0, A: 255}

// markRegion returns a copy of img with an amber outline drawn around
// regionRect (given in img's own coordinate space, clamped to bounds).
// It MUST copy: img usually aliases the page pixmap held in the Doc's
// LRU, and drawing in place would permanently scribble the marker onto
// the cached page for every later crop.
func markRegion(img image.Image, regionRect image.Rectangle, scale float64) image.Image {
	bounds := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(dst, dst.Bounds(), img, bounds.Min, draw.Src)

	// A small breathing margin so the outline hugs, not covers, glyph ink;
	// thickness scales with render DPI so it stays a hairline visually.
	pad := int(2 * scale)
	thick := int(scale)
	if thick < 2 {
		thick = 2
	}
	rect := image.Rect(
		regionRect.Min.X-bounds.Min.X-pad,
		regionRect.Min.Y-bounds.Min.Y-pad,
		regionRect.Max.X-bounds.Min.X+pad,
		regionRect.Max.Y-bounds.Min.Y+pad,
	).Intersect(dst.Bounds())
	if rect.Empty() {
		return dst
	}
	blend := func(x, y int) {
		c := dst.RGBAAt(x, y)
		// 70% marker / 30% underlying keeps ink under the line legible.
		c.R = uint8((int(markRegionColor.R)*7 + int(c.R)*3) / 10)
		c.G = uint8((int(markRegionColor.G)*7 + int(c.G)*3) / 10)
		c.B = uint8((int(markRegionColor.B)*7 + int(c.B)*3) / 10)
		c.A = 255
		dst.SetRGBA(x, y, c)
	}
	for x := rect.Min.X; x < rect.Max.X; x++ {
		for t := 0; t < thick; t++ {
			if y := rect.Min.Y + t; y < rect.Max.Y {
				blend(x, y)
			}
			if y := rect.Max.Y - 1 - t; y >= rect.Min.Y {
				blend(x, y)
			}
		}
	}
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for t := 0; t < thick; t++ {
			if x := rect.Min.X + t; x < rect.Max.X {
				blend(x, y)
			}
			if x := rect.Max.X - 1 - t; x >= rect.Min.X {
				blend(x, y)
			}
		}
	}
	return dst
}

// subImage returns img cropped to rect, preferring the image type's own
// SubImage method (zero-copy for *image.RGBA from fitz) and falling back
// to a copy when the type doesn't expose one.
func subImage(img image.Image, rect image.Rectangle) image.Image {
	type subImager interface {
		SubImage(image.Rectangle) image.Image
	}
	if si, ok := any(img).(subImager); ok {
		return si.SubImage(rect)
	}
	dst := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	draw.Draw(dst, dst.Bounds(), img, rect.Min, draw.Src)
	return dst
}
