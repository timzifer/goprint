//go:build windows

package winprint

import "math"

// On amd64 an 8-byte struct passed by value travels in one integer register.
const addPageSupported = true

func sizeArgs(s size) []uintptr {
	return []uintptr{uintptr(math.Float32bits(s.H))<<32 | uintptr(math.Float32bits(s.W))}
}
