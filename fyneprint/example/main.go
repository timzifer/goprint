// Command example is a small Fyne app that prints through fyneprint: a PDF
// (generated, or -pdf file) or a capture of its own window.
//
//	go run ./example [-pdf file.pdf]
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"image"
	"io"
	"os"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/timzifer/goprint"
	"github.com/timzifer/goprint/fyneprint"
	"github.com/timzifer/goprint/internal/testpdf"
)

func main() {
	pdfPath := flag.String("pdf", "", "PDF to print (default: a generated 3-page A4 document)")
	flag.Parse()

	a := app.New()
	w := a.NewWindow("fyneprint example")
	status := widget.NewLabel("Ready.")
	status.Wrapping = fyne.TextWrapWord
	printNow := widget.NewCheck("Print after confirming (off: only return the settings; no preview on Windows)", nil)

	report := func(job *goprint.Job, s goprint.Settings, err error) {
		switch {
		case errors.Is(err, goprint.ErrCanceled):
			status.SetText("Canceled.")
		case err != nil:
			status.SetText("Error: " + err.Error())
		case job != nil:
			status.SetText(fmt.Sprintf("Printing on %q, job %q, %d copies.", s.Printer, job.ID(), s.Copies))
		default:
			status.SetText(fmt.Sprintf("Chosen: printer %q, copies %d, media %s, %s, %s.",
				s.Printer, s.Copies, s.Media.Name, s.Orientation, s.Duplex))
		}
	}

	pdfDoc := goprint.Document{Title: "fyneprint example", PDF: func() (io.ReadSeekCloser, error) {
		if *pdfPath != "" {
			return os.Open(*pdfPath)
		}
		return nopCloser{bytes.NewReader(testpdf.Generate(3, testpdf.A4Width, testpdf.A4Height))}, nil
	}}

	w.SetContent(container.NewVBox(
		widget.NewLabel("Opens the platform's print dialog owned by this window."),
		printNow,
		widget.NewButton("Print PDF…", func() {
			status.SetText("Dialog open…")
			fyneprint.ShowDialog(w, pdfDoc, goprint.DialogOptions{PrintNow: printNow.Checked}, report)
		}),
		widget.NewButton("Print this window…", func() {
			img := w.Canvas().Capture()
			doc := goprint.Document{
				Title:  "fyneprint window",
				Images: []image.Image{img},
				DPI:    int(96 * w.Canvas().Scale()),
			}
			status.SetText("Dialog open…")
			fyneprint.ShowDialog(w, doc, goprint.DialogOptions{PrintNow: printNow.Checked}, report)
		}),
		status,
	))
	w.Resize(fyne.NewSize(480, 260))
	w.ShowAndRun()
}

type nopCloser struct{ *bytes.Reader }

func (nopCloser) Close() error { return nil }
