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

`go run ./example` starts a small app that prints a generated PDF or a capture
of its own window. Requires Fyne ≥ 2.6 and cgo (Fyne's driver).
