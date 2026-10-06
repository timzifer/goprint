// Package goprint prints documents on Linux/BSD, macOS and Windows through
// one API, either headless or through the platform's native print dialog.
//
// The package is pure Go: it builds with CGO_ENABLED=0 for every supported
// GOOS/GOARCH from any host. Documents are handled as PDF internally; raster
// sources ([]image.Image) are wrapped into an image-only PDF so that there is
// a single print path.
//
// The API has three verbs:
//
//   - [Printers] and [GetCapabilities] for discovery,
//   - [Print] for headless printing,
//   - [Dialog] for interactive printing through the native dialog.
//
// [Settings] flows both ways: into [Dialog] as presets, out of it as the
// user's choice.
package goprint

import (
	"context"
	"image"
	"io"
)

// Document is the content to print. Exactly one of PDF or Images is set.
type Document struct {
	// Title is the job name in the spooler and the dialog title.
	Title string

	// PDF opens the PDF content. It is called once per consumer (preview
	// and print run separately), so it must return a fresh reader each time.
	PDF func() (io.ReadSeekCloser, error)

	// Images is a raster source, one image per page.
	Images []image.Image
	// DPI is the resolution of Images. Required when Images is set.
	DPI int
}

// Printer describes a print queue.
type Printer struct {
	Name        string
	Description string
	Location    string
	Default     bool
	Caps        Capabilities
}

// Capabilities lists what a printer (and the platform's dialog) supports.
type Capabilities struct {
	Media       []Media
	Duplex      bool
	Color       bool
	Resolutions []Resolution
	// Formats lists accepted document MIME types, e.g. "application/pdf".
	Formats []string
	// DialogPreview reports whether the native dialog shows a print preview.
	DialogPreview bool
}

// Resolution is a printer resolution in dots per inch.
type Resolution struct {
	X, Y int
}

// Printers lists the available printers.
func Printers(ctx context.Context) ([]Printer, error) {
	return platform.printers(ctx)
}

// GetCapabilities reports the capabilities of the named printer. An empty
// name refers to the default printer.
func GetCapabilities(ctx context.Context, printer string) (Capabilities, error) {
	return platform.capabilities(ctx, printer)
}

// Print prints doc with s, without any UI.
func Print(ctx context.Context, doc Document, s Settings) (*Job, error) {
	if err := doc.validate(); err != nil {
		return nil, err
	}
	if err := s.validate(); err != nil {
		return nil, err
	}
	src, err := doc.open()
	if err != nil {
		return nil, err
	}
	defer src.Close()
	return platform.print(ctx, src, doc, s)
}

// DialogStyle selects the native dialog on platforms that offer several.
type DialogStyle int

const (
	// StyleAuto picks the best dialog for the platform and settings.
	StyleAuto DialogStyle = iota
	// StyleModern is the dialog with print preview (Windows 10+).
	StyleModern
	// StyleClassic is the dialog with full presets, including the printer.
	StyleClassic
)

// DialogOptions configures [Dialog].
type DialogOptions struct {
	// Settings are the presets shown in the dialog.
	Settings Settings
	// Owner is the parent window: HWND, NSWindow* or X11 window / portal
	// parent. 0 means a private helper window.
	Owner uintptr
	// Style selects the dialog on Windows.
	Style DialogStyle
	// RequirePrinter forces a dialog that preselects Settings.Printer.
	RequirePrinter bool
	// PrintNow prints after confirmation. If false, Dialog only returns the
	// chosen settings and a nil Job.
	PrintNow bool
}

// Dialog shows the native print dialog, preset with opts.Settings, and
// returns the job (if printed) together with the settings the user chose.
// It returns [ErrCanceled] if the user cancels and [ErrNoDialog] if the
// platform has no dialog available.
func Dialog(ctx context.Context, doc Document, opts DialogOptions) (*Job, Settings, error) {
	if err := doc.validate(); err != nil {
		return nil, Settings{}, err
	}
	if err := opts.Settings.validate(); err != nil {
		return nil, Settings{}, err
	}
	return platform.dialog(ctx, doc, opts)
}

func (d Document) validate() error {
	switch {
	case d.PDF != nil && len(d.Images) > 0:
		return invalidf("document: both PDF and Images set")
	case d.PDF == nil && len(d.Images) == 0:
		return invalidf("document: neither PDF nor Images set")
	case len(d.Images) > 0 && d.DPI <= 0:
		return invalidf("document: DPI must be positive for Images, got %d", d.DPI)
	}
	for i, img := range d.Images {
		if img == nil {
			return invalidf("document: image %d is nil", i)
		}
	}
	return nil
}
