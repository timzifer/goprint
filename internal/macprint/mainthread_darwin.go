//go:build darwin

package macprint

import (
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/timzifer/goprint/internal/errdefs"
)

// AppKit must only be used from the process's main thread. Go runs the
// main goroutine on the main thread until init is done; a program keeps it
// there with runtime.LockOSThread in an init function of package main.
//
// RunMain turns such a locked main goroutine into a small work queue:
// while f runs on another goroutine, the main thread executes functions
// that OnMain sends to it.

type mainLoop struct {
	calls chan func()
	done  chan struct{}
}

var (
	loopMu sync.Mutex
	loop   *mainLoop

	// runner executes a function on the main thread for programs whose
	// main thread is owned by a GUI toolkit (see SetRunner).
	runner atomic.Pointer[func(func())]
)

// SetRunner installs run as the way to reach the main thread when no
// RunMain loop serves it. run must execute its argument on the main thread
// and return after it returned. nil removes the runner.
func SetRunner(run func(func())) {
	if run == nil {
		runner.Store(nil)
		return
	}
	runner.Store(&run)
}

// IsMainThread reports whether the calling goroutine currently runs on
// the process's main thread. Without runtime.LockOSThread the answer can
// change at any time.
func IsMainThread() bool {
	if loadMainCheck() != nil {
		return false
	}
	return pthreadMainNP() != 0
}

// RunMain runs f and, while f runs, executes OnMain calls on the calling
// thread. If the caller is not on the main thread it just calls f.
func RunMain(f func()) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if !IsMainThread() {
		f()
		return
	}
	loopMu.Lock()
	if loop != nil {
		// Nested RunMain: the outer loop already serves the main thread.
		loopMu.Unlock()
		f()
		return
	}
	l := &mainLoop{calls: make(chan func()), done: make(chan struct{})}
	loop = l
	loopMu.Unlock()

	go func() {
		defer close(l.done)
		f()
	}()
	for {
		select {
		case c := <-l.calls:
			c()
		case <-l.done:
			loopMu.Lock()
			loop = nil
			loopMu.Unlock()
			return
		}
	}
}

// OnMain runs fn on the main thread: directly if the caller is on it,
// through the RunMain loop or the runner from SetRunner otherwise. Without
// either it returns ErrWrongThread and does not call fn.
func OnMain(fn func()) error {
	// Pin the goroutine so it cannot migrate off the main thread between
	// the check and the end of fn.
	runtime.LockOSThread()
	if IsMainThread() {
		defer runtime.UnlockOSThread()
		fn()
		return nil
	}
	runtime.UnlockOSThread()

	loopMu.Lock()
	l := loop
	loopMu.Unlock()
	if l == nil {
		return onMainRunner(fn)
	}
	finished := make(chan struct{})
	select {
	case l.calls <- func() { defer close(finished); fn() }:
	case <-l.done:
		return errdefs.ErrWrongThread
	}
	<-finished
	return nil
}

// onMainRunner runs fn through the installed runner. A runner that does not
// reach the main thread yields ErrWrongThread instead of calling fn.
func onMainRunner(fn func()) error {
	r := runner.Load()
	if r == nil {
		return errdefs.ErrWrongThread
	}
	ran := false
	(*r)(func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if !IsMainThread() {
			return
		}
		ran = true
		fn()
	})
	if !ran {
		return errdefs.ErrWrongThread
	}
	return nil
}
