package goprint

import (
	"fmt"

	"github.com/timzifer/goprint/internal/errdefs"
)

// Sentinel errors. Detailed errors wrap them and are reachable via errors.Is.
var (
	// ErrCanceled: the user canceled the dialog, or the job was canceled.
	ErrCanceled = errdefs.ErrCanceled
	// ErrNoPrinter: no printer is installed, or there is no default printer.
	ErrNoPrinter = errdefs.ErrNoPrinter
	// ErrPrinterNotFound: the named printer does not exist.
	ErrPrinterNotFound = errdefs.ErrPrinterNotFound
	// ErrUnsupported: the platform, printer or dialog cannot do what was
	// asked, e.g. a setting under Settings.Strict.
	ErrUnsupported = errdefs.ErrUnsupported
	// ErrNoDialog: no print dialog is available, e.g. no xdg-desktop-portal
	// or no session bus on Linux.
	ErrNoDialog = errdefs.ErrNoDialog
	// ErrWrongThread: Dialog on macOS was called off the main thread without
	// RunMain or a runner from SetMainThreadRunner.
	ErrWrongThread = errdefs.ErrWrongThread
	// ErrBusy: the printer or server does not accept jobs right now; a later
	// retry may succeed.
	ErrBusy = errdefs.ErrBusy
	// ErrInvalid: invalid arguments, e.g. a Document with neither PDF nor
	// Images, or negative Copies.
	ErrInvalid = errdefs.ErrInvalid
)

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalid}, args...)...)
}

// Warning reports a setting that could not be honored.
type Warning struct {
	// Setting names the affected setting, e.g. "Duplex".
	Setting string
	Message string
}

func (w Warning) String() string {
	return w.Setting + ": " + w.Message
}
