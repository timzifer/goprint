//go:build darwin

package goprint

import "github.com/timzifer/goprint/ipp"

// macOS prints headless through CUPS with the same IPP code as Linux; the
// dialog is the AppKit print panel with PDFKit preview (dialog_darwin.go).
var platform backend = darwinBackend{ippBackend{newClient: ipp.NewCUPSClient}}
