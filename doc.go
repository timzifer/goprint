// Package goprint prints documents on Linux/BSD, macOS and Windows through
// one API, either headless or through the platform's native print dialog.
//
// The package is pure Go: it builds with CGO_ENABLED=0 for every supported
// GOOS/GOARCH from any host. Documents are handled as PDF internally; raster
// sources ([]image.Image) are wrapped into an image-only PDF so that there is
// a single print path.
//
// # Overview
//
// The API has three verbs:
//
//   - [Printers] and [GetCapabilities] for discovery,
//   - [Print] for headless printing,
//   - [Dialog] for interactive printing through the native dialog.
//
// A [Document] is a PDF ([PDFFile], [PDFBytes] or any reader) or a list of
// images. [Settings] flows both ways: into [Dialog] as presets, out of it as
// the user's choice, which can be passed to [Print] later.
//
//	job, err := goprint.Print(ctx, goprint.PDFFile("invoice.pdf"), goprint.Settings{
//		Copies: 2,
//		Media:  goprint.MediaA4,
//		Duplex: goprint.DuplexLongEdge,
//	})
//	if err != nil {
//		return err
//	}
//	return job.Wait(ctx)
//
// # Settings and warnings
//
// Zero values in [Settings] mean "printer default". Settings a printer or
// platform cannot honor do not fail the job; they are reported by
// [Job.Warnings]. With [Settings.Strict] they are errors wrapping
// [ErrUnsupported] instead, and nothing is printed. [Settings.Vendor]
// passes platform-specific values through (IPP attributes on Linux/BSD and
// macOS, see also [VendorOutputFile], [VendorGTKPrefix] and
// [VendorDevMode]).
//
// On Windows, [PrinterProperties] opens the driver's own settings dialog
// for what Settings has no field for (finishing, stapling, secure print,
// ...); the result travels to [Print] in Settings.Vendor[VendorDevMode].
//
// # Providers
//
// The package-level functions print through [Default], a [Client] with
// only the [System] provider. A Client can combine further providers,
// such as the simulated printers of package virtualprinter;
// [Printer.Provider] tells where a printer comes from and
// [Settings.Provider] selects the provider that prints.
// [IPPEverywhere] is a provider for the IPP printers on the local network,
// found through DNS-SD; a [Rasterizer] (module goprint/raster) renders
// for those that accept no PDF. A [Provider] returns its jobs through [NewJob]; [DialogProvider] and
// [PropertiesProvider] add a dialog and a driver dialog.
//
// # Errors
//
// Errors wrap the sentinel errors ([ErrCanceled], [ErrPrinterNotFound],
// [ErrUnsupported], ...) and are tested with [errors.Is]. A canceled dialog
// is [ErrCanceled], not a failure.
//
// # Platforms
//
// Linux/BSD and macOS print through CUPS over IPP (no libcups); printers
// given by URI ("ipp://…", "ipps://…") are reached directly on every
// platform, including Windows. Windows renders the PDF with
// Windows.Data.Pdf and Direct2D into an XPS job for the spooler.
//
// Dialogs: the desktop's dialog through xdg-desktop-portal on Linux/BSD,
// NSPrintOperation with a PDFKit preview on macOS, and on Windows the
// modern dialog with live preview or the classic PrintDlgEx (see
// [DialogStyle] and [DialogOptions.PrintNow]).
//
// On iOS the dialog is UIKit's print sheet (AirPrint); without PrintNow it
// only picks a printer. iOS lists no printers to apps: Print reaches a
// printer by the URL the dialog returned. Dialog and Print wait for
// UIKit's main thread and must be called from another goroutine.
// Android is not supported yet: every call returns [ErrUnsupported].
//
// # Main thread (macOS)
//
// AppKit, and so the print panel, runs only on the process's main thread.
// Command-line style programs run through [RunMain]; programs whose main
// thread belongs to a GUI toolkit install a runner with
// [SetMainThreadRunner] (package [github.com/timzifer/goprint/fyneprint]
// does that for Fyne). Other platforms need neither.
package goprint
