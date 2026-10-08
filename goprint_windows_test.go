//go:build windows

package goprint

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/timzifer/goprint/internal/testpdf"
	"github.com/timzifer/goprint/internal/winprint"
)

const pdfPrinter = "Microsoft Print to PDF"

func requirePDFPrinter(t *testing.T) {
	t.Helper()
	ps, err := Printers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ps {
		if p.Name == pdfPrinter {
			return
		}
	}
	t.Skipf("%q not installed", pdfPrinter)
}

func printToFile(t *testing.T, doc Document, s Settings) (*Job, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out := filepath.Join(t.TempDir(), "out.pdf")
	winprint.Tracef = t.Logf
	defer func() { winprint.Tracef = nil }()
	s.Printer = pdfPrinter
	s.Vendor = maps.Clone(s.Vendor)
	if s.Vendor == nil {
		s.Vendor = map[string]string{}
	}
	s.Vendor[VendorOutputFile] = out
	job, err := Print(ctx, doc, s)
	if err != nil {
		t.Fatalf("Print: %v", err)
	}
	if err := job.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	var data []byte
	for ctx.Err() == nil {
		if data, err = os.ReadFile(out); err == nil && bytes.Contains(data, []byte("%%EOF")) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	return job, data
}

func countPages(b []byte) int { return len(regexp.MustCompile(`/Type\s*/Page[^s]`).FindAll(b, -1)) }

func TestWindowsPrintPDFPageRanges(t *testing.T) {
	requirePDFPrinter(t)
	src := testpdf.Generate(5, testpdf.A4Width, testpdf.A4Height)
	doc := Document{Title: "ranges", PDF: func() (io.ReadSeekCloser, error) { return nopCloser{bytes.NewReader(src)}, nil }}
	job, out := printToFile(t, doc, Settings{PageRanges: []PageRange{{2, 3}, {5, 9}}})
	if n := countPages(out); n != 3 {
		t.Errorf("printed %d pages, want 3", n)
	}
	if w := job.Warnings(); len(w) != 1 || w[0].Setting != "PageRanges" {
		t.Errorf("warnings = %v, want one PageRanges warning", w)
	}
	if job.ID() == "" {
		t.Error("empty job id")
	}
}

func TestWindowsPrintImages(t *testing.T) {
	requirePDFPrinter(t)
	img := image.NewNRGBA(image.Rect(0, 0, 400, 200))
	for x := 0; x < 400; x++ {
		img.Set(x, 100, color.Black)
	}
	job, out := printToFile(t, Document{Images: []image.Image{img, img}, DPI: 100}, Settings{Copies: 2})
	if n := countPages(out); n != 4 {
		t.Errorf("printed %d pages, want 4 (2 copies of 2 pages)", n)
	}
	if w := job.Warnings(); len(w) != 0 {
		t.Errorf("warnings = %v", w)
	}
}

func TestWindowsStrict(t *testing.T) {
	requirePDFPrinter(t)
	src := testpdf.Generate(1, 100, 100)
	doc := Document{PDF: func() (io.ReadSeekCloser, error) { return nopCloser{bytes.NewReader(src)}, nil }}
	// "Microsoft Print to PDF" cannot print two-sided: Strict must fail
	// before anything is spooled.
	_, err := Print(context.Background(), doc, Settings{Printer: pdfPrinter, Duplex: DuplexLongEdge, Strict: true})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("strict print = %v, want ErrUnsupported", err)
	}
}

// TestWindowsVendorDevMode prints from a DEVMODE passed in Settings.Vendor,
// as PrinterProperties returns it.
func TestWindowsVendorDevMode(t *testing.T) {
	requirePDFPrinter(t)
	dm, _, err := winprint.BuildDevMode(pdfPrinter, winprint.JobSettings{PaperWidth: 148000, PaperHeight: 210000, Orientation: dmOrientLandscape})
	if err != nil {
		t.Fatal(err)
	}
	doc := PDFBytes("devmode", testpdf.Generate(1, testpdf.A4Width, testpdf.A4Height))
	job, data := printToFile(t, doc, withDevMode(Settings{}, dm))
	if w := job.Warnings(); len(w) > 0 {
		t.Errorf("warnings = %v", w)
	}
	m := regexp.MustCompile(`/MediaBox\s*\[\s*0(?:\.0)?\s+0(?:\.0)?\s+([\d.]+)\s+([\d.]+)`).FindSubmatch(data)
	if m == nil || !strings.HasPrefix(string(m[1]), "595") || !strings.HasPrefix(string(m[2]), "419") {
		t.Errorf("MediaBox %q, want A5 landscape (595 x 419)", m)
	}

	job, _ = printToFile(t, doc, Settings{Vendor: map[string]string{VendorDevMode: "not base64!"}})
	if w := job.Warnings(); len(w) != 1 || w[0].Setting != "Vendor["+VendorDevMode+"]" {
		t.Errorf("invalid DEVMODE: warnings = %v", w)
	}
}

func TestWindowsDriverDialog(t *testing.T) {
	requirePDFPrinter(t)
	c, err := GetCapabilities(context.Background(), pdfPrinter)
	if err != nil {
		t.Fatal(err)
	}
	if !c.DriverDialog {
		t.Error("DriverDialog = false")
	}
	// No UI: IPP printers have no driver dialog.
	if _, err := PrinterProperties(context.Background(), Settings{Printer: "ipp://localhost/ipp/print"}, 0); !errors.Is(err, ErrUnsupported) {
		t.Errorf("PrinterProperties(ipp) = %v, want ErrUnsupported", err)
	}
}

func TestMain(m *testing.M) {
	// Never print on a real device from tests.
	winprint.PrinterGuard = func(printer string) error {
		if printer != pdfPrinter && !strings.HasPrefix(printer, "goprint-") {
			return fmt.Errorf("test tried to print to %q; only %q is allowed", printer, pdfPrinter)
		}
		return nil
	}
	os.Exit(m.Run())
}
