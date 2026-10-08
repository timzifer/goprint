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

> **Status:** early development, the API is not stable yet.
>
> | Feature | Linux/BSD | macOS | Windows |
> |---|---|---|---|
> | `Printers`, `GetCapabilities` | ✓ (CUPS) | ✓ (CUPS) | ✓ |
> | `Print` (headless) | ✓ all settings via IPP | ✓ all settings via IPP | ✓ amd64/386, all settings via DEVMODE/PrintTicket |
> | IPP Everywhere printer by URI (`Printer: "ipp://…"`) | ✓ | ✓ | ✓ |
> | `Dialog` | ✓ desktop dialog via xdg-desktop-portal (presets + result; no job tracking) | ✓ print panel with PDFKit preview; bare panel (no preview) for `PrintNow == false` | ✓ amd64/386: modern dialog with live preview and page selection; classic `PrintDlgEx` for settings-only use |
>
> Unimplemented parts return `ErrUnsupported` / `ErrNoDialog`.

## Usage

```go
doc := goprint.Document{
	Title: "Invoice 4711",
	PDF:   func() (io.ReadSeekCloser, error) { return os.Open("invoice.pdf") },
}

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
```

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
for printing and the classic one for `PrintNow == false`, which creates no
job and so never asks print-to-file printers for a file name.

On Windows, `Settings.Vendor[goprint.VendorOutputFile]` writes the printer
output to a file instead of the device, e.g. to get a PDF from
"Microsoft Print to PDF" without its save dialog.

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
| any       | IPP Everywhere printers (`goprint/ipp`) | –                                  |

## Packages

| Package           | Purpose                                                 |
|-------------------|---------------------------------------------------------|
| `goprint`         | Public API, types, errors                               |
| `goprint/ipp`     | IPP codec and client (RFC 8010/8011), usable standalone |
| `goprint/ipp/ipptest` | IPP mock server for tests                           |
| `goprint/win`     | Windows extras (raw PrintTicket, DEVMODE)               |

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
