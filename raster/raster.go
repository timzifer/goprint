// Package raster renders PDF into PWG Raster and Apple Raster for goprint,
// so that printers which accept no PDF can print. It uses the pure Go
// PDF renderer cera and is a module of its own, so that goprint itself
// does not depend on it.
//
//	ipp := goprint.IPPEverywhere(goprint.IPPEverywhereOptions{Rasterizer: raster.New()})
//	goprint.Default = goprint.NewClient(goprint.System(), ipp)
//
// goprint chooses the format, resolution and color space from the
// printer's attributes; the rasterizer selects the pages, turns and
// scales them onto the sheet and writes the stream as libcups does.
package raster

import (
	"context"
	"fmt"
	"io"

	"github.com/timzifer/cera"
	"github.com/timzifer/goprint"
	"github.com/timzifer/goprint/internal/core"
)

// Rasterizer is a [goprint.Rasterizer] using cera.
type Rasterizer struct{}

var _ goprint.Rasterizer = Rasterizer{}

// New returns a rasterizer.
func New() Rasterizer { return Rasterizer{} }

// Rasterize implements [goprint.Rasterizer]. Pages are rendered one at a
// time, each as a whole sheet at the format's resolution.
func (Rasterizer) Rasterize(ctx context.Context, w io.Writer, pdf []byte, f goprint.RasterFormat) error {
	switch {
	case f.Type != goprint.FormatPWGRaster && f.Type != goprint.FormatURF:
		return fmt.Errorf("%w: raster format %q", goprint.ErrUnsupported, f.Type)
	case f.Resolution.X <= 0 || f.Resolution.X != f.Resolution.Y:
		return fmt.Errorf("%w: raster resolution %v", goprint.ErrInvalid, f.Resolution)
	}
	doc, err := cera.Open(pdf)
	if err != nil {
		return fmt.Errorf("%w: %v", goprint.ErrInvalid, err)
	}
	ranges := make([]core.PageRange, len(f.PageRanges))
	for i, r := range f.PageRanges {
		ranges[i] = core.PageRange{From: r.From, To: r.To}
	}
	pages, _ := core.SelectPages(ranges, doc.NumPages())
	if len(pages) == 0 {
		return fmt.Errorf("%w: no pages selected", goprint.ErrInvalid)
	}
	j := job{pages: len(pages), duplex: f.Duplex, quality: f.Quality, media: f.Media.Name}
	enc := newEncoder(f.Type, w)
	if err := enc.begin(j); err != nil {
		return err
	}
	for _, i := range pages {
		if err := ctx.Err(); err != nil {
			return err
		}
		s, err := renderSheet(ctx, doc, i, f)
		if err != nil {
			return fmt.Errorf("page %d: %w", i+1, err)
		}
		if err := enc.page(j, s); err != nil {
			return err
		}
	}
	return enc.flush()
}
