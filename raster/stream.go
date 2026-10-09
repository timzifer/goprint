package raster

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"io"

	"github.com/timzifer/goprint"
)

// sheet is one rendered side of a sheet: 8-bit gray or sRGB, rows top
// to bottom without padding.
type sheet struct {
	width, height, dpi int
	color              bool
	pix                []byte
}

func (s *sheet) bytesPerPixel() int {
	if s.color {
		return 3
	}
	return 1
}

// job are the per-job values of the page headers.
type job struct {
	pages   int
	duplex  goprint.Duplex
	quality goprint.Quality
	media   string // PWG media name, may be empty
}

// encoder writes a raster stream: begin once, then one page per side.
type encoder interface {
	begin(j job) error
	page(j job, s *sheet) error
	flush() error
}

// newEncoder returns the encoder for a goprint raster format.
func newEncoder(format string, w io.Writer) encoder {
	bw := bufio.NewWriterSize(w, 64<<10)
	if format == goprint.FormatURF {
		return &urfEncoder{w: bw}
	}
	return &pwgEncoder{w: bw}
}

// pwgEncoder writes PWG Raster (PWG 5102.4) as libcups does in
// CUPS_RASTER_WRITE_PWG mode.
type pwgEncoder struct{ w *bufio.Writer }

const pwgHeaderSize = 1796

func (e *pwgEncoder) begin(job) error {
	_, err := e.w.WriteString("RaS2")
	return err
}

func (e *pwgEncoder) page(j job, s *sheet) error {
	var h [pwgHeaderSize]byte
	copy(h[0:64], "PwgRaster") // MediaClass
	u32 := func(off int, v uint32) { binary.BigEndian.PutUint32(h[off:], v) }
	if j.duplex == goprint.DuplexLongEdge || j.duplex == goprint.DuplexShortEdge {
		u32(272, 1) // Duplex
	}
	if j.duplex == goprint.DuplexShortEdge {
		u32(368, 1) // Tumble
	}
	u32(276, uint32(s.dpi)) // HWResolution
	u32(280, uint32(s.dpi))
	u32(340, 1)                                   // NumCopies: the printer copies
	u32(352, uint32((s.width*72+s.dpi/2)/s.dpi))  // PageSize, points
	u32(356, uint32((s.height*72+s.dpi/2)/s.dpi)) //
	u32(372, uint32(s.width))                     // cupsWidth
	u32(376, uint32(s.height))                    // cupsHeight
	bpp := s.bytesPerPixel()
	u32(384, 8)                                             // cupsBitsPerColor
	u32(388, uint32(8*bpp))                                 // cupsBitsPerPixel
	u32(392, uint32(s.width*bpp))                           // cupsBytesPerLine
	u32(396, 0)                                             // cupsColorOrder: chunky
	u32(400, map[bool]uint32{false: 18, true: 19}[s.color]) // cupsColorSpace: sGray, sRGB
	u32(420, uint32(bpp))                                   // cupsNumColors
	u32(452, uint32(j.pages))                               // TotalPageCount
	u32(456, 1)                                             // CrossFeedTransform
	u32(460, 1)                                             // FeedTransform
	u32(468, 0)                                             // ImageBoxTop
	u32(472, uint32(s.width))                               // ImageBoxRight
	u32(476, uint32(s.height))                              // ImageBoxBottom
	u32(480, 0xffffff)                                      // AlternatePrimary: white
	u32(484, ippQuality(j.quality))                         // PrintQuality
	copy(h[1732:1796], j.media)                             // cupsPageSizeName
	if _, err := e.w.Write(h[:]); err != nil {
		return err
	}
	return writeLines(e.w, s)
}

func (e *pwgEncoder) flush() error { return e.w.Flush() }

// urfEncoder writes Apple Raster as libcups does in
// CUPS_RASTER_WRITE_APPLE mode.
type urfEncoder struct{ w *bufio.Writer }

func (e *urfEncoder) begin(j job) error {
	var h [12]byte
	copy(h[:], "UNIRAST\x00")
	binary.BigEndian.PutUint32(h[8:], uint32(j.pages))
	_, err := e.w.Write(h[:])
	return err
}

func (e *urfEncoder) page(j job, s *sheet) error {
	var h [32]byte
	h[0] = byte(8 * s.bytesPerPixel())
	if s.color {
		h[1] = 1 // sRGB; 0 is sGray
	}
	switch j.duplex {
	case goprint.DuplexLongEdge:
		h[2] = 3
	case goprint.DuplexShortEdge:
		h[2] = 2
	default:
		h[2] = 1
	}
	h[3] = byte(ippQuality(j.quality))
	binary.BigEndian.PutUint32(h[12:], uint32(s.width))
	binary.BigEndian.PutUint32(h[16:], uint32(s.height))
	binary.BigEndian.PutUint32(h[20:], uint32(s.dpi))
	if _, err := e.w.Write(h[:]); err != nil {
		return err
	}
	return writeLines(e.w, s)
}

func (e *urfEncoder) flush() error { return e.w.Flush() }

// ippQuality is the print-quality enum, 0 for the printer default.
func ippQuality(q goprint.Quality) uint32 {
	switch q {
	case goprint.QualityDraft:
		return 3
	case goprint.QualityNormal:
		return 4
	case goprint.QualityHigh:
		return 5
	}
	return 0
}

// writeLines writes the pixels compressed as both formats do: a repeat
// count for identical lines (up to 256), then each line in a PackBits
// variant counting whole pixels.
func writeLines(w *bufio.Writer, s *sheet) error {
	bpp := s.bytesPerPixel()
	bpl := s.width * bpp
	out := make([]byte, 0, 2*bpl+1)
	for y := 0; y < s.height; {
		line := s.pix[y*bpl : (y+1)*bpl]
		n := 1
		for y+n < s.height && n < 256 && bytes.Equal(line, s.pix[(y+n)*bpl:(y+n+1)*bpl]) {
			n++
		}
		out = append(out[:0], byte(n-1))
		out = packLine(out, line, bpp)
		if _, err := w.Write(out); err != nil {
			return err
		}
		y += n
	}
	return nil
}

// packLine appends line to out: a byte 0–127 repeats the following pixel
// that many times plus one, a byte 129–255 is followed by 257 minus it
// literal pixels. It follows libcups' cups_raster_write.
func packLine(out, line []byte, bpp int) []byte {
	end := len(line)
	last := end - bpp
	for p := 0; p < end; {
		start := p
		p += bpp
		switch {
		case p == end:
			// A single pixel at the end.
			out = append(out, 0)
			out = append(out, line[start:p]...)
		case bytes.Equal(line[start:p], line[p:p+bpp]):
			// Repeated pixels.
			count := 2
			for ; count < 128 && p < last; count, p = count+1, p+bpp {
				if !bytes.Equal(line[p:p+bpp], line[p+bpp:p+2*bpp]) {
					break
				}
			}
			out = append(out, byte(count-1))
			out = append(out, line[p:p+bpp]...)
			p += bpp
		default:
			// Different pixels.
			count := 1
			for ; count < 128 && p < last; count, p = count+1, p+bpp {
				if bytes.Equal(line[p:p+bpp], line[p+bpp:p+2*bpp]) {
					break
				}
			}
			if p >= last && count < 128 {
				count++
				p += bpp
			}
			out = append(out, byte(257-count))
			out = append(out, line[start:start+count*bpp]...)
		}
	}
	return out
}
