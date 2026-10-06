//go:build windows

package winprint

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
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
