//go:build windows

package goprint

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"io"
	"os"
	"path/filepath"
	"regexp"
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
	s.Vendor = map[string]string{VendorOutputFile: out}
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
	if n := countPages(out); n != 2 {
		t.Errorf("printed %d pages, want 2", n)
	}
	if w := job.Warnings(); len(w) != 1 || w[0].Setting != "Copies" {
		t.Errorf("warnings = %v, want one Copies warning", w)
	}
}

func TestWindowsStrict(t *testing.T) {
	src := testpdf.Generate(1, 100, 100)
	doc := Document{PDF: func() (io.ReadSeekCloser, error) { return nopCloser{bytes.NewReader(src)}, nil }}
	if _, err := Print(context.Background(), doc, Settings{Duplex: DuplexLongEdge, Strict: true}); err == nil {
		t.Fatal("strict print with unsupported setting succeeded")
	}
}
