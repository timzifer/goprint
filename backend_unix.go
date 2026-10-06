//go:build linux || freebsd || openbsd || netbsd || dragonfly

package goprint

// TODO(phase 2): IPP against local CUPS, dialog via xdg-desktop-portal.
var platform backend = unsupported{goos: "unix"}
