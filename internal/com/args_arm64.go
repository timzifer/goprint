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

// On windows/arm64 integer arguments go to x0..x7 and floating-point ones
// to s0..s7, each with its own counter; a struct of up to four floats (an
// HFA such as D2D_SIZE_F) also uses s-registers. syscall.SyscallN only sets
// x-registers. callArgs therefore passes the integers in x0..x6 and packs up
// to two float32 into x7; a small thunk moves them into s0 and s1 and jumps
// to the target:
//
//	fmov s0, w7
//	lsr  x7, x7, #32
//	fmov s1, w7
//	ldr  x16, =target
//	br   x16

var procFlushInstructionCache = windows.NewLazySystemDLL("kernel32.dll").NewProc("FlushInstructionCache")

var arm64Thunks struct {
	sync.Mutex
	m map[uintptr]uintptr
}

func floatThunkARM64(target uintptr) (uintptr, error) {
	arm64Thunks.Lock()
	defer arm64Thunks.Unlock()
	if t, ok := arm64Thunks.m[target]; ok {
		return t, nil
	}
	code := make([]byte, 0, 28)
	for _, insn := range []uint32{
		0x1E2700E0, // fmov s0, w7
		0xD360FCE7, // lsr x7, x7, #32
		0x1E2700E1, // fmov s1, w7
		0x58000050, // ldr x16, #8 (the literal after br)
		0xD61F0200, // br x16
	} {
		code = binary.LittleEndian.AppendUint32(code, insn)
	}
	code = binary.LittleEndian.AppendUint64(code, uint64(target))

	mem, err := windows.VirtualAlloc(0, uintptr(len(code)), windows.MEM_COMMIT|windows.MEM_RESERVE, windows.PAGE_READWRITE)
	if err != nil {
		return 0, fmt.Errorf("com: allocate float thunk: %w", err)
	}
	dst := unsafe.Slice((*byte)(ptr(mem)), len(code))
	//lint:ignore S1001 copy() into VirtualAlloc'd memory trips the Go 1.26.0 compiler (see float_amd64.go).
	for i, b := range code {
		dst[i] = b
	}
	var old uint32
	if err := windows.VirtualProtect(mem, uintptr(len(code)), windows.PAGE_EXECUTE_READ, &old); err != nil {
		return 0, fmt.Errorf("com: protect float thunk: %w", err)
	}
	// ARM requires the instruction cache to see the new code.
	syscall.SyscallN(procFlushInstructionCache.Addr(), uintptr(windows.CurrentProcess()), mem, uintptr(len(code)))
	if arm64Thunks.m == nil {
		arm64Thunks.m = map[uintptr]uintptr{}
	}
	arm64Thunks.m[target] = mem
	return mem, nil
}

func (u *Unknown) callArgs(op string, i int, args []Arg) error {
	ints := []uintptr{u.Ptr()}
	var floats []uintptr
	for _, a := range args {
		if a.float {
			floats = append(floats, a.v)
		} else {
			ints = append(ints, a.v)
		}
	}
	if len(floats) == 0 {
		return u.CallHR(op, i, ints[1:]...)
	}
	if len(floats) > 2 || len(ints) > 7 {
		return fmt.Errorf("com: %s: unsupported argument layout on arm64 (%d ints, %d floats)", op, len(ints), len(floats))
	}
	packed := floats[0]
	if len(floats) == 2 {
		packed |= floats[1] << 32
	}
	regs := make([]uintptr, 8)
	copy(regs, ints)
	regs[7] = packed
	t, err := floatThunkARM64(u.vtbl[i])
	if err != nil {
		return err
	}
	r, _, _ := syscall.SyscallN(t, regs...)
	return HR(op, r)
}
