package goprint

import (
	"fmt"

	"github.com/timzifer/goprint/internal/errdefs"
)

// Sentinel errors. Detailed errors wrap them and are reachable via errors.Is.
var (
	ErrCanceled        = errdefs.ErrCanceled
	ErrNoPrinter       = errdefs.ErrNoPrinter
	ErrPrinterNotFound = errdefs.ErrPrinterNotFound
	ErrUnsupported     = errdefs.ErrUnsupported
	ErrNoDialog        = errdefs.ErrNoDialog
	ErrWrongThread     = errdefs.ErrWrongThread
	ErrBusy            = errdefs.ErrBusy
	ErrInvalid         = errdefs.ErrInvalid
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
