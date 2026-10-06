//go:build darwin

package goprint

import "github.com/timzifer/goprint/ipp"

// macOS prints through CUPS as well; headless uses the same IPP code as
// Linux. TODO(phase 4): dialog via NSPrintOperation (purego).
var platform backend = ippBackend{newClient: ipp.NewCUPSClient}
