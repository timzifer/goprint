//go:build darwin

package goprint

import (
	"bytes"
	"context"
	"errors"
	"image"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"testing"

	"github.com/timzifer/goprint/internal/testpdf"
)

// AppKit needs the main thread: keep the main goroutine on it and run the
// tests through RunMain, which serves the main thread while they run.
func init() { runtime.LockOSThread() }

func TestMain(m *testing.M) {
	code := 0
	RunMain(func() { code = m.Run() })
	os.Exit(code)
}

var mediaBoxRE = regexp.MustCompile(`/MediaBox\s*\[\s*([-\d.]+)\s+([-\d.]+)\s+([-\d.]+)\s+([-\d.]+)\s*\]`)

// pdfPages returns the MediaBox size (points) of every page object in a
// PDF written by Quartz, which stores page dictionaries uncompressed.
func pdfPages(t *testing.T, b []byte) [][2]float64 {
	t.Helper()
	var out [][2]float64
	pageRE := regexp.MustCompile(`/Type\s*/Page[^s]`)
	for _, loc := range pageRE.FindAllIndex(b, -1) {
		// The MediaBox belongs to the dictionary around the /Type entry.
		start := bytes.LastIndex(b[:loc[0]], []byte("obj"))
		end := bytes.Index(b[loc[0]:], []byte("endobj"))
		if start < 0 || end < 0 {
			t.Fatalf("cannot find page object around offset %d", loc[0])
		}
		m := mediaBoxRE.FindSubmatch(b[start : loc[0]+end])
		if m == nil {
			t.Fatalf("page object without MediaBox: %q", b[start:loc[0]+end])
		}
		f := func(i int) float64 {
			v, err := strconv.ParseFloat(string(m[i]), 64)
			if err != nil {
				t.Fatal(err)
			}
			return v
		}
		out = append(out, [2]float64{f(3) - f(1), f(4) - f(2)})
	}
	return out
}

// TestMacPrintToFile runs the dialog's print operation (presets, PDFKit
// print operation, read-back) without panel and saves the result as PDF.
func TestMacPrintToFile(t *testing.T) {
	src := testpdf.Generate(5, testpdf.A4Width, testpdf.A4Height)
	doc := Document{Title: "goprint-mac", PDF: func() (io.ReadSeekCloser, error) { return nopCloser{bytes.NewReader(src)}, nil }}
	const a4w, a4h, a5w, a5h, ltrw, ltrh = 595.28, 841.89, 419.53, 595.28, 612, 792

	tests := []struct {
		name     string
		s        Settings
		pages    int
		w, h     float64 // expected MediaBox of every page
		media    Media
		orient   Orientation
		ranges   []PageRange
		warnings int
	}{
		{"a4 portrait", Settings{Media: MediaA4, Orientation: Portrait}, 5, a4w, a4h, MediaA4, Portrait, nil, 0},
		{"a5 landscape", Settings{Media: MediaA5, Orientation: Landscape}, 5, a5h, a5w, MediaA5, Landscape, nil, 0},
		{"a4 landscape", Settings{Media: MediaA4, Orientation: Landscape}, 5, a4h, a4w, MediaA4, Landscape, nil, 0},
		{"letter", Settings{Media: MediaLetter, Orientation: Portrait}, 5, ltrw, ltrh, MediaLetter, Portrait, nil, 0},
		{"page range", Settings{Media: MediaA4, Orientation: Portrait, PageRanges: []PageRange{{2, 3}}},
			2, a4w, a4h, MediaA4, Portrait, []PageRange{{2, 3}}, 0},
		{"open range", Settings{Media: MediaA5, Orientation: Portrait, PageRanges: []PageRange{{4, 0}}},
			2, a5w, a5h, MediaA5, Portrait, []PageRange{{4, 5}}, 0},
		{"single page landscape", Settings{Media: MediaA5, Orientation: Landscape, PageRanges: []PageRange{{5, 5}}},
			1, a5h, a5w, MediaA5, Landscape, []PageRange{{5, 5}}, 0},
		{"several ranges use the first", Settings{Media: MediaA4, Orientation: Portrait, PageRanges: []PageRange{{1, 1}, {3, 4}}},
			1, a4w, a4h, MediaA4, Portrait, []PageRange{{1, 1}}, 1},
		{"scaling none", Settings{Media: MediaA4, Orientation: Portrait, Scaling: ScalingNone, Copies: 2}, 5, a4w, a4h, MediaA4, Portrait, nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out.pdf")
			chosen, warnings, err := macPrintToFile(doc, tt.s, out)
			if err != nil {
				t.Fatalf("macPrintToFile: %v", err)
			}
			if len(warnings) != tt.warnings {
				t.Errorf("warnings = %v, want %d", warnings, tt.warnings)
			}
			b, err := os.ReadFile(out)
			if err != nil {
				t.Fatalf("no output: %v", err)
			}
			pages := pdfPages(t, b)
			if len(pages) != tt.pages {
				t.Errorf("output has %d pages, want %d", len(pages), tt.pages)
			}
			for i, p := range pages {
				if math.Abs(p[0]-tt.w) > 1 || math.Abs(p[1]-tt.h) > 1 {
					t.Errorf("page %d is %.2fx%.2f pt, want %.2fx%.2f", i+1, p[0], p[1], tt.w, tt.h)
				}
			}
			t.Logf("chosen: %+v", chosen)
			if chosen.Media != tt.media {
				t.Errorf("read back media %v, want %v", chosen.Media, tt.media)
			}
			if chosen.Orientation != tt.orient {
				t.Errorf("read back orientation %v, want %v", chosen.Orientation, tt.orient)
			}
			if len(chosen.PageRanges) != len(tt.ranges) || (len(tt.ranges) > 0 && chosen.PageRanges[0] != tt.ranges[0]) {
				t.Errorf("read back page ranges %v, want %v", chosen.PageRanges, tt.ranges)
			}
			if tt.s.Copies > 0 && chosen.Copies != tt.s.Copies {
				t.Errorf("read back copies %d, want %d", chosen.Copies, tt.s.Copies)
			}
		})
	}
}

func TestMacPrintToFileImages(t *testing.T) {
	doc := Document{Images: []image.Image{image.NewGray(image.Rect(0, 0, 100, 50)), image.NewGray(image.Rect(0, 0, 100, 50))}, DPI: 100}
	out := filepath.Join(t.TempDir(), "out.pdf")
	if _, _, err := macPrintToFile(doc, Settings{Media: MediaA4}, out); err != nil {
		t.Fatalf("macPrintToFile: %v", err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(pdfPages(t, b)); n != 2 {
		t.Errorf("output has %d pages, want 2", n)
	}
}

func TestMacPrintToFileErrors(t *testing.T) {
	src := testpdf.Generate(2, 200, 200)
	doc := Document{PDF: func() (io.ReadSeekCloser, error) { return nopCloser{bytes.NewReader(src)}, nil }}
	out := filepath.Join(t.TempDir(), "out.pdf")
	if _, _, err := macPrintToFile(doc, Settings{PageRanges: []PageRange{{3, 4}}}, out); !errors.Is(err, ErrInvalid) {
		t.Errorf("range beyond document = %v, want ErrInvalid", err)
	}
	if _, _, err := macPrintToFile(doc, Settings{Printer: "goprint-no-such-printer"}, out); !errors.Is(err, ErrPrinterNotFound) {
		t.Errorf("unknown printer = %v, want ErrPrinterNotFound", err)
	}
	bad := Document{PDF: func() (io.ReadSeekCloser, error) { return nopCloser{bytes.NewReader([]byte("not a pdf"))}, nil }}
	if _, _, err := macPrintToFile(bad, Settings{}, out); !errors.Is(err, ErrInvalid) {
		t.Errorf("garbage PDF = %v, want ErrInvalid", err)
	}
}

// TestMacDialogStrict checks that Strict fails before any panel is shown.
func TestMacDialogStrict(t *testing.T) {
	src := testpdf.Generate(1, 200, 200)
	doc := Document{PDF: func() (io.ReadSeekCloser, error) { return nopCloser{bytes.NewReader(src)}, nil }}
	_, _, err := Dialog(context.Background(), doc, DialogOptions{Settings: Settings{Scaling: ScalingFill, Strict: true}})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Dialog(strict) = %v, want ErrUnsupported", err)
	}
	_, _, err = Dialog(context.Background(), doc, DialogOptions{
		Settings: Settings{Printer: "ipp://printer.local/ipp/print"}, RequirePrinter: true})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Dialog(RequirePrinter, URI) = %v, want ErrUnsupported", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := Dialog(ctx, doc, DialogOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Dialog(canceled ctx) = %v, want context.Canceled", err)
	}
}

func TestMacCapabilitiesDialogPreview(t *testing.T) {
	if os.Getenv("GOPRINT_CUPS") == "" {
		t.Skip("set GOPRINT_CUPS=1 to test against the local CUPS")
	}
	ps, err := Printers(context.Background())
	if err != nil {
		t.Fatalf("Printers: %v", err)
	}
	for _, p := range ps {
		if !p.Caps.DialogPreview {
			t.Errorf("%s: DialogPreview = false", p.Name)
		}
	}
}
