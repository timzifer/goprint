//go:build windows

package winprint

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/timzifer/goprint/internal/errdefs"
	"github.com/timzifer/goprint/internal/testpdf"
)

const pdfPrinter = "Microsoft Print to PDF"

func requirePrinter(t *testing.T, name string) {
	t.Helper()
	ps, err := Printers()
	if err != nil {
		t.Fatalf("Printers: %v", err)
	}
	for _, p := range ps {
		if p.Name == name {
			return
		}
	}
	t.Skipf("printer %q not installed", name)
}

func TestPrinters(t *testing.T) {
	ps, err := Printers()
	if err != nil {
		t.Fatal(err)
	}
	defaults := 0
	for _, p := range ps {
		if p.Name == "" {
			t.Error("printer without name")
		}
		if p.Default {
			defaults++
		}
		t.Logf("%+v", p)
	}
	if defaults > 1 {
		t.Errorf("%d default printers", defaults)
	}
}

func TestPrintToPDF(t *testing.T) {
	requirePrinter(t, pdfPrinter)
	out := filepath.Join(t.TempDir(), "out.pdf")
	if dir := os.Getenv("GOPRINT_KEEP_OUTPUT"); dir != "" {
		out = filepath.Join(dir, "TestPrintToPDF.pdf")
		_ = os.Remove(out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	Tracef = t.Logf
	defer func() { Tracef = nil }()
	const pages = 3
	src := bytes.NewReader(testpdf.Generate(pages, testpdf.A4Width, testpdf.A4Height))
	job, err := Print(ctx, src, Options{Printer: pdfPrinter, Title: "goprint test", OutputFile: out})
	if err != nil {
		t.Fatalf("Print: %v", err)
	}
	if err := job.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	t.Logf("job id %q", job.ID())

	var data []byte
	for ctx.Err() == nil { // the spooler may still be flushing the file
		if data, err = os.ReadFile(out); err == nil && bytes.Contains(data, []byte("%%EOF")) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		t.Fatalf("output is not a PDF (%d bytes): %q", len(data), data[:min(len(data), 32)])
	}
	if n := len(regexp.MustCompile(`/Type\s*/Page[^s]`).FindAll(data, -1)); n != pages {
		t.Errorf("output has %d pages, want %d", n, pages)
	}
}

func TestPrintUnknownPrinter(t *testing.T) {
	_, err := Print(context.Background(), bytes.NewReader(testpdf.Generate(1, 100, 100)), Options{Printer: "goprint-does-not-exist"})
	if !errors.Is(err, errdefs.ErrPrinterNotFound) {
		t.Fatalf("err = %v, want ErrPrinterNotFound", err)
	}
}

func TestPrintInvalidPDF(t *testing.T) {
	requirePrinter(t, pdfPrinter)
	_, err := Print(context.Background(), bytes.NewReader([]byte("not a pdf")), Options{Printer: pdfPrinter, OutputFile: filepath.Join(t.TempDir(), "x.pdf")})
	if err == nil {
		t.Fatal("Print(invalid PDF) succeeded")
	}
	t.Log(err)
}

func TestCapabilitiesPDFPrinter(t *testing.T) {
	requirePrinter(t, pdfPrinter)
	c, err := Capabilities(pdfPrinter)
	if err != nil {
		t.Fatal(err)
	}
	var a4 bool
	for _, p := range c.Papers {
		if p.ID == 9 && abs(p.Width-210000) < 1000 && abs(p.Height-297000) < 1000 {
			a4 = true
		}
	}
	if !a4 {
		t.Errorf("no A4 in %+v", c.Papers)
	}
	t.Logf("%d papers, bins %v, duplex %v, color %v, res %v, copies %d, max custom %dx%d",
		len(c.Papers), c.Bins, c.Duplex, c.Color, c.Resolutions, c.MaxCopies, c.MaxCustomWidth, c.MaxCustomHeight)
}

// TestPrintSettingsToPDF checks that paper size and orientation reach the
// driver: "Microsoft Print to PDF" writes them into the MediaBox.
func TestPrintSettingsToPDF(t *testing.T) {
	requirePrinter(t, pdfPrinter)
	Tracef = t.Logf
	defer func() { Tracef = nil }()
	for _, tc := range []struct {
		name string
		s    JobSettings
		w, h float64 // expected MediaBox in points
	}{
		{"A5 portrait", JobSettings{PaperWidth: 148000, PaperHeight: 210000, Orientation: dmOrientPortrait}, 419.5, 595.3},
		{"A4 landscape", JobSettings{PaperWidth: 210000, PaperHeight: 297000, Orientation: dmOrientLandscape}, 841.9, 595.3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out.pdf")
			if dir := os.Getenv("GOPRINT_KEEP_OUTPUT"); dir != "" {
				out = filepath.Join(dir, strings.ReplaceAll(tc.name, " ", "_")+".pdf")
				_ = os.Remove(out)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			src := bytes.NewReader(testpdf.Generate(1, testpdf.A4Width, testpdf.A4Height))
			job, err := Print(ctx, src, Options{Printer: pdfPrinter, Title: "settings", OutputFile: out, Settings: tc.s})
			if err != nil {
				t.Fatal(err)
			}
			if w := job.Warnings(); len(w) > 0 {
				t.Errorf("warnings %v", w)
			}
			if err := job.Wait(ctx); err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(out)
			m := regexp.MustCompile(`/MediaBox\s*\[\s*0(?:\.0)?\s+0(?:\.0)?\s+([\d.]+)\s+([\d.]+)\s*\]`).FindSubmatch(data)
			if m == nil {
				t.Fatalf("no MediaBox in output (%d bytes)", len(data))
			}
			w, _ := strconv.ParseFloat(string(m[1]), 64)
			h, _ := strconv.ParseFloat(string(m[2]), 64)
			if math.Abs(w-tc.w) > 2 || math.Abs(h-tc.h) > 2 {
				t.Errorf("MediaBox %.1fx%.1f, want %.1fx%.1f", w, h, tc.w, tc.h)
			}
		})
	}
}

func TestMain(m *testing.M) {
	// Never print on a real device from tests. The interactive dialog test
	// in print mode needs the user's choice, which the guard cannot see.
	if os.Getenv("GOPRINT_DIALOG") != "print" {
		PrinterGuard = func(printer string) error {
			if printer != pdfPrinter && !strings.HasPrefix(printer, "goprint-") {
				return fmt.Errorf("test tried to print to %q; only %q is allowed", printer, pdfPrinter)
			}
			return nil
		}
	}
	os.Exit(m.Run())
}

// TestPrinterGuardBlocksDefault makes sure the test guard stops a job aimed
// at the default (possibly real) printer before anything is spooled.
func TestPrinterGuardBlocksDefault(t *testing.T) {
	if PrinterGuard == nil {
		t.Skip("guard disabled")
	}
	if def, err := DefaultPrinter(); err != nil || def == pdfPrinter {
		t.Skip("default printer is the PDF printer or unknown")
	}
	_, err := Print(context.Background(), bytes.NewReader(testpdf.Generate(1, 100, 100)), Options{})
	if err == nil || !strings.Contains(err.Error(), "only") {
		t.Fatalf("Print to default printer = %v, want guard error", err)
	}
}
