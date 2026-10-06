//go:build windows

package winprint

// On arm64 D2D_SIZE_F is a homogeneous floating-point aggregate passed in
// SIMD registers, which syscall.SyscallN cannot set. Printing needs an
// assembly trampoline there; until then it reports ErrUnsupported.
const addPageSupported = false

func sizeArgs(size) []uintptr { return nil }
