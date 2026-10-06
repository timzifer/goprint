//go:build windows

package com

import (
	"math"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TestFloatThunk calls _gcvt(double value, int digits, char *buf), which
// reads value from XMM0: only correct with the thunk.
func TestFloatThunk(t *testing.T) {
	gcvt := windows.NewLazySystemDLL("ucrtbase.dll").NewProc("_gcvt")
	if err := gcvt.Find(); err != nil {
		t.Skip(err)
	}
	buf := make([]byte, 32)
	if _, err := callFloat(gcvt.Addr(), uintptr(math.Float64bits(1234.5)), 6, uintptr(unsafe.Pointer(&buf[0]))); err != nil {
		t.Fatal(err)
	}
	if got := windows.BytePtrToString(&buf[0]); got != "1234.5" {
		t.Fatalf("_gcvt via thunk = %q, want 1234.5", got)
	}
}
