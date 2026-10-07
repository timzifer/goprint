//go:build windows

package winprint

import (
	"math"

	"github.com/timzifer/goprint/internal/com"
)

// sizeArg passes a D2D_SIZE_F by value. On amd64 an 8-byte struct travels
// in one integer register.
func sizeArg(s size) []com.Arg {
	return []com.Arg{com.I(uintptr(math.Float32bits(s.H))<<32 | uintptr(math.Float32bits(s.W)))}
}

// tokenArgs passes an EventRegistrationToken (int64) by value.
func tokenArgs(t int64) []uintptr { return []uintptr{uintptr(t)} }

// pointArgs passes a POINT by value (8 bytes, one register).
func pointArgs(x, y int32) []uintptr { return []uintptr{uintptr(uint32(y))<<32 | uintptr(uint32(x))} }
