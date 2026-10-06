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
// block in LocalAlloc'd memory holding one vtable pointer per implemented
// interface; each interface pointer's address is a key into a Go-side
// registry that holds the implementation and the shared reference count. No
// Go pointer is ever handed to foreign code.
//
// Every object aggregates the COM free-threaded marshaler, so it is agile:
// it may be called from any thread and passed between apartments without
// proxies. Implementations must therefore be safe for concurrent use.

// Interface describes one interface of a Go-implemented object: its vtable
// and the IIDs QueryInterface answers with this interface pointer.
type Interface struct {
	VT   *VTable
	IIDs []GUID
}

type objEntry struct {
	impl   any
	base   uintptr   // address of the block (first interface)
	ifaces []uintptr // interface pointer per Interface
	iids   [][]GUID
	refs   int32
	ftm    *Unknown // inner IUnknown of the free-threaded marshaler
}

var (
	objMu  sync.Mutex
	objMap = map[uintptr]*objEntry{}

	cbQueryInterface = syscall.NewCallback(objQueryInterface)
	cbAddRef         = syscall.NewCallback(objAddRef)
	cbRelease        = syscall.NewCallback(objRelease)

	cbGetIids             = syscall.NewCallback(inspGetIids)
	cbGetRuntimeClassName = syscall.NewCallback(inspGetRuntimeClassName)
	cbGetTrustLevel       = syscall.NewCallback(inspGetTrustLevel)

	procCoCreateFreeThreadedMarshaler = modole32.NewProc("CoCreateFreeThreadedMarshaler")

	// IIDIMarshal is answered by delegating to the free-threaded marshaler.
	IIDIMarshal = MustGUID("00000003-0000-0000-C000-000000000046")
)

// TraceQI, if set, is called for every QueryInterface on a Go-implemented
// object that is answered with E_NOINTERFACE (debugging).
var TraceQI func(this uintptr, iid GUID)

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

// NewInspectableVTable builds a vtable for an IInspectable-derived
// interface; GetIids, GetRuntimeClassName and GetTrustLevel are provided.
func NewInspectableVTable(methods ...uintptr) *VTable {
	return NewVTable(append([]uintptr{cbGetIids, cbGetRuntimeClassName, cbGetTrustLevel}, methods...)...)
}

// Method wraps syscall.NewCallback for readability at vtable construction.
func Method(fn any) uintptr { return syscall.NewCallback(fn) }

// NewObject creates a single-interface COM object with reference count 1
// that answers QueryInterface for IUnknown, IAgileObject, IMarshal and iids.
// Lookup(this) returns impl.
func NewObject(vt *VTable, impl any, iids ...GUID) (*Unknown, error) {
	ps, err := NewMultiObject(impl, Interface{VT: vt, IIDs: iids})
	if err != nil {
		return nil, err
	}
	return ps[0], nil
}

// NewMultiObject creates a COM object implementing several interfaces with
// one shared reference count (initially 1). It returns the interface
// pointers in the order of ifaces; IUnknown resolves to the first.
func NewMultiObject(impl any, ifaces ...Interface) ([]*Unknown, error) {
	word := unsafe.Sizeof(uintptr(0))
	mem, err := windows.LocalAlloc(windows.LPTR, uint32(uintptr(len(ifaces))*word))
	if err != nil {
		return nil, err
	}
	e := &objEntry{impl: impl, base: mem, refs: 1}
	out := make([]*Unknown, len(ifaces))
	for i, it := range ifaces {
		addr := mem + uintptr(i)*word
		*(*uintptr)(ptr(addr)) = it.VT.addr
		e.ifaces = append(e.ifaces, addr)
		e.iids = append(e.iids, it.IIDs)
		out[i] = (*Unknown)(ptr(addr))
	}
	objMu.Lock()
	for _, a := range e.ifaces {
		objMap[a] = e
	}
	objMu.Unlock()

	// Aggregate the free-threaded marshaler. Creating it AddRefs and
	// Releases the outer object, which is consistent at this point.
	var ftm *Unknown
	if err := Call(procCoCreateFreeThreadedMarshaler, mem, uintptr(unsafe.Pointer(&ftm))); err == nil {
		objMu.Lock()
		e.ftm = ftm
		objMu.Unlock()
	}
	return out, nil
}

// Lookup returns the Go implementation behind a Go-implemented object's
// interface pointer, or nil if this is not one of ours (or released).
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
	e := objMap[this]
	if e == nil {
		objMu.Unlock()
		return E_FAIL
	}
	var found uintptr
	switch iid {
	case IIDIUnknown, IIDIAgileObject:
		found = e.ifaces[0]
	default:
		for i, ids := range e.iids {
			for _, id := range ids {
				if id == iid {
					found = e.ifaces[i]
					break
				}
			}
			if found != 0 {
				break
			}
		}
	}
	if found != 0 {
		e.refs++
		objMu.Unlock()
		*pout = found
		return S_OK
	}
	ftm := e.ftm
	objMu.Unlock()
	if iid == IIDIMarshal && ftm != nil {
		// The marshaler AddRefs us (its outer object) through our AddRef.
		return ftm.Call(0, riid, out)
	}
	if f := TraceQI; f != nil {
		f(this, iid)
	}
	return E_NOINTERFACE
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
		for _, a := range e.ifaces {
			delete(objMap, a)
		}
	}
	objMu.Unlock()
	if refs > 0 {
		return uintptr(refs)
	}
	// Guard against re-entry from the marshaler's own release.
	e.refs = 1 << 30
	if e.ftm != nil {
		e.ftm.Release()
	}
	_, _ = windows.LocalFree(windows.Handle(e.base))
	if c, ok := e.impl.(interface{ Destroy() }); ok {
		c.Destroy()
	}
	return 0
}

func inspGetIids(this, count, iids uintptr) uintptr {
	if count != 0 {
		*(*uint32)(ptr(count)) = 0
	}
	if iids != 0 {
		*(*uintptr)(ptr(iids)) = 0
	}
	return S_OK
}

func inspGetRuntimeClassName(this, name uintptr) uintptr {
	if name != 0 {
		*(*uintptr)(ptr(name)) = 0
	}
	return S_OK
}

func inspGetTrustLevel(this, level uintptr) uintptr {
	if level != 0 {
		*(*int32)(ptr(level)) = 0 // BaseTrust
	}
	return S_OK
}

// liveObjects reports the number of live Go-implemented objects (tests).
func liveObjects() int {
	objMu.Lock()
	defer objMu.Unlock()
	return len(objMap)
}
