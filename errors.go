package goprint

import (
	"errors"
	"fmt"
)

// Sentinel errors. Detailed errors wrap them and are reachable via errors.Is.
var (
	ErrCanceled        = errors.New("goprint: canceled by user")
	ErrNoPrinter       = errors.New("goprint: no printer available")
	ErrPrinterNotFound = errors.New("goprint: printer not found")
	ErrUnsupported     = errors.New("goprint: not supported on this platform")
	ErrNoDialog        = errors.New("goprint: no print dialog available")
	ErrWrongThread     = errors.New("goprint: must be called on the main thread")
	ErrBusy            = errors.New("goprint: busy")
	ErrInvalid         = errors.New("goprint: invalid argument")
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
