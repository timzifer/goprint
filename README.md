# goprint

[![CI](https://github.com/timzifer/goprint/actions/workflows/ci.yml/badge.svg)](https://github.com/timzifer/goprint/actions/workflows/ci.yml)
[![Fuzz](https://github.com/timzifer/goprint/actions/workflows/fuzz.yml/badge.svg)](https://github.com/timzifer/goprint/actions/workflows/fuzz.yml)
[![Coverage](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/timzifer/goprint/badges/coverage.json)](https://github.com/timzifer/goprint/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/timzifer/goprint.svg)](https://pkg.go.dev/github.com/timzifer/goprint)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Platform-independent printing for Go: one API for Linux/BSD, macOS and Windows,
headless or through the native print dialog.

- **Pure Go** – builds with `CGO_ENABLED=0` for every `GOOS/GOARCH` from any host.
  Native APIs are reached through `golang.org/x/sys/windows`, `purego` and plain
  protocols (IPP, D-Bus).
- **PDF in, paper out** – documents are PDF (or images, wrapped into a PDF).
  CUPS, PDFKit and `Windows.Data.Pdf` render them natively.
- **Native dialogs, fully preset** – printer, copies, page ranges, media,
  orientation, duplex, color, collation – and the user's choice comes back as
  structured `Settings`. On Windows the modern dialog with live preview is supported.

> **Status:** 0.x preview releases; the API may still change before v1.0.
>
> | Feature | Linux/BSD | macOS | Windows |
> |---|---|---|---|
> | `Printers`, `GetCapabilities` | ✓ (CUPS) | ✓ (CUPS) | ✓ |
> | `Print` (headless) | ✓ all settings via IPP | ✓ all settings via IPP | ✓ amd64/386/arm64, all settings via DEVMODE/PrintTicket |
> | IPP Everywhere printer by URI (`Printer: "ipp://…"`) | ✓ | ✓ | ✓ |
> | `Dialog` | ✓ desktop dialog via xdg-desktop-portal (presets + result; no job tracking) | ✓ print panel with PDFKit preview; bare panel (no preview) for `PrintNow == false` | ✓ modern dialog with live preview and page selection; classic `PrintDlgEx` for settings-only use |
>
> Unimplemented parts return `ErrUnsupported` / `ErrNoDialog`.

## Install

```sh
go get github.com/timzifer/goprint
go get github.com/timzifer/goprint/fyneprint   # Fyne apps only
```

## Usage

```go
doc := goprint.PDFFile("invoice.pdf") // or PDFBytes, or Document{PDF: …} / {Images: …}

// Headless
job, err := goprint.Print(ctx, doc, goprint.Settings{
	Copies: 2,
	Media:  goprint.MediaA4,
	Duplex: goprint.DuplexLongEdge,
})
if err != nil {
	return err
}
for _, w := range job.Warnings() {
	log.Println("not honored:", w)
}
err = job.Wait(ctx)

// Interactive
job, chosen, err := goprint.Dialog(ctx, doc, goprint.DialogOptions{
	Settings: goprint.Settings{Copies: 2},
	PrintNow: true,
})
if errors.Is(err, goprint.ErrCanceled) {
	// user canceled
}

// Settings only ("page setup"), print later without UI
_, chosen, err = goprint.Dialog(ctx, doc, goprint.DialogOptions{})
job, err = goprint.Print(ctx, doc, chosen)
```

Settings a printer cannot honor are reported by `job.Warnings()`; with
`Settings.Strict` they fail the call with `ErrUnsupported` instead. Errors
wrap sentinels (`ErrCanceled`, `ErrPrinterNotFound`, `ErrNoDialog`, …) for
`errors.Is`. More examples are in the
[package documentation](https://pkg.go.dev/github.com/timzifer/goprint).

On Linux/BSD, `Dialog` shows the desktop's print dialog through
xdg-desktop-portal (`org.freedesktop.portal.Print`), so it also works in
Flatpak and Snap. Whether a preview is shown and whether presets such as the
printer are honored is up to the portal implementation; with
`RequirePrinter`, goprint refuses to print if the dialog returns another
printer. Jobs printed through the portal cannot be tracked or canceled.
Without a running portal, `Dialog` returns `ErrNoDialog`.

On Windows, the modern print dialog cannot preselect a printer. Windows 11
also shows the classic dialog (`PrintDlgEx`) as its modern dialog and then
ignores the preselection, so `DialogOptions.RequirePrinter` returns
`ErrUnsupported` there. `StyleAuto` uses the modern dialog (with preview)
for printing and the classic one for `PrintNow == false`: only the classic
dialog reports the chosen printer, and it creates no job, so print-to-file
printers never ask for a file name. It has no preview (see
[#18](https://github.com/timzifer/goprint/issues/18)).

`DialogOptions.NoFileOutput` keeps dialogs from writing files: printers that
write files ("Microsoft Print to PDF", CUPS-PDF, GTK's "Print to File";
`Printer.ToFile`) and "Save as PDF" or "Open in Preview" on macOS. None of the
platform dialogs can hide them all, so choosing one returns
`goprint.ErrFileOutput` before anything is printed or written. On Windows this
uses the classic dialog (`StyleModern` returns `ErrUnsupported`); on macOS the
panel then shows no preview.

On Windows, `Settings.Vendor[goprint.VendorOutputFile]` writes the printer
output to a file instead of the device, e.g. to get a PDF from
"Microsoft Print to PDF" without its save dialog.

On Windows, `goprint.PrinterProperties` opens the printer driver's own settings
dialog ("Printing preferences") for what `Settings` has no field for:
finishing, stapling, secure print and so on. It returns the settings with the
user's choices; the driver's part travels as
`Settings.Vendor[goprint.VendorDevMode]` (a base64 DEVMODE) to `Print`, which
applies the other settings on top. The classic dialog returns it too. Apps can
store it to reuse the choice later, but only for the same printer.

### Providers

The package-level functions print through `goprint.Default`, a `Client` with
only the `System()` provider (CUPS, the Windows spooler, IPP URIs). A
`Client` can combine further providers that implement `goprint.Provider`,
such as the simulated printers of `goprint/virtualprinter`. `Printer.Provider` tells where a printer comes
from; `Settings.Provider` selects the provider that prints (empty: the system).

```go
c := goprint.NewClient(goprint.System(), myProvider) // myProvider.Name() == "virtual"
printers, err := c.Printers(ctx) // all providers; partial results if some fail
job, err := c.Print(ctx, doc, goprint.Settings{Provider: "virtual", Printer: "Label-62"})
```

Programs may also replace `goprint.Default` before they print. Providers
create their jobs with `goprint.NewJob`; implementing `DialogProvider` or
`PropertiesProvider` adds a dialog or a driver dialog.

`goprint.IPPEverywhere(goprint.IPPEverywhereOptions{})` finds the IPP printers
on the local network through DNS-SD (multicast DNS) and prints to them
directly: no driver and no print server, on every platform, also on Linux
without CUPS. Printers are named by their DNS-SD instance name. PDF goes to
printers that accept it as it is. Many printers only take raster formats (PWG
Raster, Apple Raster/URF); for them the separate module `goprint/raster`
renders the PDF with [cera](https://github.com/timzifer/cera), choosing format,
resolution and color space from the printer's attributes. Without it, such
printers return `ErrUnsupported`.

```go
ipp := goprint.IPPEverywhere(goprint.IPPEverywhereOptions{Rasterizer: raster.New()})
goprint.Default = goprint.NewClient(goprint.System(), ipp)
// Printers lists them with Provider "ipp"
```

Two-sided raster jobs need printers that take back sides as they come
(`pwg-raster-document-sheet-back` "normal", URF "DM1"); others print
one-sided with a warning for now.

`goprint/virtualprinter` simulates printers without hardware, for tests,
demos and development: jobs are kept in memory as PDF, settings a printer
cannot honor become warnings, and failures are scripted (offline printers,
failing or held jobs, latency). Presets: `Office`, `Label`, `Photo`,
`Receipt`, `PDFWriter`.

```go
vp := virtualprinter.New("virtual", virtualprinter.Office("Office"), virtualprinter.Label("Label-62"))
goprint.Default = goprint.NewClient(goprint.System(), vp)
// ... print with Settings{Provider: "virtual", Printer: "Label-62"}
pdf := vp.Jobs()[0].PDF
```

### macOS: main thread

AppKit runs only on the main thread. Lock the main goroutine to it in an
`init` of package `main` and run your program through `goprint.RunMain`;
`Dialog` may then be called from any goroutine inside it (or directly from
the main goroutine). Called from another thread without `RunMain`, `Dialog`
returns `ErrWrongThread`. Programs with their own Cocoa event loop call
`Dialog` on that thread. `RunMain` just calls `f` on other platforms.

```go
func init() { runtime.LockOSThread() }

func main() {
	goprint.RunMain(func() {
		job, chosen, err := goprint.Dialog(ctx, doc, goprint.DialogOptions{PrintNow: true})
		// ...
	})
}
```

The panel is app-modal (`DialogOptions.Owner` is not used for a sheet
yet) and takes a single page range. A job printed from the panel is found
in CUPS by name; if the user saves a PDF or opens Preview instead, the
returned `Job` cannot be tracked and reports completed.

## Platforms

| Platform  | Headless                         | Dialog                                    |
|-----------|----------------------------------|-------------------------------------------|
| Linux/BSD | IPP to CUPS (no libcups)         | xdg-desktop-portal (`org.freedesktop.portal.Print`) |
| macOS     | IPP to CUPS                      | `NSPrintOperation` via purego             |
| Windows   | PDF → Direct2D → XPS → spooler   | Modern (`PrintManager`, preview) or classic (`PrintDlgEx`) |
| any       | IPP Everywhere printers by URI, or found via DNS-SD (`IPPEverywhere`) | –          |
| Android, iOS | not yet: every call returns `ErrUnsupported` | –                         |

## Packages

| Package           | Purpose                                                 |
|-------------------|---------------------------------------------------------|
| `goprint`         | Public API, types, errors                               |
| `goprint/ipp`     | IPP codec and client (RFC 8010/8011), usable standalone |
| `goprint/ipp/ipptest` | IPP mock server for tests                           |
| `goprint/raster` | PDF → PWG Raster / Apple Raster for `IPPEverywhere` (separate module, uses cera) |
| `goprint/virtualprinter` | Simulated printers as a `Provider`: jobs kept in memory as PDF, scripted failures |
| `goprint/fyneprint` | [Fyne](https://fyne.io) integration (separate module) |

## Fyne

Fyne's event loop owns the main thread, so a Fyne app uses
[`fyneprint`](fyneprint) instead of `goprint.RunMain`: importing it lets
goprint reach the main thread through `fyne.DoAndWait`, and `ShowDialog`
runs the dialog owned by a Fyne window without blocking the UI:

```go
fyneprint.ShowDialog(w, doc, goprint.DialogOptions{PrintNow: true},
	func(job *goprint.Job, s goprint.Settings, err error) { /* on the UI goroutine */ })
```

Other toolkits that own the main thread install their own runner with
`goprint.SetMainThreadRunner`.

## Example

[`examples/printdemo`](examples/printdemo) lists printers, shows capabilities,
opens the print dialog and prints headless; its README has a manual test
checklist for macOS.

## Development

```sh
go test -race ./...
./.github/scripts/fuzz.sh 30s          # all fuzz targets
GOOS=windows go vet ./...             # vet another platform
```

CI runs tests on Linux, macOS and Windows, `go vet`, `staticcheck`,
`govulncheck`, a cross-compile matrix with `CGO_ENABLED=0` and nightly fuzzing.

Changes to `main` go through pull requests; the `ci-ok` check must be green.

## License

[MIT](LICENSE)
