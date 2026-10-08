//go:build windows

package winprint

import "github.com/timzifer/goprint/internal/com"

// sizeArg passes a D2D_SIZE_F by value. On arm64 it is a homogeneous
// floating-point aggregate and travels in s-registers.
func sizeArg(s size) []com.Arg {
	return []com.Arg{com.F(s.W), com.F(s.H)}
}

func tokenArgs(t int64) []uintptr { return []uintptr{uintptr(t)} }

// pointArgs passes a POINT by value (8 bytes, one register).
func pointArgs(x, y int32) []uintptr { return []uintptr{uintptr(uint32(y))<<32 | uintptr(uint32(x))} }
