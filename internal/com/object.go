//go:build windows

package com

import (
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Go-implemented COM objects.
//
// syscall.NewCallback has a small process-wide limit and callbacks are never
// freed, so callbacks exist once per vtable slot (created when the VTable is
// built, typically at package init) and never per object. An object is a
// two-word block in LocalAlloc'd memory, {vtbl, 0}; its address ("this") is
// the key into a Go-side registry that holds the implementation and the
// reference count. No Go pointer is ever handed to foreign code.

type objEntry struct {
	impl any
	iids []GUID
	refs int32
}

var (
	objMu  sync.Mutex
	objMap = map[uintptr]*objEntry{}

	cbQueryInterface = syscall.NewCallback(objQueryInterface)
	cbAddRef         = syscall.NewCallback(objAddRef)
	cbRelease        = syscall.NewCallback(objRelease)
)

// VTable is a vtable in non-Go memory. Slots 0-2 are the shared IUnknown
// implementation; methods follow in declaration order.
type VTable struct {
	addr uintptr
}

// NewVTable builds a vtable from callback addresses for the slots after
// IUnknown. Each method must come from syscall.NewCallback (see Method).
// VTables live for the life of the process.
func NewVTable(methods ...uintptr) *VTable {
	slots := append([]uintptr{cbQueryInterface, cbAddRef, cbRelease}, methods...)
	size := uintptr(len(slots)) * unsafe.Sizeof(uintptr(0))
	mem, err := windows.LocalAlloc(windows.LPTR, uint32(size))
	if err != nil {
		panic("com: LocalAlloc for vtable: " + err.Error())
	}
	copy(unsafe.Slice((*uintptr)(ptr(mem)), len(slots)), slots)
	return &VTable{addr: mem}
}

// Method wraps syscall.NewCallback for readability at vtable construction.
func Method(fn any) uintptr { return syscall.NewCallback(fn) }

// NewObject creates a COM object with reference count 1 that answers
// QueryInterface for IUnknown and iids. Lookup(this) returns impl.
func NewObject(vt *VTable, impl any, iids ...GUID) (*Unknown, error) {
	mem, err := windows.LocalAlloc(windows.LPTR, uint32(2*unsafe.Sizeof(uintptr(0))))
	if err != nil {
		return nil, err
	}
	*(*uintptr)(ptr(mem)) = vt.addr
	objMu.Lock()
	objMap[mem] = &objEntry{impl: impl, iids: iids, refs: 1}
	objMu.Unlock()
	return (*Unknown)(ptr(mem)), nil
}

// Lookup returns the Go implementation behind a Go-implemented object, or
// nil if this is not one of ours (or already released).
func Lookup(this uintptr) any {
	objMu.Lock()
	defer objMu.Unlock()
	if e := objMap[this]; e != nil {
		return e.impl
	}
	return nil
}

// Guard recovers a panic in a callback and turns it into E_FAIL. Use as
// `defer com.Guard(&hr)` with a named result; a panic must never unwind
// through a foreign stack.
func Guard(hr *uintptr) {
	if r := recover(); r != nil {
		*hr = E_FAIL
	}
}

func objQueryInterface(this, riid, out uintptr) (hr uintptr) {
	defer Guard(&hr)
	if out == 0 {
		return E_POINTER
	}
	pout := (*uintptr)(ptr(out))
	*pout = 0
	if riid == 0 {
		return E_POINTER
	}
	iid := *(*GUID)(ptr(riid))
	objMu.Lock()
	defer objMu.Unlock()
	e := objMap[this]
	if e == nil {
		return E_FAIL
	}
	ok := iid == IIDIUnknown || iid == IIDIAgileObject
	for i := 0; !ok && i < len(e.iids); i++ {
		ok = e.iids[i] == iid
	}
	if !ok {
		return E_NOINTERFACE
	}
	e.refs++
	*pout = this
	return S_OK
}

func objAddRef(this uintptr) (n uintptr) {
	defer func() { _ = recover() }()
	objMu.Lock()
	defer objMu.Unlock()
	if e := objMap[this]; e != nil {
		e.refs++
		return uintptr(e.refs)
	}
	return 0
}

func objRelease(this uintptr) (n uintptr) {
	defer func() { _ = recover() }()
	objMu.Lock()
	e := objMap[this]
	if e == nil {
		objMu.Unlock()
		return 0
	}
	e.refs--
	refs := e.refs
	if refs <= 0 {
		delete(objMap, this)
	}
	objMu.Unlock()
	if refs <= 0 {
		_, _ = windows.LocalFree(windows.Handle(this))
		if c, ok := e.impl.(interface{ Destroy() }); ok {
			c.Destroy()
		}
		return 0
	}
	return uintptr(refs)
}

// liveObjects reports the number of live Go-implemented objects (tests).
func liveObjects() int {
	objMu.Lock()
	defer objMu.Unlock()
	return len(objMap)
}
