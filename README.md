# goprint

[![CI](https://github.com/timzifer/goprint/actions/workflows/ci.yml/badge.svg)](https://github.com/timzifer/goprint/actions/workflows/ci.yml)
[![Fuzz](https://github.com/timzifer/goprint/actions/workflows/fuzz.yml/badge.svg)](https://github.com/timzifer/goprint/actions/workflows/fuzz.yml)
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

> **Status:** early development. The API is not stable yet and the platform
> backends currently return `ErrUnsupported`.

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

## Development

```sh
go test -race ./...
./.github/scripts/fuzz.sh 30s          # all fuzz targets
GOOS=windows go vet ./...             # vet another platform
```

CI runs tests on Linux, macOS and Windows, `go vet`, `staticcheck`,
`govulncheck`, a cross-compile matrix with `CGO_ENABLED=0` and nightly fuzzing.

## License

[MIT](LICENSE)
