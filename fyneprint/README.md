# fyneprint

[![Go Reference](https://pkg.go.dev/badge/github.com/timzifer/goprint/fyneprint.svg)](https://pkg.go.dev/github.com/timzifer/goprint/fyneprint)

Print dialogs from [Fyne](https://fyne.io) apps with
[goprint](https://github.com/timzifer/goprint). Separate module, so goprint
itself has no Fyne dependency.

```sh
go get github.com/timzifer/goprint/fyneprint
```

```go
btn := widget.NewButton("Print…", func() {
	fyneprint.ShowDialog(w, goprint.PDFFile("report.pdf"), goprint.DialogOptions{PrintNow: true},
		func(job *goprint.Job, s goprint.Settings, err error) {
			// called on the UI goroutine when the dialog closes
		})
})
```

- **Main thread:** importing the package installs `fyne.DoAndWait` as goprint's
  main-thread runner, which macOS's print panel needs. `goprint.RunMain` is not
  used in Fyne apps.
- **Owner:** the dialog belongs to the Fyne window (HWND, NSWindow, X11). The
  window is disabled while the dialog is open. Wayland has no owner yet
  ([#16](https://github.com/timzifer/goprint/issues/16)).
- **`ShowDialog`** does not block the event loop. **`Dialog`** blocks and must not run
  on the UI goroutine; on Windows it returns `goprint.ErrWrongThread` there
  instead of deadlocking.

## Fyne's own print dialog

`ShowPrintDialog` draws the dialog in Fyne instead of opening the platform's
one, and prints headless through a `goprint.Client`. It looks and behaves the
same on every platform and can always preselect the printer, which the
Windows 11 dialog cannot.

```go
fyneprint.ShowPrintDialog(w, goprint.PDFFile("report.pdf"),
	fyneprint.PrintDialogOptions{PrintNow: true, Settings: goprint.Settings{Printer: "Office"}},
	func(job *goprint.Job, s goprint.Settings, err error) {
		switch {
		case errors.Is(err, fyneprint.ErrSavedAsPDF): // saved instead of printed
		case errors.Is(err, goprint.ErrCanceled):
		case err != nil:
			dialog.ShowError(err, w)
		}
	})
```

- **Settings:** printer, copies and collation, page ranges, paper size,
  orientation, scaling; two-sided, color, quality and paper source where the
  printer reports them. Other presets (vendor values) are passed through.
- **Preview** of each sheet as it comes out of the printer: page ranges, paper,
  orientation and scaling applied. Rendered with
  [fyne-pdf](https://github.com/timzifer/fyne-pdf).
- **Save as PDF** writes the selected pages, turned to the chosen orientation,
  instead of printing (`SaveLabel`, `SavePDF`, `NoSave`). Printers that write
  files ("Microsoft Print to PDF", CUPS-PDF, …) are hidden unless
  `ShowFilePrinters` is set. `NoFileOutput` keeps the dialog away from the
  file system: no file printers at all, and no save button unless `SavePDF`
  hands the PDF to the app. Pages are copied as they are, not re-rendered
  ([cera](https://github.com/timzifer/cera)'s `pdfedit`); encrypted files
  whose permissions forbid reassembling them cannot be saved in parts.
- **Accessibility:** the dialog is drawn by Fyne. It works with the keyboard
  (Tab moves between the controls), but Fyne has no screen reader support yet. Use
  `ShowDialog` where that matters.
- **Languages:** English and German built in, following the system language
  through Fyne's `lang` package. Apps add more with `lang.AddTranslations…`,
  using the keys in [translations/fyneprint.de.json](translations/fyneprint.de.json).
- **Own localization:** apps that switch language themselves supply the texts
  with `PrintDialogOptions.Translate` or, for all dialogs, `SetTranslator`.
  Every text goes through it, OK and Cancel, option names and input errors
  included. An empty result keeps the built-in text. `TranslationKeys` lists
  all keys with their English texts (for a completeness test), `RenderText`
  fills placeholders like `{{.Name}}`, and `RefreshTexts` redraws open dialogs
  after a language switch.

  ```go
  fyneprint.SetTranslator(func(key, fallback string, data any) string {
  	return fyneprint.RenderText(myLocalizer.Text(key, fallback), data)
  })
  // after the operator switched the language:
  fyneprint.RefreshTexts()
  ```
- **Providers:** `PrintDialogOptions.Client` (default `goprint.Default`) lists
  the printers and prints. With several providers, e.g. simulated printers of
  `goprint/virtualprinter`, the list shows all of them, labeled with their
  provider, and `Settings.Provider` with `Settings.Printer` preselects one.
- **Driver options:** for printers with a driver dialog (Windows) a
  "Properties…" button opens it (`goprint.PrinterProperties`). The choices
  there, finishing and other vendor features included, are shown in the
  dialog where it has a control for them and printed along.

`go run ./example` starts a small app that prints a generated PDF or a capture
of its own window, with the platform's dialog or Fyne's own. Requires Fyne ≥ 2.6,
Go ≥ 1.26.4 and cgo (Fyne's driver).
