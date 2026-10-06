//go:build windows

package winprint

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/timzifer/goprint/internal/errdefs"
	"github.com/timzifer/goprint/internal/testpdf"
)

func TestDevNames(t *testing.T) {
	b := devNames("Microsoft Print to PDF", "PORTPROMPT:")
	if got := deviceFromDevNames(b); got != "Microsoft Print to PDF" {
		t.Fatalf("device = %q", got)
	}
	h, err := globalFrom(b)
	if err != nil {
		t.Fatal(err)
	}
	defer globalFree(h)
	back, err := globalBytes(h)
	if err != nil || !bytes.Equal(back, b) {
		t.Fatalf("global round trip: %v", err)
	}
}

func TestReadDevModeRoundTrip(t *testing.T) {
	requirePrinter(t, pdfPrinter)
	yes := true
	in := JobSettings{Copies: 3, Collate: &yes, PaperWidth: 148000, PaperHeight: 210000, Orientation: dmOrientLandscape}
	dm, warns, err := BuildDevMode(pdfPrinter, in)
	if err != nil {
		t.Fatal(err)
	}
	got := readDevMode(pdfPrinter, dm)
	if got.Copies != 3 || got.Orientation != dmOrientLandscape || abs(got.PaperWidth-148000) > 1000 || abs(got.PaperHeight-210000) > 1000 {
		t.Errorf("read back %+v (warnings %v)", got, warns)
	}
}

// TestClassicDialogInteractive opens PrintDlgExW preset to the PDF printer.
// Set GOPRINT_DIALOG=classic (settings only) or classic-print.
func TestClassicDialogInteractive(t *testing.T) {
	mode := os.Getenv("GOPRINT_DIALOG")
	if mode != "classic" && mode != "classic-print" {
		t.Skip("set GOPRINT_DIALOG=classic or classic-print")
	}
	requirePrinter(t, pdfPrinter)
	Tracef = t.Logf
	defer func() { Tracef = nil }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	res, err := ClassicDialog(ctx, bytes.NewReader(testpdf.Generate(3, testpdf.A4Width, testpdf.A4Height)), ClassicOptions{
		Title:      "goprint classic test",
		Printer:    pdfPrinter,
		Settings:   JobSettings{Copies: 2, Orientation: dmOrientLandscape, PaperWidth: 148000, PaperHeight: 210000},
		PageRanges: nil,
		PrintNow:   mode == "classic-print",
	})
	if errors.Is(err, errdefs.ErrCanceled) {
		t.Log("canceled")
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("printer %q, chosen %+v, ranges %v, warnings %v", res.Printer, res.Chosen, res.PageRanges, res.Warnings)
	if res.Job != nil {
		if err := res.Job.Wait(ctx); err != nil {
			t.Fatal(err)
		}
		t.Logf("job %s", res.Job.ID())
	}
}
