//go:build (android && !cgo) || !(linux || freebsd || openbsd || netbsd || dragonfly || darwin || windows)

package goprint

import "runtime"

var platform backend = unsupported{goos: runtime.GOOS}
