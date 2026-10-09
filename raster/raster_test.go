package raster

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"io"
	"testing"

	"github.com/timzifer/goprint"
	"github.com/timzifer/goprint/internal/testpdf"
	"github.com/timzifer/goprint/ipp"
	"github.com/timzifer/goprint/ipp/ipptest"
)

// decodedPage is a page read back from a raster stream.
type decodedPage struct {
	header        []byte
	width, height int
	bpp           int
	pix           []byte
}

// unpackLines decodes height lines of width pixels, the inverse of
// writeLines.
func unpackLines(r *bufio.Reader, width, height, bpp int) ([]byte, error) {
	bpl := width * bpp
	pix := make([]byte, 0, bpl*height)
	for y := 0; y < height; {
		rep, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		line := make([]byte, 0, bpl)
		for len(line) < bpl {
			c, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			n := int(c) + 1
			px := make([]byte, bpp)
			if c <= 127 {
				if _, err := io.ReadFull(r, px); err != nil {
					return nil, err
				}
				for range n {
					line = append(line, px...)
				}
				continue
			}
			n = 257 - int(c)
			lit := make([]byte, n*bpp)
			if _, err := io.ReadFull(r, lit); err != nil {
				return nil, err
			}
			line = append(line, lit...)
		}
		if len(line) != bpl {
			return nil, fmt.Errorf("line %d: %d bytes, want %d", y, len(line), bpl)
		}
		for i := 0; i <= int(rep) && y < height; i++ {
			pix = append(pix, line...)
			y++
		}
	}
	return pix, nil
}

func decodePWG(t *testing.T, b []byte) []decodedPage {
	t.Helper()
	if !bytes.HasPrefix(b, []byte("RaS2")) {
		t.Fatalf("no PWG sync: %.8q", b)
	}
	r := bufio.NewReader(bytes.NewReader(b[4:]))
	var pages []decodedPage
	for {
		h := make([]byte, pwgHeaderSize)
		if _, err := io.ReadFull(r, h); err == io.EOF {
			return pages
		} else if err != nil {
			t.Fatal(err)
		}
		u := func(off int) int { return int(binary.BigEndian.Uint32(h[off:])) }
		p := decodedPage{header: h, width: u(372), height: u(376), bpp: u(388) / 8}
		pix, err := unpackLines(r, p.width, p.height, p.bpp)
		if err != nil {
			t.Fatal(err)
		}
		p.pix = pix
		pages = append(pages, p)
	}
}

func decodeURF(t *testing.T, b []byte) (int, []decodedPage) {
	t.Helper()
	if !bytes.HasPrefix(b, []byte("UNIRAST\x00")) {
		t.Fatalf("no URF sync: %.8q", b)
	}
	count := int(binary.BigEndian.Uint32(b[8:]))
	r := bufio.NewReader(bytes.NewReader(b[12:]))
	var pages []decodedPage
	for {
		h := make([]byte, 32)
		if _, err := io.ReadFull(r, h); err == io.EOF {
			return count, pages
		} else if err != nil {
			t.Fatal(err)
		}
		u := func(off int) int { return int(binary.BigEndian.Uint32(h[off:])) }
		p := decodedPage{header: h, width: u(12), height: u(16), bpp: int(h[0]) / 8}
		pix, err := unpackLines(r, p.width, p.height, p.bpp)
		if err != nil {
			t.Fatal(err)
		}
		p.pix = pix
		pages = append(pages, p)
	}
}

func TestPackLineRoundTrip(t *testing.T) {
	lines := [][]byte{
		{7},
		{1, 1},
		{1, 2},
		{5, 5, 5, 5, 1, 2, 3, 3, 9},
		bytes.Repeat([]byte{0}, 300),
		func() []byte { // 300 different pixels: more than one literal run
			b := make([]byte, 300)
			for i := range b {
				b[i] = byte(i)
			}
			return b
		}(),
	}
	for _, line := range lines {
		for _, bpp := range []int{1, 3} {
			l := bytes.Repeat(line, bpp) // keeps len a multiple of bpp
			if bpp == 3 {
				l = nil
				for _, c := range line {
					l = append(l, c, c^0x55, c)
				}
			}
			w := len(l) / bpp
			s := &sheet{width: w, height: 3, dpi: 72, color: bpp == 3, pix: bytes.Repeat(l, 3)}
			var buf bytes.Buffer
			bw := bufio.NewWriter(&buf)
			if err := writeLines(bw, s); err != nil {
				t.Fatal(err)
			}
			bw.Flush()
			if buf.Bytes()[0] != 2 {
				t.Errorf("line repeat count %d, want 2", buf.Bytes()[0])
			}
			got, err := unpackLines(bufio.NewReader(&buf), w, 3, bpp)
			if err != nil || !bytes.Equal(got, s.pix) {
				t.Errorf("bpp %d, line %v: round trip %v, %v", bpp, line, got, err)
			}
		}
	}
}

func TestPackLineMatchesLibcups(t *testing.T) {
	// Output of libcups' cups_raster_write for these 1-byte pixels.
	for _, tc := range []struct{ line, want []byte }{
		{[]byte{9}, []byte{0, 9}},
		{[]byte{4, 4, 4}, []byte{2, 4}},
		{[]byte{1, 2, 3}, []byte{254, 1, 2, 3}},
		// A single literal pixel is written as 257-1, which wraps to 0.
		{[]byte{1, 2, 2, 2}, []byte{0, 1, 2, 2}},
	} {
		if got := packLine(nil, tc.line, 1); !bytes.Equal(got, tc.want) {
			t.Errorf("packLine(%v) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

func a4PDF(pages int) []byte { return testpdf.Generate(pages, testpdf.A4Width, testpdf.A4Height) }

func TestRasterizePWG(t *testing.T) {
	var buf bytes.Buffer
	f := goprint.RasterFormat{
		Type: goprint.FormatPWGRaster, Resolution: goprint.Resolution{X: 72, Y: 72},
		Media: goprint.MediaA4, Duplex: goprint.DuplexShortEdge, Quality: goprint.QualityHigh,
		PageRanges: []goprint.PageRange{{From: 3, To: 3}, {From: 1, To: 1}},
	}
	if err := New().Rasterize(context.Background(), &buf, a4PDF(3), f); err != nil {
		t.Fatal(err)
	}
	pages := decodePWG(t, buf.Bytes())
	if len(pages) != 2 {
		t.Fatalf("%d pages", len(pages))
	}
	p := pages[0]
	h := p.header
	u := func(off int) uint32 { return binary.BigEndian.Uint32(h[off:]) }
	if string(h[:9]) != "PwgRaster" || p.width != 595 || p.height != 842 || p.bpp != 1 {
		t.Fatalf("header: %q %dx%d bpp %d", h[:9], p.width, p.height, p.bpp)
	}
	if u(276) != 72 || u(352) != 595 || u(356) != 842 || u(272) != 1 || u(368) != 1 ||
		u(400) != 18 || u(420) != 1 || u(452) != 2 || u(480) != 0xffffff || u(484) != 5 {
		t.Errorf("header fields: res %d, size %dx%d, duplex %d tumble %d, cspace %d, colors %d, pages %d, alt %x, quality %d",
			u(276), u(352), u(356), u(272), u(368), u(400), u(420), u(452), u(480), u(484))
	}
	if got := string(bytes.TrimRight(h[1732:], "\x00")); got != "iso_a4_210x297mm" {
		t.Errorf("page size name %q", got)
	}
	if !hasInk(p.pix) {
		t.Error("page is blank")
	}
}

func TestRasterizeURFColorLandscape(t *testing.T) {
	landscape := testpdf.Generate(1, testpdf.A4Height, testpdf.A4Width)
	var buf bytes.Buffer
	f := goprint.RasterFormat{Type: goprint.FormatURF, Resolution: goprint.Resolution{X: 72, Y: 72}, Color: true, Media: goprint.MediaA4}
	if err := New().Rasterize(context.Background(), &buf, landscape, f); err != nil {
		t.Fatal(err)
	}
	count, pages := decodeURF(t, buf.Bytes())
	if count != 1 || len(pages) != 1 {
		t.Fatalf("count %d, pages %d", count, len(pages))
	}
	p := pages[0]
	if p.width != 595 || p.height != 842 || p.bpp != 3 || p.header[1] != 1 || p.header[2] != 1 ||
		binary.BigEndian.Uint32(p.header[20:]) != 72 {
		t.Errorf("header %v, %dx%d", p.header, p.width, p.height)
	}
	if !hasInk(p.pix) {
		t.Error("page is blank")
	}
}

func hasInk(pix []byte) bool {
	for _, b := range pix {
		if b < 128 {
			return true
		}
	}
	return false
}

func TestTurn(t *testing.T) {
	for _, tc := range []struct {
		pw, ph float64
		o      goprint.Orientation
		want   int
	}{
		{595, 842, goprint.OrientationDefault, 0},
		{842, 595, goprint.OrientationDefault, 270},
		{595, 842, goprint.Landscape, 270},
		{595, 842, goprint.ReverseLandscape, 90},
		{595, 842, goprint.ReversePortrait, 180},
		{842, 595, goprint.Portrait, 270},
	} {
		if got := turn(tc.pw, tc.ph, tc.o); got != tc.want {
			t.Errorf("turn(%v×%v, %v) = %d, want %d", tc.pw, tc.ph, tc.o, got, tc.want)
		}
	}
}

func TestPlaceTurns(t *testing.T) {
	// A 2×1 image with a dark left pixel, turned onto a 1×2 sheet.
	img := imageRGBA(2, 1, []byte{0, 255})
	for _, tc := range []struct {
		angle int
		want  []byte
	}{
		{90, []byte{0, 255}},  // clockwise: left goes to the top
		{270, []byte{255, 0}}, // counter-clockwise: left goes to the bottom
	} {
		s := &sheet{width: 1, height: 2, dpi: 72}
		place(s, img, tc.angle, 0, 0)
		if !bytes.Equal(s.pix, tc.want) {
			t.Errorf("angle %d: %v, want %v", tc.angle, s.pix, tc.want)
		}
	}
}

func TestRasterizeErrors(t *testing.T) {
	ok := goprint.RasterFormat{Type: goprint.FormatPWGRaster, Resolution: goprint.Resolution{X: 72, Y: 72}}
	bad := ok
	bad.Type = "image/jpeg"
	if err := New().Rasterize(context.Background(), io.Discard, a4PDF(1), bad); !errors.Is(err, goprint.ErrUnsupported) {
		t.Errorf("format: %v", err)
	}
	if err := New().Rasterize(context.Background(), io.Discard, []byte("no pdf"), ok); !errors.Is(err, goprint.ErrInvalid) {
		t.Errorf("not a PDF: %v", err)
	}
	none := ok
	none.PageRanges = []goprint.PageRange{{From: 5, To: 6}}
	if err := New().Rasterize(context.Background(), io.Discard, a4PDF(1), none); !errors.Is(err, goprint.ErrInvalid) {
		t.Errorf("no pages: %v", err)
	}
}

// TestPrintThroughIPPEverywhere prints to a raster-only printer of an
// in-process IPP server, addressed by URI; nothing leaves the machine.
func TestPrintThroughIPPEverywhere(t *testing.T) {
	attrs := ipp.Attributes{
		{Name: "document-format-supported", Values: []ipp.Value{ipp.MimeMediaType("application/octet-stream"), ipp.MimeMediaType("image/urf")}},
		{Name: "urf-supported", Values: []ipp.Value{ipp.Keyword("W8"), ipp.Keyword("RS72")}},
	}
	srv := ipptest.NewServer(ipptest.Printer{Name: "Brother", Attrs: attrs})
	t.Cleanup(srv.Close)
	p := goprint.IPPEverywhere(goprint.IPPEverywhereOptions{Rasterizer: New()})
	uri := "ipp" + srv.URL[len("http"):] + "/printers/Brother"
	_, err := goprint.NewClient(p).Print(context.Background(), goprint.PDFBytes("Report", a4PDF(2)), goprint.Settings{Provider: "ipp", Printer: uri})
	if err != nil {
		t.Fatal(err)
	}
	jobs := srv.Jobs()
	if len(jobs) != 1 {
		t.Fatalf("%d jobs", len(jobs))
	}
	if a, _ := jobs[0].Operation.Get("document-format"); a.String() != goprint.FormatURF {
		t.Errorf("document-format %v", a)
	}
	count, pages := decodeURF(t, jobs[0].Document)
	if count != 2 || len(pages) != 2 || pages[0].bpp != 1 {
		t.Errorf("count %d, %d pages", count, len(pages))
	}
}

func imageRGBA(w, h int, gray []byte) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i, g := range gray {
		img.Pix[i*4], img.Pix[i*4+1], img.Pix[i*4+2], img.Pix[i*4+3] = g, g, g, 255
	}
	return img
}
