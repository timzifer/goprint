//go:build darwin

package goprint

// TODO(phase 2/4): IPP against local CUPS, dialog via NSPrintOperation (purego).
var platform backend = unsupported{goos: "darwin"}
