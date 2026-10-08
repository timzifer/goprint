package fyneprint

import (
	"context"
	"image"
	"image/color"
	"image/draw"
	"math"

	"github.com/timzifer/goprint"
	"github.com/timzifer/goprint/internal/core"
)

// pageRenderer renders one page (0-based) at dpi; *pdf.Source in the
// dialog, replaced in tests.
type pageRenderer interface {
	RenderPageContext(ctx context.Context, page int, dpi float64) (image.Image, error)
}

// paperFor returns the paper a page of size page lands on, in points, the
// way the backends place it: the chosen media (or, without one, the page's
// own size), turned to the orientation.
func paperFor(page pageSize, media goprint.Media, o goprint.Orientation) pageSize {
	paper := page
	if media.Width > 0 && media.Height > 0 {
		paper = pageSize{float64(media.Width) * 72 / 25400, float64(media.Height) * 72 / 25400}
		if paper.W > paper.H {
			paper.W, paper.H = paper.H, paper.W
		}
	}
	switch o {
	case goprint.Portrait, goprint.ReversePortrait:
		if paper.W > paper.H {
			paper.W, paper.H = paper.H, paper.W
		}
	case goprint.Landscape, goprint.ReverseLandscape:
		if paper.W < paper.H {
			paper.W, paper.H = paper.H, paper.W
		}
	}
	return paper
}

// scalingMode maps goprint's scaling onto core.Layout's modes.
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

// renderSheet draws page on its paper, at most maxPx pixels on the long
// side: the sheet as it would come out of the printer.
func renderSheet(ctx context.Context, r pageRenderer, page int, sz, paper pageSize, s goprint.Scaling, maxPx int) (image.Image, error) {
	k := float64(maxPx) / math.Max(paper.W, paper.H) // pixels per point
	scale, dx, dy := core.Layout(sz.W, sz.H, paper.W, paper.H, scalingMode(s))
	img, err := r.RenderPageContext(ctx, page, 72*k*scale)
	if err != nil {
		return nil, err
	}
	sheet := image.NewRGBA(image.Rect(0, 0, int(math.Round(paper.W*k)), int(math.Round(paper.H*k))))
	draw.Draw(sheet, sheet.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	at := image.Pt(int(math.Round(dx*k)), int(math.Round(dy*k)))
	draw.Draw(sheet, img.Bounds().Sub(img.Bounds().Min).Add(at), img, img.Bounds().Min, draw.Src)
	// Outline the paper so that it stands out from the background.
	edge := image.NewUniform(color.Gray{0xa0})
	b := sheet.Bounds()
	for _, r := range []image.Rectangle{
		{b.Min, image.Pt(b.Max.X, b.Min.Y+1)}, {image.Pt(b.Min.X, b.Max.Y-1), b.Max},
		{b.Min, image.Pt(b.Min.X+1, b.Max.Y)}, {image.Pt(b.Max.X-1, b.Min.Y), b.Max},
	} {
		draw.Draw(sheet, r, edge, image.Point{}, draw.Src)
	}
	return sheet, nil
}
