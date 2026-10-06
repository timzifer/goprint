//go:build windows

package com

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procRegisterClassExW = moduser32.NewProc("RegisterClassExW")
	procCreateWindowExW  = moduser32.NewProc("CreateWindowExW")
	procDestroyWindow    = moduser32.NewProc("DestroyWindow")
	procDefWindowProcW   = moduser32.NewProc("DefWindowProcW")
	procShowWindow       = moduser32.NewProc("ShowWindow")
	procGetSystemMetrics = moduser32.NewProc("GetSystemMetrics")
	procSetForeground    = moduser32.NewProc("SetForegroundWindow")
	procSetLayeredAttrs  = moduser32.NewProc("SetLayeredWindowAttributes")

	helperClass struct {
		once sync.Once
		name *uint16
		err  error
	}
)

type wndClassEx struct {
	size       uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   windows.Handle
	icon       windows.Handle
	cursor     windows.Handle
	background windows.Handle
	menuName   *uint16
	className  *uint16
	iconSm     windows.Handle
}

// HelperWindow creates a top-level window owned by the calling thread, used
// as owner for dialogs when the application provides none. It sits in the
// middle of the primary screen, is fully transparent and is brought to the
// foreground, so that dialogs it owns (the print dialog, and e.g. the save
// dialog of "Microsoft Print to PDF") open centered and in front instead of
// behind other windows. It uses DefWindowProcW, so no Go callback is
// involved. Destroy it on the same thread with DestroyWindow.
func HelperWindow(title string) (windows.HWND, error) {
	helperClass.once.Do(func() {
		helperClass.name = windows.StringToUTF16Ptr("goprintHelperWindow")
		var inst windows.Handle
		if err := windows.GetModuleHandleEx(0, nil, &inst); err != nil {
			helperClass.err = err
			return
		}
		wc := wndClassEx{wndProc: procDefWindowProcW.Addr(), instance: inst, className: helperClass.name}
		wc.size = uint32(unsafe.Sizeof(wc))
		if r, _, e := syscall.SyscallN(procRegisterClassExW.Addr(), uintptr(unsafe.Pointer(&wc))); r == 0 {
			helperClass.err = fmt.Errorf("RegisterClassExW: %w", e)
		}
	})
	if helperClass.err != nil {
		return 0, helperClass.err
	}
	t, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return 0, err
	}
	const (
		wsExToolWindow = 0x00000080
		wsExLayered    = 0x00080000
		wsPopup        = 0x80000000
		smCxScreen     = 0
		smCyScreen     = 1
		lwaAlpha       = 0x2
		swShow         = 5
	)
	cx, _, _ := syscall.SyscallN(procGetSystemMetrics.Addr(), smCxScreen)
	cy, _, _ := syscall.SyscallN(procGetSystemMetrics.Addr(), smCyScreen)
	h, _, e := syscall.SyscallN(procCreateWindowExW.Addr(), wsExToolWindow|wsExLayered,
		uintptr(unsafe.Pointer(helperClass.name)), uintptr(unsafe.Pointer(t)), wsPopup,
		cx/2, cy/2, 1, 1, 0, 0, 0, 0)
	if h == 0 {
		return 0, fmt.Errorf("CreateWindowExW: %w", e)
	}
	syscall.SyscallN(procSetLayeredAttrs.Addr(), h, 0, 0, lwaAlpha) // fully transparent
	syscall.SyscallN(procShowWindow.Addr(), h, swShow)
	syscall.SyscallN(procSetForeground.Addr(), h)
	return windows.HWND(h), nil
}

// DestroyWindow destroys a window created on the calling thread.
func DestroyWindow(h windows.HWND) {
	syscall.SyscallN(procDestroyWindow.Addr(), uintptr(h))
}
