//go:build ios

package goprint

import (
	"context"
	"fmt"
	"io"
)

// iOS has no print queues an app can list. The dialog is UIKit's print
// sheet (AirPrint); headless printing goes to a printer the user picked
// once, by its URL (dialog_ios.go).
var platform backend = iosBackend{}

type iosBackend struct{}

// printers is empty: iOS lists printers only in its own sheet. Use
// [Dialog] (without PrintNow to only pick a printer) or [IPPEverywhere].
func (iosBackend) printers(context.Context) ([]Printer, error) { return nil, nil }

func (iosBackend) capabilities(ctx context.Context, printer string) (Capabilities, error) {
	if !isPrinterURI(printer) {
		return Capabilities{}, iosNoPrinter(printer)
	}
	c, err := networkIPP.capabilities(ctx, printer)
	if err == nil {
		c.DialogPreview = true
	}
	return c, err
}

func (iosBackend) print(ctx context.Context, src io.Reader, doc Document, s Settings) (*Job, error) {
	if !isPrinterURI(s.Printer) {
		return nil, iosNoPrinter(s.Printer)
	}
	pdf, err := io.ReadAll(src)
	if err != nil {
		return nil, fmt.Errorf("goprint: reading document: %w", err)
	}
	info, warnings := toIOSPrintInfo(s, docTitle(doc))
	if len(doc.Attributes) > 0 {
		warnings = append(warnings, attributesWarning("iOS does not pass them on"))
	}
	if s.Strict && len(warnings) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, warnings[0])
	}
	if err := iosPrintToPrinter(ctx, pdf, info); err != nil {
		return nil, err
	}
	return &Job{b: iosJob{}, warnings: warnings}, nil
}

func (iosBackend) properties(context.Context, Settings, uintptr) (Settings, error) {
	return Settings{}, fmt.Errorf("%w: printers have no driver dialog on iOS", ErrUnsupported)
}

// iosNoPrinter is the error for a printer that is not a URL.
func iosNoPrinter(name string) error {
	if name == "" {
		return fmt.Errorf("%w: iOS has no default printer; pick one with Dialog", ErrNoPrinter)
	}
	return fmt.Errorf("%w: %q: iOS reaches printers only by URL (ipp://…), as Dialog returns them", ErrPrinterNotFound, name)
}

// iosJob is a job handed to iOS, which does not report its progress.
type iosJob struct{}

func (iosJob) id() string                              { return "" }
func (iosJob) state(context.Context) (JobState, error) { return JobCompleted, nil }
func (iosJob) wait(context.Context) error              { return nil }
func (iosJob) cancel(context.Context) error {
	return fmt.Errorf("%w: iOS does not let apps track print jobs", ErrUnsupported)
}
