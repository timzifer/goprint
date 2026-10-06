//go:build windows

package com

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ApartmentKind selects the COM threading model of an Apartment.
type ApartmentKind int

const (
	// STA is a single-threaded apartment with a message loop (dialogs).
	STA ApartmentKind = iota
	// MTA is the multi-threaded apartment (headless work).
	MTA
)

var (
	moduser32 = windows.NewLazySystemDLL("user32.dll")

	procGetMessageW        = moduser32.NewProc("GetMessageW")
	procPeekMessageW       = moduser32.NewProc("PeekMessageW")
	procTranslateMessage   = moduser32.NewProc("TranslateMessage")
	procDispatchMessageW   = moduser32.NewProc("DispatchMessageW")
	procPostThreadMessageW = moduser32.NewProc("PostThreadMessageW")
	procMsgWaitForMultiple = moduser32.NewProc("MsgWaitForMultipleObjectsEx")
)

const (
	wmQuit     = 0x0012
	wmApp      = 0x8000
	pmNoRemove = 0x0000
	pmRemove   = 0x0001
	qsAllInput = 0x04FF
)

type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      struct{ x, y int32 }
	private uint32
}

// Apartment is a dedicated OS thread initialized for COM and WinRT. Work is
// sent to it with Do; callers may be on any goroutine.
type Apartment struct {
	kind     ApartmentKind
	threadID uint32

	mu     sync.Mutex
	queue  []func()
	closed bool
	done   chan struct{}
}

// NewApartment starts the apartment thread.
func NewApartment(kind ApartmentKind) (*Apartment, error) {
	a := &Apartment{kind: kind, done: make(chan struct{})}
	ready := make(chan error, 1)
	go a.run(ready)
	if err := <-ready; err != nil {
		return nil, err
	}
	return a, nil
}

func (a *Apartment) run(ready chan<- error) {
	runtime.LockOSThread()
	defer close(a.done)

	roInit := uintptr(1) // RO_INIT_MULTITHREADED
	if a.kind == STA {
		roInit = 0 // RO_INIT_SINGLETHREADED
	}
	if err := Call(procRoInitialize, roInit); err != nil {
		runtime.UnlockOSThread()
		ready <- fmt.Errorf("RoInitialize: %w", err)
		return
	}
	defer syscall.SyscallN(procRoUninitialize.Addr())

	// Force creation of the thread's message queue before publishing the id.
	var m msg
	syscall.SyscallN(procPeekMessageW.Addr(), uintptr(unsafe.Pointer(&m)), 0, wmApp, wmApp, pmNoRemove)
	a.threadID = windows.GetCurrentThreadId()
	ready <- nil

	for {
		r, _, _ := syscall.SyscallN(procGetMessageW.Addr(), uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 { // WM_QUIT or error
			a.drain()
			return
		}
		if m.hwnd == 0 && m.message == wmApp {
			a.drain()
			continue
		}
		syscall.SyscallN(procTranslateMessage.Addr(), uintptr(unsafe.Pointer(&m)))
		syscall.SyscallN(procDispatchMessageW.Addr(), uintptr(unsafe.Pointer(&m)))
	}
}

func (a *Apartment) drain() {
	for {
		a.mu.Lock()
		q := a.queue
		a.queue = nil
		a.mu.Unlock()
		if len(q) == 0 {
			return
		}
		for _, f := range q {
			f()
		}
	}
}

// ErrApartmentClosed is returned by Do after Close.
var ErrApartmentClosed = errors.New("com: apartment closed")

// Do runs f on the apartment thread and waits for it. Panics in f are
// recovered and returned as errors. If ctx is done before f starts, f does
// not run; once started, f runs to completion and should watch ctx itself.
func (a *Apartment) Do(ctx context.Context, f func() error) error {
	res := make(chan error, 1)
	started := make(chan struct{})
	var canceled bool
	var cmu sync.Mutex
	job := func() {
		cmu.Lock()
		skip := canceled
		if !skip {
			close(started)
		}
		cmu.Unlock()
		if skip {
			return
		}
		res <- protect(f)
	}

	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return ErrApartmentClosed
	}
	a.queue = append(a.queue, job)
	a.mu.Unlock()
	syscall.SyscallN(procPostThreadMessageW.Addr(), uintptr(a.threadID), wmApp, 0, 0)

	select {
	case err := <-res:
		return err
	case <-ctx.Done():
		cmu.Lock()
		select {
		case <-started:
			cmu.Unlock()
			return <-res
		default:
			canceled = true
			cmu.Unlock()
			return ctx.Err()
		}
	}
}

// Close stops the apartment after queued work has run.
func (a *Apartment) Close() {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	a.closed = true
	a.mu.Unlock()
	syscall.SyscallN(procPostThreadMessageW.Addr(), uintptr(a.threadID), wmQuit, 0, 0)
	<-a.done
}

func protect(f func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("com: panic on apartment thread: %v", r)
		}
	}()
	return f()
}

// Pump dispatches pending window messages of the current thread (except
// apartment work, which must not re-enter) and then waits up to ms
// milliseconds for new input. Use it in wait loops on an STA.
func Pump(ms uint32) {
	var m msg
	for _, r := range [][2]uintptr{{0, wmApp - 1}, {wmApp + 1, 0xFFFFFFFF}} {
		for {
			ok, _, _ := syscall.SyscallN(procPeekMessageW.Addr(), uintptr(unsafe.Pointer(&m)), 0, r[0], r[1], pmRemove)
			if ok == 0 {
				break
			}
			syscall.SyscallN(procTranslateMessage.Addr(), uintptr(unsafe.Pointer(&m)))
			syscall.SyscallN(procDispatchMessageW.Addr(), uintptr(unsafe.Pointer(&m)))
		}
	}
	syscall.SyscallN(procMsgWaitForMultiple.Addr(), 0, 0, uintptr(ms), qsAllInput, 0)
}
