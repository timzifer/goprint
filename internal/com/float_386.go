//go:build windows

package com

// CallFloatHR invokes vtable slot i like CallHR. On 386 float32 arguments
// are pushed on the stack as 32-bit words, so passing F32(x) just works.
//
//go:uintptrescapes
func (u *Unknown) CallFloatHR(op string, i int, args ...uintptr) error {
	return u.CallHR(op, i, args...)
}
