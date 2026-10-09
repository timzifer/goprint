//go:build android || ios || !(linux || freebsd || openbsd || netbsd || dragonfly || darwin || windows)

package goprint

import "runtime"

var platform backend = unsupported{goos: runtime.GOOS}
