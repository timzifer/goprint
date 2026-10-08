//go:build windows

package winprint

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/timzifer/goprint/internal/com"
	"github.com/timzifer/goprint/internal/errdefs"
	"github.com/timzifer/goprint/internal/testpdf"
)

// TestDialogInteractive opens the modern print dialog. It needs a desktop
// session and someone (or a script) to close it; set GOPRINT_DIALOG=1.
// GOPRINT_DIALOG=print prints (PrintNow) with whatever printer is chosen.
func TestDialogInteractive(t *testing.T) {
	mode := os.Getenv("GOPRINT_DIALOG")
	if mode == "" {
		t.Skip("set GOPRINT_DIALOG=1 for the interactive dialog test")
	}
	Tracef = t.Logf
	if os.Getenv("GOPRINT_TRACE_QI") != "" {
		com.TraceQI = func(this uintptr, iid com.GUID) { t.Logf("QI miss %x: %v", this, iid) }
	}
	defer func() { Tracef, com.TraceQI = nil, nil }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	src := bytes.NewReader(testpdf.Generate(3, testpdf.A4Width, testpdf.A4Height))
	res, err := Dialog(ctx, src, DialogOptions{
		Title:    "goprint dialog test",
		Presets:  TaskOptions{Copies: 2, Orientation: 3 /* portrait */},
		PrintNow: mode == "print",
	})
	if errors.Is(err, errdefs.ErrCanceled) {
		t.Log("canceled by user")
		return
	}
	if err != nil {
		t.Fatalf("Dialog: %v", err)
	}
	t.Logf("chosen: %+v", res.Chosen)
	if res.Job != nil {
		if err := res.Job.Wait(ctx); err != nil {
			t.Fatalf("Wait: %v", err)
		}
		t.Logf("job %s on %q", res.Job.ID(), res.Job.Printer())
	}
}

// TestPropertiesInteractive opens the driver dialog of the PDF printer,
// preset to A5 landscape, and logs what the user confirms.
func TestPropertiesInteractive(t *testing.T) {
	if os.Getenv("GOPRINT_DIALOG") == "" {
		t.Skip("set GOPRINT_DIALOG=1 for the interactive dialog test")
	}
	requirePrinter(t, pdfPrinter)
	Tracef = t.Logf
	defer func() { Tracef = nil }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	res, err := PropertiesDialog(ctx, 0, pdfPrinter, nil, a5Landscape)
	if errors.Is(err, errdefs.ErrCanceled) {
		t.Log("canceled by user")
		return
	}
	if err != nil {
		t.Fatalf("PropertiesDialog: %v", err)
	}
	t.Logf("printer %q, %d bytes DEVMODE, chosen %+v, warnings %v", res.Printer, len(res.DevMode), res.Chosen, res.Warnings)
}
