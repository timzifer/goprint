package goprint

import (
	"context"
	"image"
	"io"
	"strings"
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
	// Provider names the [Provider] the printer belongs to; "" is the
	// [System] provider. Use it as [Settings.Provider].
	Provider    string
	Name        string
	Description string
	Location    string
	Default     bool
	// ToFile reports a printer that writes files instead of paper, such as
	// "Microsoft Print to PDF", "Microsoft XPS Document Writer" or
	// CUPS-PDF. It is detected from the name, and on Windows from the port.
	ToFile bool
	Caps   Capabilities
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
	// Trays lists the paper sources, as values for [Settings.Tray]: IPP
	// keywords such as "auto", "manual" or "tray-1" on Linux/BSD and macOS,
	// the driver's bin names on Windows. Empty if unknown.
	Trays []string
	// Qualities lists the supported [Settings.Quality] values, without
	// QualityDefault. Empty if unknown.
	Qualities []Quality
	// DriverDialog reports whether [PrinterProperties] can show the
	// driver's own settings dialog for the printer.
	DriverDialog bool
}

// Resolution is a printer resolution in dots per inch.
type Resolution struct {
	X, Y int
}

// Printers lists the available printers of [Default]'s providers, as
// [Client.Printers].
func Printers(ctx context.Context) ([]Printer, error) {
	return Default.Printers(ctx)
}

// GetCapabilities reports the capabilities of the named printer of the
// [System] provider. An empty name refers to the default printer. Other
// providers' printers are reached through [Client.Capabilities].
func GetCapabilities(ctx context.Context, printer string) (Capabilities, error) {
	return Default.Capabilities(ctx, "", printer)
}

// Print prints doc with s, without any UI, through [Default].
func Print(ctx context.Context, doc Document, s Settings) (*Job, error) {
	return Default.Print(ctx, doc, s)
}

// PrinterProperties shows the printer driver's own settings dialog
// ("Printing preferences" on Windows) for s.Printer (empty: the default
// printer), preset with s, and returns s with the user's choices. The
// standard settings are read back into their fields; everything else the
// driver offers travels in s.Vendor[VendorDevMode] to [Print]. owner is
// the parent window as in [DialogOptions.Owner].
//
// It blocks until the dialog closes and returns [ErrCanceled] if the user
// cancels. Only Windows printers have such a dialog
// ([Capabilities.DriverDialog]); elsewhere it returns [ErrUnsupported].
func PrinterProperties(ctx context.Context, s Settings, owner uintptr) (Settings, error) {
	return Default.PrinterProperties(ctx, s, owner)
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
	// NoFileOutput keeps the dialog from writing files: printers that
	// write files ("Microsoft Print to PDF", CUPS-PDF, GTK's "Print to
	// File") and "Save as PDF" / "Open in Preview" on macOS. Where the
	// dialog cannot hide them, choosing one returns [ErrFileOutput] before
	// anything is printed or written. On Windows this needs the classic
	// dialog: StyleAuto uses it, StyleModern returns [ErrUnsupported].
	NoFileOutput bool
	// PrintNow prints after confirmation. If false, Dialog only returns the
	// chosen settings and a nil Job.
	//
	// On Windows, StyleAuto without PrintNow shows the classic dialog: it
	// reports the chosen printer and full settings but has no preview, also
	// where Windows 11 draws it in the modern look. StyleModern previews,
	// but cannot report the printer and makes print-to-file printers ask
	// for a file name.
	PrintNow bool
}

// Dialog shows the native print dialog, preset with opts.Settings, and
// returns the job (if printed) together with the settings the user chose.
// It returns [ErrCanceled] if the user cancels and [ErrNoDialog] if the
// platform has no dialog available.
func Dialog(ctx context.Context, doc Document, opts DialogOptions) (*Job, Settings, error) {
	return Default.Dialog(ctx, doc, opts)
}

// fileOutputName reports printer names that stand for printers writing
// files instead of paper.
func fileOutputName(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	return strings.Contains(n, "print to pdf") || strings.Contains(n, "xps document writer") ||
		strings.Contains(n, "onenote") || n == "pdf" || strings.Contains(n, "cups-pdf") ||
		strings.Contains(n, "print to file")
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
