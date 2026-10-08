// Package fyneprint connects goprint to Fyne apps.
//
// Fyne's event loop owns the process's main thread, so [goprint.RunMain]
// cannot be used. Importing this package installs fyne.DoAndWait as
// goprint's main-thread runner (see [goprint.SetMainThreadRunner]); the
// print panel on macOS then works from any goroutine of a running app.
//
// From a widget callback use [ShowDialog]: it runs the dialog without
// blocking Fyne's event loop and reports the result back on the UI
// goroutine.
//
//	btn := widget.NewButton("Print…", func() {
//		fyneprint.ShowDialog(w, goprint.PDFFile("report.pdf"), goprint.DialogOptions{PrintNow: true},
//			func(job *goprint.Job, s goprint.Settings, err error) {
//				if err != nil && !errors.Is(err, goprint.ErrCanceled) {
//					dialog.ShowError(err, w)
//				}
//			})
//	})
//
// [ShowPrintDialog] is an alternative to the platform's dialog: a print
// dialog drawn by Fyne, with a preview and a "Save as PDF" button, that
// prints through goprint's headless [goprint.Print].
package fyneprint

import (
	"context"
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver"

	"github.com/timzifer/goprint"
)

func init() { goprint.SetMainThreadRunner(runOnMain) }

// runOnMain runs f on Fyne's main goroutine. Without a running app it
// calls f directly; goprint then sees that f is not on the main thread
// and reports ErrWrongThread.
func runOnMain(f func()) {
	if fyne.CurrentApp() == nil {
		f()
		return
	}
	fyne.DoAndWait(f)
}

// dialogFunc is goprint.Dialog, replaced in tests.
var dialogFunc = goprint.Dialog

// Owner returns the native handle of w for [goprint.DialogOptions.Owner]:
// the HWND on Windows, the NSWindow on macOS, the X11 window on Linux/BSD.
// It returns 0 for windows without a native handle, e.g. under Wayland
// (the portal then shows an unparented dialog) or in Fyne's test driver.
func Owner(w fyne.Window) uintptr {
	nw, ok := w.(driver.NativeWindow)
	if !ok {
		return 0
	}
	var h uintptr
	// The handle getters only read the GLFW window and are safe on any
	// goroutine; RunNative calls back synchronously.
	nw.RunNative(func(ctx any) {
		switch c := ctx.(type) {
		case driver.WindowsWindowContext:
			h = c.HWND
		case driver.MacWindowContext:
			h = c.NSWindow
		case driver.X11WindowContext:
			h = c.WindowHandle
		}
	})
	return h
}

// Dialog is [goprint.Dialog] with the dialog owned by w (unless
// opts.Owner is set). It blocks until the dialog closes and must not be
// called on Fyne's UI goroutine: the app would freeze, and on Windows the
// owner window could not answer the dialog and both would hang. Use
// [ShowDialog] from callbacks.
func Dialog(ctx context.Context, w fyne.Window, doc goprint.Document, opts goprint.DialogOptions) (*goprint.Job, goprint.Settings, error) {
	if opts.Owner == 0 && w != nil {
		opts.Owner = Owner(w)
	}
	if opts.Owner != 0 && ownedByCaller(opts.Owner) {
		return nil, goprint.Settings{}, fmt.Errorf("%w: fyneprint.Dialog called on the UI goroutine; use ShowDialog", goprint.ErrWrongThread)
	}
	return dialogFunc(ctx, doc, opts)
}

// ShowDialog shows the print dialog for w without blocking and calls done
// on Fyne's UI goroutine when it closes. It may be called from any
// goroutine, typically from a widget callback. done may be nil.
func ShowDialog(w fyne.Window, doc goprint.Document, opts goprint.DialogOptions, done func(*goprint.Job, goprint.Settings, error)) {
	ShowDialogContext(context.Background(), w, doc, opts, done)
}

// ShowDialogContext is [ShowDialog] with a context that can cancel the
// dialog.
func ShowDialogContext(ctx context.Context, w fyne.Window, doc goprint.Document, opts goprint.DialogOptions, done func(*goprint.Job, goprint.Settings, error)) {
	if opts.Owner == 0 && w != nil {
		opts.Owner = Owner(w)
	}
	go func() {
		job, s, err := dialogFunc(ctx, doc, opts)
		if done != nil {
			fyne.Do(func() { done(job, s, err) })
		}
	}()
}
