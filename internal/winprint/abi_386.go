//go:build windows

package winprint

import "math"

// On 386 a struct passed by value is pushed on the stack word by word.
const addPageSupported = true

func sizeArgs(s size) []uintptr {
	return []uintptr{uintptr(math.Float32bits(s.W)), uintptr(math.Float32bits(s.H))}
}

// tokenArgs passes an EventRegistrationToken (int64) by value: two stack words.
func tokenArgs(t int64) []uintptr {
	return []uintptr{uintptr(uint32(t)), uintptr(uint32(uint64(t) >> 32))}
}

// pointArgs passes a POINT by value: two stack words.
func pointArgs(x, y int32) []uintptr { return []uintptr{uintptr(uint32(x)), uintptr(uint32(y))} }
