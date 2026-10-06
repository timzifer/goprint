//go:build windows

// Package com calls COM and WinRT interfaces through hand-written vtables and
// implements COM objects in Go, without cgo.
//
// Interface pointers are represented as *Unknown (or types embedding it).
// They point to memory owned by COM, never by Go.
package com

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/timzifer/goprint/internal/errdefs"
)

// GUID is a COM GUID (IID, CLSID).
type GUID = windows.GUID

// MustGUID parses a GUID in "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx" form, with
// or without braces. It panics on malformed input and is meant for package
// level IID/CLSID variables.
func MustGUID(s string) GUID {
	if len(s) > 0 && s[0] != '{' {
		s = "{" + s + "}"
	}
	g, err := windows.GUIDFromString(s)
	if err != nil {
		panic(fmt.Sprintf("com: bad GUID %q: %v", s, err))
	}
	return g
}

// Well-known interface IDs.
var (
	IIDIUnknown     = MustGUID("00000000-0000-0000-C000-000000000046")
	IIDIDispatch    = MustGUID("00020400-0000-0000-C000-000000000046")
	IIDIInspectable = MustGUID("AF86E2E0-B12D-4C6A-9C5A-D7AA65101E90")
	IIDIAgileObject = MustGUID("94EA2B94-E9CC-49E0-C0FF-EE64CA8F5B90")
)

// Unknown is an IUnknown interface pointer. The first word of the pointee is
// the vtable.
type Unknown struct {
	vtbl *[512]uintptr
}

// Ptr returns the interface pointer as uintptr for passing to syscalls.
func (u *Unknown) Ptr() uintptr { return uintptr(unsafe.Pointer(u)) }

// Call invokes vtable slot i with this as first argument and returns the raw
// result. Pointer arguments must be converted with uintptr(unsafe.Pointer(p))
// directly in the call expression so that they are kept alive and on the heap.
//
//go:uintptrescapes
func (u *Unknown) Call(i int, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(u.vtbl[i], append([]uintptr{u.Ptr()}, args...)...)
	return r
}

// CallHR invokes vtable slot i and converts a failing HRESULT into *Error.
// See Call for pointer arguments.
//
//go:uintptrescapes
func (u *Unknown) CallHR(op string, i int, args ...uintptr) error {
	return HR(op, u.Call(i, args...))
}

// AddRef increments the reference count.
func (u *Unknown) AddRef() { u.Call(1) }

// Release decrements the reference count. It is safe on a nil receiver.
func (u *Unknown) Release() {
	if u != nil {
		u.Call(2)
	}
}

// QueryInterface asks for interface iid and returns the new pointer.
func (u *Unknown) QueryInterface(iid *GUID) (*Unknown, error) {
	var out *Unknown
	if err := u.CallHR("QueryInterface", 0, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out))); err != nil {
		return nil, err
	}
	return out, nil
}

// HRESULT values used across the package.
const (
	S_OK          = 0
	S_FALSE       = 1
	E_NOTIMPL     = 0x80004001
	E_NOINTERFACE = 0x80004002
	E_POINTER     = 0x80004003
	E_ABORT       = 0x80004004
	E_FAIL        = 0x80004005
	E_INVALIDARG  = 0x80070057

	hrCancelled    = 0x800704C7 // HRESULT_FROM_WIN32(ERROR_CANCELLED)
	hrNotFound     = 0x80070490 // HRESULT_FROM_WIN32(ERROR_NOT_FOUND)
	hrInvalidPrn   = 0x80070709 // HRESULT_FROM_WIN32(ERROR_INVALID_PRINTER_NAME)
	hrProcNotFound = 0x8007007F // HRESULT_FROM_WIN32(ERROR_PROC_NOT_FOUND)
)

// Error is a failed COM call.
type Error struct {
	HR uint32
	Op string
}

func (e *Error) Error() string {
	msg := windows.Errno(e.HR & 0xFFFF).Error()
	if e.HR>>16 != 0x8007 {
		msg = fmt.Sprintf("HRESULT 0x%08X", e.HR)
	}
	return fmt.Sprintf("%s: %s (0x%08X)", e.Op, msg, e.HR)
}

// Is maps well-known HRESULTs to goprint sentinels.
func (e *Error) Is(target error) bool {
	switch target {
	case errdefs.ErrCanceled:
		return e.HR == hrCancelled || e.HR == E_ABORT
	case errdefs.ErrPrinterNotFound:
		return e.HR == hrInvalidPrn
	case errdefs.ErrUnsupported:
		return e.HR == E_NOTIMPL || e.HR == hrProcNotFound
	}
	return false
}

// HR converts an HRESULT into an error; success codes yield nil.
func HR(op string, hr uintptr) error {
	if int32(hr) >= 0 {
		return nil
	}
	return &Error{HR: uint32(hr), Op: op}
}

// ErrNilPointer is returned when a call succeeded but produced a nil pointer.
var ErrNilPointer = errors.New("com: unexpected nil interface pointer")

var (
	modole32   = windows.NewLazySystemDLL("ole32.dll")
	modcombase = windows.NewLazySystemDLL("combase.dll")

	procCoCreateInstance          = modole32.NewProc("CoCreateInstance")
	procRoInitialize              = modcombase.NewProc("RoInitialize")
	procRoUninitialize            = modcombase.NewProc("RoUninitialize")
	procRoGetActivationFactory    = modcombase.NewProc("RoGetActivationFactory")
	procWindowsCreateString       = modcombase.NewProc("WindowsCreateString")
	procWindowsDeleteString       = modcombase.NewProc("WindowsDeleteString")
	procWindowsGetStringRawBuffer = modcombase.NewProc("WindowsGetStringRawBuffer")
)

// Call calls a lazily loaded DLL procedure and converts the HRESULT. A
// missing procedure (older Windows) yields an error wrapping ErrUnsupported.
// See (*Unknown).Call for pointer arguments.
//
//go:uintptrescapes
func Call(p *windows.LazyProc, args ...uintptr) error {
	if err := p.Find(); err != nil {
		return fmt.Errorf("%w: %s: %v", errdefs.ErrUnsupported, p.Name, err)
	}
	r, _, _ := syscall.SyscallN(p.Addr(), args...)
	return HR(p.Name, r)
}

// CLSCTX values.
const (
	CLSCTX_INPROC_SERVER = 0x1
	CLSCTX_LOCAL_SERVER  = 0x4
	CLSCTX_ALL           = 0x17
)

// CreateInstance calls CoCreateInstance.
func CreateInstance(clsid, iid *GUID, ctx uint32) (*Unknown, error) {
	var out *Unknown
	err := Call(procCoCreateInstance, uintptr(unsafe.Pointer(clsid)), 0, uintptr(ctx), uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out)))
	if err != nil {
		return nil, err
	}
	return out, nil
}

// HString is a WinRT HSTRING.
type HString uintptr

// NewHString creates an HSTRING; free it with Delete.
func NewHString(s string) (HString, error) {
	u, err := windows.UTF16FromString(s)
	if err != nil {
		return 0, err
	}
	var h HString
	err = Call(procWindowsCreateString, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(&h)))
	return h, err
}

// Delete frees the HSTRING. A zero HString is a valid empty string.
func (h HString) Delete() {
	if h != 0 {
		_ = Call(procWindowsDeleteString, uintptr(h))
	}
}

// String returns the Go string.
func (h HString) String() string {
	if h == 0 {
		return ""
	}
	var n uint32
	p, _, _ := syscall.SyscallN(procWindowsGetStringRawBuffer.Addr(), uintptr(h), uintptr(unsafe.Pointer(&n)))
	if p == 0 || n == 0 {
		return ""
	}
	return windows.UTF16ToString(unsafe.Slice((*uint16)(ptr(p)), n))
}

// ActivationFactory calls RoGetActivationFactory for a runtime class.
func ActivationFactory(class string, iid *GUID) (*Unknown, error) {
	h, err := NewHString(class)
	if err != nil {
		return nil, err
	}
	defer h.Delete()
	var out *Unknown
	if err := Call(procRoGetActivationFactory, uintptr(h), uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out))); err != nil {
		return nil, fmt.Errorf("activation factory %s: %w", class, err)
	}
	return out, nil
}

//go:uintptrescapes
func syscallN(fn uintptr, args ...uintptr) (uintptr, uintptr, error) {
	r1, r2, e := syscall.SyscallN(fn, args...)
	return r1, r2, e
}

// ptr converts an address of foreign (non-Go) memory into an unsafe.Pointer.
func ptr(u uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&u)) }

// Ptr is the exported form of ptr for sibling internal packages.
func Ptr(u uintptr) unsafe.Pointer { return ptr(u) }
