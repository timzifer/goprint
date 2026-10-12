//go:build darwin && !ios

package goprint

import "github.com/timzifer/goprint/ipp"

// macOS prints headless through CUPS with the same IPP code as Linux (jobs
// with an orientation or scaling through PDFKit, see darwinBackend.print); the
// dialog is the AppKit print panel with PDFKit preview (dialog_darwin.go).
var platform backend = darwinBackend{ippBackend{newClient: ipp.NewCUPSClient}}
