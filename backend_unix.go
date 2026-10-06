//go:build linux || freebsd || openbsd || netbsd || dragonfly

package goprint

import "github.com/timzifer/goprint/ipp"

// Headless printing talks IPP to the local CUPS scheduler: no lp, no libcups.
// TODO(phase 4): dialog via xdg-desktop-portal.
var platform backend = ippBackend{newClient: ipp.NewCUPSClient}
