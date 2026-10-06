//go:build windows

package winprint

// On arm64 D2D_SIZE_F is a homogeneous floating-point aggregate passed in
// SIMD registers, which syscall.SyscallN cannot set. Printing needs an
// assembly trampoline there; until then it reports ErrUnsupported.
const addPageSupported = false

func sizeArgs(size) []uintptr { return nil }

func tokenArgs(t int64) []uintptr { return []uintptr{uintptr(t)} }

// pointArgs passes a POINT by value (8 bytes, one register).
func pointArgs(x, y int32) []uintptr { return []uintptr{uintptr(uint32(y))<<32 | uintptr(uint32(x))} }
