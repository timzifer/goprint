//go:build linux || freebsd || openbsd || netbsd || dragonfly

package goprint

import "github.com/timzifer/goprint/ipp"

// Headless printing talks IPP to the local CUPS scheduler: no lp, no
// libcups. The dialog goes through xdg-desktop-portal (dialog_unix.go).
var platform backend = unixBackend{ippBackend{newClient: ipp.NewCUPSClient}}
