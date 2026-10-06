//go:build windows

package com

import (
	"encoding/binary"
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// On windows/amd64 the first four arguments travel in RCX, RDX, R8, R9 or,
// for floating-point values, in XMM0-XMM3 by position. syscall.SyscallN only
// sets the integer registers. A tiny thunk copies RCX..R9 into XMM0..XMM3
// and jumps to the target, so float32 arguments passed as their bit pattern
// (see F32) arrive where the callee reads them. Integer arguments are
// unaffected, and stack arguments are left untouched by the jump.

var thunks struct {
	sync.Mutex
	m map[uintptr]uintptr
}

func floatThunk(target uintptr) (uintptr, error) {
	thunks.Lock()
	defer thunks.Unlock()
	if t, ok := thunks.m[target]; ok {
		return t, nil
	}
	code := []byte{
		0x66, 0x48, 0x0F, 0x6E, 0xC1, // movq xmm0, rcx
		0x66, 0x48, 0x0F, 0x6E, 0xCA, // movq xmm1, rdx
		0x66, 0x49, 0x0F, 0x6E, 0xD0, // movq xmm2, r8
		0x66, 0x49, 0x0F, 0x6E, 0xD9, // movq xmm3, r9
		0x48, 0xB8, 0, 0, 0, 0, 0, 0, 0, 0, // mov rax, target
		0xFF, 0xE0, // jmp rax
	}
	binary.LittleEndian.PutUint64(code[22:], uint64(target))
	mem, err := windows.VirtualAlloc(0, uintptr(len(code)), windows.MEM_COMMIT|windows.MEM_RESERVE, windows.PAGE_READWRITE)
	if err != nil {
		return 0, fmt.Errorf("com: allocate float thunk: %w", err)
	}
	// A plain copy into this slice crashes the Go 1.26 compiler
	// ("disjointTypes: one of arguments is not a pointer"); copy by hand.
	dst := unsafe.Slice((*byte)(ptr(mem)), len(code))
	//lint:ignore S1001 copy() triggers the compiler crash described above.
	for i, b := range code {
		dst[i] = b
	}
	var old uint32
	if err := windows.VirtualProtect(mem, uintptr(len(code)), windows.PAGE_EXECUTE_READ, &old); err != nil {
		return 0, fmt.Errorf("com: protect float thunk: %w", err)
	}
	if thunks.m == nil {
		thunks.m = map[uintptr]uintptr{}
	}
	thunks.m[target] = mem
	return mem, nil
}

// CallFloatHR invokes vtable slot i like CallHR but makes float32 arguments
// among the first four parameters (this included) work. Pass floats as
// F32(x).
//
//go:uintptrescapes
func (u *Unknown) CallFloatHR(op string, i int, args ...uintptr) error {
	r, err := callFloat(u.vtbl[i], append([]uintptr{u.Ptr()}, args...)...)
	if err != nil {
		return err
	}
	return HR(op, r)
}

// callFloat calls fn through the float thunk.
//
//go:uintptrescapes
func callFloat(fn uintptr, args ...uintptr) (uintptr, error) {
	t, err := floatThunk(fn)
	if err != nil {
		return 0, err
	}
	r, _, _ := syscall.SyscallN(t, args...)
	return r, nil
}
