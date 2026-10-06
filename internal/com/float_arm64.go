//go:build windows

package com

import (
	"fmt"

	"github.com/timzifer/goprint/internal/errdefs"
)

// CallFloatHR is not implemented on arm64, where floats travel in SIMD
// registers that syscall.SyscallN cannot set.
func (u *Unknown) CallFloatHR(op string, i int, args ...uintptr) error {
	return fmt.Errorf("%w: %s with float arguments on arm64", errdefs.ErrUnsupported, op)
}
