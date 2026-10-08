package fyneprint_test

import (
	"errors"
	"log"

	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/widget"

	"github.com/timzifer/goprint"
	"github.com/timzifer/goprint/fyneprint"
)

func ExampleShowDialog() {
	a := app.New()
	w := a.NewWindow("Report")
	w.SetContent(widget.NewButton("Print…", func() {
		// Runs on the UI goroutine; ShowDialog does not block it.
		fyneprint.ShowDialog(w, goprint.PDFFile("report.pdf"), goprint.DialogOptions{PrintNow: true},
			func(job *goprint.Job, s goprint.Settings, err error) {
				// Back on the UI goroutine.
				if err != nil && !errors.Is(err, goprint.ErrCanceled) {
					log.Println("print:", err) // or dialog.ShowError(err, w)
				}
			})
	}))
	w.ShowAndRun()
}
