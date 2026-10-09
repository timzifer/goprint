package goprint

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/timzifer/goprint/ipp"
)

// Raster document formats of IPP printers.
const (
	// FormatPWGRaster is PWG Raster (PWG 5102.4), required by IPP
	// Everywhere.
	FormatPWGRaster = "image/pwg-raster"
	// FormatURF is Apple Raster, required by AirPrint.
	FormatURF = "image/urf"
)

// Rasterizer renders PDF into a raster format, for printers that accept
// no PDF. The module github.com/timzifer/goprint/raster provides one; it
// is passed to [IPPEverywhereOptions.Rasterizer].
type Rasterizer interface {
	// Rasterize writes the pages of pdf to w in the format f describes.
	Rasterize(ctx context.Context, w io.Writer, pdf []byte, f RasterFormat) error
}

// RasterFormat is what a [Rasterizer] produces. It is chosen from the
// printer's attributes and the settings; the rasterizer applies page
// selection, orientation and scaling itself, since a printer does not
// for raster data.
type RasterFormat struct {
	// Type is [FormatPWGRaster] or [FormatURF].
	Type string
	// Resolution is the device resolution; X == Y.
	Resolution Resolution
	// Color selects 8-bit sRGB; otherwise 8-bit gray.
	Color bool
	// Media is the sheet size; zero means each page's own size.
	Media       Media
	Orientation Orientation
	Scaling     Scaling
	// Duplex is recorded in the page headers. Back sides are not
	// transformed: it is only set for printers that print them as they
	// come.
	Duplex     Duplex
	Quality    Quality
	PageRanges []PageRange
}

// rasterAttrs are the printer attributes chooseRaster needs.
var rasterAttrs = []string{
	"document-format-supported", "media-default", "print-color-mode-supported", "color-supported",
	"pwg-raster-document-resolution-supported", "pwg-raster-document-type-supported",
	"pwg-raster-document-sheet-back", "urf-supported",
}

// chooseRaster picks the raster format for a printer with attrs. It
// reports settings it cannot keep as warnings; it fails if the printer
// takes no raster format goprint can produce.
func chooseRaster(attrs ipp.Attributes, s Settings) (RasterFormat, []Warning, error) {
	strs := func(name string) []string {
		if a, ok := attrs.Get(name); ok {
			return a.Strings()
		}
		return nil
	}
	formats := strs("document-format-supported")
	f := RasterFormat{
		Media:       s.Media,
		Orientation: s.Orientation,
		Scaling:     s.Scaling,
		Quality:     s.Quality,
		PageRanges:  s.PageRanges,
	}
	var w []Warning
	var resolutions []int
	var gray, color, backNormal, duplex bool
	switch {
	case slices.Contains(formats, FormatPWGRaster):
		f.Type = FormatPWGRaster
		if a, ok := attrs.Get("pwg-raster-document-resolution-supported"); ok {
			for _, v := range a.Values {
				if r, ok := v.(ipp.Resolution); ok && r.X == r.Y && r.Units == ipp.UnitsDPI {
					resolutions = append(resolutions, int(r.X))
				}
			}
		}
		types := strs("pwg-raster-document-type-supported")
		gray, color = slices.Contains(types, "sgray_8"), slices.Contains(types, "srgb_8")
		back := strs("pwg-raster-document-sheet-back")
		duplex, backNormal = true, len(back) == 0 || back[0] == "normal"
	case slices.Contains(formats, FormatURF):
		f.Type = FormatURF
		for _, kw := range strs("urf-supported") {
			switch {
			case strings.HasPrefix(kw, "RS"):
				for r := range strings.SplitSeq(kw[2:], "-") {
					if n, err := strconv.Atoi(r); err == nil && n > 0 {
						resolutions = append(resolutions, n)
					}
				}
			case kw == "W8":
				gray = true
			case kw == "SRGB24":
				color = true
			case strings.HasPrefix(kw, "DM"):
				duplex, backNormal = true, kw == "DM1"
			}
		}
	default:
		return f, nil, fmt.Errorf("%w: printer takes neither PDF nor a raster format goprint can produce (%s)",
			ErrUnsupported, strings.Join(formats, ", "))
	}
	if len(resolutions) == 0 {
		return f, nil, fmt.Errorf("%w: printer lists no raster resolution", ErrUnsupported)
	}
	dpi := pickResolution(resolutions, s.Quality)
	f.Resolution = Resolution{X: dpi, Y: dpi}

	switch {
	case !gray && !color:
		return f, nil, fmt.Errorf("%w: printer takes no 8-bit gray or sRGB raster", ErrUnsupported)
	case s.Color == Monochrome:
		f.Color = !gray
		if f.Color {
			w = append(w, Warning{"Color", "printer takes no gray raster; printing in color"})
		}
	default:
		f.Color = color
		if s.Color == Color && !color {
			w = append(w, Warning{"Color", "printer cannot print in color"})
		}
	}

	if f.Media.Name == "" && f.Media.Width == 0 {
		if m, err := ParseMedia(strings.Join(strs("media-default"), "")); err == nil {
			f.Media = m
		}
	}

	if s.Duplex == DuplexLongEdge || s.Duplex == DuplexShortEdge {
		switch {
		case !duplex:
			w = append(w, Warning{"Duplex", "printer cannot print two-sided raster"})
		case !backNormal:
			w = append(w, Warning{"Duplex", "printer needs turned back sides, which goprint does not produce yet; printing one-sided"})
		default:
			f.Duplex = s.Duplex
		}
	}
	return f, w, nil
}

// pickResolution chooses from the printer's resolutions: the lowest for
// draft, the highest for high quality, else 300 dpi or the next above
// (the highest if all are lower).
func pickResolution(rs []int, q Quality) int {
	slices.Sort(rs)
	switch q {
	case QualityDraft:
		return rs[0]
	case QualityHigh:
		return rs[len(rs)-1]
	}
	if i := slices.IndexFunc(rs, func(r int) bool { return r >= 300 }); i >= 0 {
		return rs[i]
	}
	return rs[len(rs)-1]
}

// rasterSettings returns s for the job attributes of a raster job: what
// the rasterizer applied (pages, orientation, scaling) is not sent again,
// and two-sided printing follows f.
func rasterSettings(s Settings, f RasterFormat) Settings {
	s.PageRanges, s.Orientation, s.Scaling = nil, OrientationDefault, ScalingDefault
	if f.Duplex == DuplexDefault && (s.Duplex == DuplexLongEdge || s.Duplex == DuplexShortEdge) {
		s.Duplex = DuplexNone
	}
	if f.Media.Name != "" && s.Media.Name == "" && s.Media.Width == 0 {
		s.Media = f.Media
	}
	return s
}
