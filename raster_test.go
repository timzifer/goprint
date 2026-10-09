package goprint

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/timzifer/goprint/ipp"
	"github.com/timzifer/goprint/ipp/ipptest"
)

func kw(name string, vs ...string) ipp.Attribute {
	a := ipp.Attribute{Name: name}
	for _, v := range vs {
		a.Values = append(a.Values, ipp.Keyword(v))
	}
	return a
}

func mime(vs ...string) ipp.Attribute {
	a := ipp.Attribute{Name: "document-format-supported"}
	for _, v := range vs {
		a.Values = append(a.Values, ipp.MimeMediaType(v))
	}
	return a
}

func dpi(vs ...int32) ipp.Attribute {
	a := ipp.Attribute{Name: "pwg-raster-document-resolution-supported"}
	for _, v := range vs {
		a.Values = append(a.Values, ipp.Resolution{X: v, Y: v, Units: ipp.UnitsDPI})
	}
	return a
}

var pwgPrinter = ipp.Attributes{
	mime("application/octet-stream", "image/urf", "image/pwg-raster"),
	dpi(600, 300),
	kw("pwg-raster-document-type-supported", "sgray_8", "srgb_8"),
	kw("pwg-raster-document-sheet-back", "normal"),
	kw("media-default", "iso_a4_210x297mm"),
	kw("urf-supported", "RS600", "W8"),
}

var urfPrinter = ipp.Attributes{
	mime("application/octet-stream", "image/urf"),
	kw("urf-supported", "V1.4", "CP1", "W8", "RS300-600-1200", "DM3"),
}

func TestChooseRaster(t *testing.T) {
	ranges := []PageRange{{From: 2, To: 3}}
	f, w, err := chooseRaster(pwgPrinter, Settings{PageRanges: ranges, Duplex: DuplexShortEdge, Orientation: Landscape})
	if err != nil || len(w) != 0 {
		t.Fatalf("PWG: %v %v", err, w)
	}
	want := RasterFormat{Type: FormatPWGRaster, Resolution: Resolution{300, 300}, Color: true, Media: MediaA4,
		Orientation: Landscape, Duplex: DuplexShortEdge, PageRanges: ranges}
	if !reflect.DeepEqual(f, want) {
		t.Errorf("PWG:\n got %+v\nwant %+v", f, want)
	}

	f, _, _ = chooseRaster(pwgPrinter, Settings{Color: Monochrome, Quality: QualityHigh, Media: MediaLetter})
	if f.Color || f.Resolution.X != 600 || f.Media != MediaLetter {
		t.Errorf("PWG gray high: %+v", f)
	}

	f, w, err = chooseRaster(urfPrinter, Settings{Duplex: DuplexLongEdge, Color: Color, Quality: QualityDraft})
	if err != nil || f.Type != FormatURF || f.Color || f.Resolution.X != 300 || f.Duplex != DuplexDefault {
		t.Errorf("URF: %+v, %v", f, err)
	}
	var got []string
	for _, x := range w {
		got = append(got, x.Setting)
	}
	if strings.Join(got, ",") != "Color,Duplex" {
		t.Errorf("URF warnings %v", w)
	}

	if _, _, err := chooseRaster(ipp.Attributes{mime("application/postscript")}, Settings{}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("PostScript only: %v", err)
	}
	if _, _, err := chooseRaster(ipp.Attributes{mime("image/urf"), kw("urf-supported", "W8")}, Settings{}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("no resolution: %v", err)
	}
}

func TestPickResolution(t *testing.T) {
	for _, tc := range []struct {
		rs   []int
		q    Quality
		want int
	}{
		{[]int{600, 300, 150}, QualityDefault, 300},
		{[]int{600, 1200}, QualityNormal, 600},
		{[]int{150, 200}, QualityDefault, 200},
		{[]int{150, 600}, QualityDraft, 150},
		{[]int{150, 600}, QualityHigh, 600},
	} {
		if got := pickResolution(tc.rs, tc.q); got != tc.want {
			t.Errorf("pickResolution(%v, %v) = %d, want %d", tc.rs, tc.q, got, tc.want)
		}
	}
}

// fakeRasterizer writes the format it got, so tests can check it arrived.
type fakeRasterizer struct {
	got RasterFormat
	pdf []byte
	err error
}

func (r *fakeRasterizer) Rasterize(_ context.Context, w io.Writer, pdf []byte, f RasterFormat) error {
	r.got, r.pdf = f, pdf
	if r.err != nil {
		return r.err
	}
	_, err := io.WriteString(w, "RaS2-fake")
	return err
}

func TestIPPEverywhereRasterizes(t *testing.T) {
	p, srv, _ := networkMock(t, ipptest.Printer{Name: "Raster", Attrs: pwgPrinter})
	rz := &fakeRasterizer{}
	p.opts.Rasterizer = rz
	ranges := []PageRange{{From: 2, To: 2}}
	job, err := NewClient(p).Print(context.Background(), pdfDoc("x"), Settings{Provider: "ipp", Printer: "Raster", PageRanges: ranges, Copies: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(rz.pdf, []byte("%PDF")) || rz.got.Type != FormatPWGRaster || !reflect.DeepEqual(rz.got.PageRanges, ranges) {
		t.Errorf("rasterizer got %+v, pdf %.8q", rz.got, rz.pdf)
	}
	jobs := srv.Jobs()
	if len(jobs) != 1 || string(jobs[0].Document) != "RaS2-fake" {
		t.Fatalf("server jobs %+v", jobs)
	}
	if a, _ := jobs[0].Operation.Get("document-format"); a.String() != FormatPWGRaster {
		t.Errorf("document-format %v", a)
	}
	if _, ok := jobs[0].Attrs.Get("page-ranges"); ok {
		t.Error("page-ranges sent with raster pages already selected")
	}
	if a, _ := jobs[0].Attrs.Get("copies"); a.String() != "2" {
		t.Errorf("copies %v", a)
	}
	if len(job.Warnings()) != 0 {
		t.Errorf("warnings %v", job.Warnings())
	}
}

func TestIPPEverywhereRasterFailures(t *testing.T) {
	p, srv, _ := networkMock(t, ipptest.Printer{Name: "Raster", Attrs: urfPrinter})
	c := NewClient(p)
	s := Settings{Provider: "ipp", Printer: "Raster"}

	// Without a rasterizer.
	if _, err := c.Print(context.Background(), pdfDoc("x"), s); !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "goprint/raster") {
		t.Errorf("no rasterizer: %v", err)
	}
	// Strict with a setting the printer cannot keep (duplex with DM3).
	p.opts.Rasterizer = &fakeRasterizer{}
	strict := s
	strict.Strict, strict.Duplex = true, DuplexLongEdge
	if _, err := c.Print(context.Background(), pdfDoc("x"), strict); !errors.Is(err, ErrUnsupported) {
		t.Errorf("strict: %v", err)
	}
	// A failing rasterizer fails the job.
	p.opts.Rasterizer = &fakeRasterizer{err: errors.New("render failed")}
	if _, err := c.Print(context.Background(), pdfDoc("x"), s); err == nil {
		t.Error("render failure not reported")
	}
	for _, j := range srv.Jobs() {
		if len(j.Document) > 0 {
			t.Errorf("job with document %q sent", j.Document)
		}
	}
}
