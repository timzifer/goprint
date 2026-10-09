package raster

import (
	"context"
	"image"
	"image/color"
	"math"

	"github.com/timzifer/cera"
	"github.com/timzifer/goprint"
	"github.com/timzifer/goprint/internal/core"
)

// renderSheet renders page i of doc onto a sheet as f describes.
func renderSheet(ctx context.Context, doc *cera.Document, i int, f goprint.RasterFormat) (*sheet, error) {
	page, err := doc.Page(i)
	if err != nil {
		return nil, err
	}
	defer page.Release()
	dpi := f.Resolution.X
	pw, ph := page.Size()
	sw, sh := sheetSize(pw, ph, f.Media)
	angle := turn(pw, ph, f.Orientation)
	rw, rh := pw, ph
	if angle == 90 || angle == 270 {
		rw, rh = ph, pw
	}
	scale, dx, dy := core.Layout(rw, rh, sw, sh, scalingMode(f.Scaling))

	px := float64(dpi) / 72
	img := image.NewRGBA(page.Bounds(scale * px))
	err = page.Render(ctx, img, cera.RenderOptions{
		Scale:       scale * px,
		Background:  color.RGBA{255, 255, 255, 255},
		Usage:       cera.UsagePrint,
		Annotations: cera.AnnotsPrint,
	})
	if err != nil {
		return nil, err
	}

	s := &sheet{
		width:  max(1, int(math.Round(sw*px))),
		height: max(1, int(math.Round(sh*px))),
		dpi:    dpi,
		color:  f.Color,
	}
	place(s, img, angle, int(math.Round(dx*px)), int(math.Round(dy*px)))
	return s, nil
}

// sheetSize is the sheet in points, upright: the media, or the page
// itself if no media is given.
func sheetSize(pw, ph float64, m goprint.Media) (w, h float64) {
	w, h = pw, ph
	if m.Width > 0 && m.Height > 0 {
		w, h = float64(m.Width)*72/25400, float64(m.Height)*72/25400
	}
	if w > h {
		w, h = h, w
	}
	return w, h
}

// turn returns the clockwise rotation of a pw×ph page on an upright
// sheet. Landscape content is turned counter-clockwise (IPP
// orientation-requested); without an orientation, landscape pages are
// turned to fit.
func turn(pw, ph float64, o goprint.Orientation) int {
	landscape := pw > ph
	switch o {
	case goprint.ReversePortrait:
		if landscape {
			return 90
		}
		return 180
	case goprint.ReverseLandscape:
		return 90
	case goprint.Landscape:
		return 270
	}
	if landscape {
		return 270
	}
	return 0
}

func scalingMode(s goprint.Scaling) int {
	switch s {
	case goprint.ScalingFit:
		return core.ScaleFit
	case goprint.ScalingFill:
		return core.ScaleFill
	case goprint.ScalingNone:
		return core.ScaleNone
	}
	return core.ScaleAuto
}

// place draws img, turned clockwise by angle, onto the white sheet with
// its top-left corner at (ox, oy).
func place(s *sheet, img *image.RGBA, angle, ox, oy int) {
	bpp := s.bytesPerPixel()
	s.pix = make([]byte, s.width*s.height*bpp)
	for i := range s.pix {
		s.pix[i] = 255
	}
	iw, ih := img.Rect.Dx(), img.Rect.Dy()
	rw, rh := iw, ih
	if angle == 90 || angle == 270 {
		rw, rh = ih, iw
	}
	for y := max(0, oy); y < min(s.height, oy+rh); y++ {
		v := y - oy
		row := s.pix[y*s.width*bpp:]
		for x := max(0, ox); x < min(s.width, ox+rw); x++ {
			u := x - ox
			// (u, v) on the turned page; (sx, sy) on the rendered one.
			var sx, sy int
			switch angle {
			case 90:
				sx, sy = v, ih-1-u
			case 180:
				sx, sy = iw-1-u, ih-1-v
			case 270:
				sx, sy = iw-1-v, u
			default:
				sx, sy = u, v
			}
			o := sy*img.Stride + sx*4
			r, g, b := img.Pix[o], img.Pix[o+1], img.Pix[o+2]
			if bpp == 3 {
				row[x*3], row[x*3+1], row[x*3+2] = r, g, b
			} else {
				row[x] = byte((299*int(r) + 587*int(g) + 114*int(b) + 500) / 1000)
			}
		}
	}
}
