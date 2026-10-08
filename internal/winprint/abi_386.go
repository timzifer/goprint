//go:build windows

package winprint

import (
	"math"

	"github.com/timzifer/goprint/internal/com"
)

// sizeArg passes a D2D_SIZE_F by value. On 386 a struct is pushed on the
// stack word by word.
func sizeArg(s size) []com.Arg {
	return []com.Arg{com.I(uintptr(math.Float32bits(s.W))), com.I(uintptr(math.Float32bits(s.H)))}
}

// tokenArgs passes an EventRegistrationToken (int64) by value: two stack words.
func tokenArgs(t int64) []uintptr {
	return []uintptr{uintptr(uint32(t)), uintptr(uint32(uint64(t) >> 32))}
}

// pointArgs passes a POINT by value: two stack words.
func pointArgs(x, y int32) []uintptr { return []uintptr{uintptr(uint32(x)), uintptr(uint32(y))} }
