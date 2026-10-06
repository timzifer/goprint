//go:build windows

package goprint

// TODO(phase 1/3): PDF → Direct2D → XPS pipeline, modern and classic dialog.
var platform backend = unsupported{goos: "windows"}
