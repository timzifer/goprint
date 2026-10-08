//go:build windows

package com

// Arg is an argument of CallArgsHR: an integer or pointer (I) or a float32
// passed by value (F). Calling conventions place floats differently: on
// amd64 by position in XMM registers, on arm64 in their own s-registers,
// on 386 on the stack. CallArgsHR lays them out for the running
// architecture. A struct of two floats passed by value (D2D_SIZE_F) is two
// F arguments: on amd64 such a struct travels in one integer register, so
// pass it there as I of the packed bits instead (see the callers).
type Arg struct {
	v     uintptr
	float bool
}

// I passes an integer or pointer. Convert pointers with
// uintptr(unsafe.Pointer(p)) directly in the call.
//
//go:uintptrescapes
func I(v uintptr) Arg { return Arg{v: v} }

// F passes a float32 by value.
func F(f float32) Arg { return Arg{v: F32(f), float: true} }

// CallArgsHR invokes vtable slot i with this and args, placing float
// arguments as the calling convention requires.
func (u *Unknown) CallArgsHR(op string, i int, args ...Arg) error {
	return u.callArgs(op, i, args)
}
