package fyneprint

import (
	"context"
	"errors"
	"runtime"
	"syscall"
	"testing"
	"unsafe"

	"github.com/timzifer/goprint"
)

var (
	procCreateWindowExW = user32.NewProc("CreateWindowExW")
	procDestroyWindow   = user32.NewProc("DestroyWindow")
)

func TestDialogOnOwnerThread(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	class, _ := syscall.UTF16PtrFromString("STATIC")
	hwnd, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(class)), 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	if hwnd == 0 {
		t.Fatalf("CreateWindowExW: %v", err)
	}
	defer procDestroyWindow.Call(hwnd)

	called := false
	stubDialog(t, func(context.Context, goprint.Document, goprint.DialogOptions) (*goprint.Job, goprint.Settings, error) {
		called = true
		return nil, goprint.Settings{}, nil
	})
	if _, _, err := Dialog(context.Background(), nil, goprint.Document{}, goprint.DialogOptions{Owner: hwnd}); !errors.Is(err, goprint.ErrWrongThread) {
		t.Fatalf("Dialog on the owner's thread = %v, want ErrWrongThread", err)
	}
	if called {
		t.Error("dialog was started on the owner's thread")
	}

	// From another thread the same owner is fine.
	errc := make(chan error)
	go func() {
		_, _, err := Dialog(context.Background(), nil, goprint.Document{}, goprint.DialogOptions{Owner: hwnd})
		errc <- err
	}()
	if err := <-errc; err != nil || !called {
		t.Fatalf("Dialog from another thread = %v (called %v)", err, called)
	}
}
