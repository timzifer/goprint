//go:build windows

package winprint

import "math"

// On 386 a struct passed by value is pushed on the stack word by word.
const addPageSupported = true

func sizeArgs(s size) []uintptr {
	return []uintptr{uintptr(math.Float32bits(s.W)), uintptr(math.Float32bits(s.H))}
}
