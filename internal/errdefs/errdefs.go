// Package errdefs defines the sentinel errors shared by goprint and its
// internal backends. goprint re-exports them.
package errdefs

import "errors"

var (
	ErrCanceled        = errors.New("goprint: canceled by user")
	ErrNoPrinter       = errors.New("goprint: no printer available")
	ErrPrinterNotFound = errors.New("goprint: printer not found")
	ErrUnsupported     = errors.New("goprint: not supported on this platform")
	ErrNoDialog        = errors.New("goprint: no print dialog available")
	ErrWrongThread     = errors.New("goprint: must be called on the main thread")
	ErrBusy            = errors.New("goprint: busy")
	ErrInvalid         = errors.New("goprint: invalid argument")
	ErrFileOutput      = errors.New("goprint: output to a file is not allowed")
)
