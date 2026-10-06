//go:build windows

package com

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
	"unsafe"

	"github.com/timzifer/goprint/internal/errdefs"
)

var iidTest = MustGUID("6B1D3C2A-1111-4C4C-9A9A-0123456789AB")

type testImpl struct {
	calls     int
	destroyed bool
}

func (t *testImpl) Destroy() { t.destroyed = true }

var testVTable = NewVTable(
	Method(func(this, a uintptr) (hr uintptr) {
		defer Guard(&hr)
		impl := Lookup(this).(*testImpl)
		impl.calls++
		if a == 42 {
			panic("boom")
		}
		return uintptr(impl.calls)
	}),
)

func TestObjectLifecycle(t *testing.T) {
	impl := &testImpl{}
	before := liveObjects()
	obj, err := NewObject(testVTable, impl, iidTest)
	if err != nil {
		t.Fatal(err)
	}
	if got := obj.Call(3, 1); got != 1 {
		t.Fatalf("method returned %d, want 1", got)
	}
	if got := obj.Call(3, 42); got != E_FAIL {
		t.Fatalf("panicking method returned 0x%X, want E_FAIL", got)
	}

	q, err := obj.QueryInterface(&iidTest)
	if err != nil || q != obj {
		t.Fatalf("QueryInterface(iidTest) = %p, %v", q, err)
	}
	if _, err := obj.QueryInterface(&IIDIDispatch); !errors.As(err, new(*Error)) {
		t.Fatalf("QueryInterface(IDispatch) err = %v, want E_NOINTERFACE", err)
	}
	q.Release()
	obj.Release()
	if !impl.destroyed {
		t.Fatal("object not destroyed after final Release")
	}
	if liveObjects() != before {
		t.Fatalf("live objects %d, want %d", liveObjects(), before)
	}
}

func TestErrorIs(t *testing.T) {
	if !errors.Is(&Error{HR: hrCancelled}, errdefs.ErrCanceled) {
		t.Error("ERROR_CANCELLED is not ErrCanceled")
	}
	if !errors.Is(&Error{HR: E_ABORT}, errdefs.ErrCanceled) {
		t.Error("E_ABORT is not ErrCanceled")
	}
	if errors.Is(&Error{HR: E_FAIL}, errdefs.ErrCanceled) {
		t.Error("E_FAIL is ErrCanceled")
	}
	if HR("x", 0) != nil || HR("x", S_FALSE) != nil {
		t.Error("success HRESULT yields error")
	}
}

func TestApartment(t *testing.T) {
	for _, kind := range []ApartmentKind{STA, MTA} {
		a, err := NewApartment(kind)
		if err != nil {
			t.Fatal(err)
		}
		h, err := NewHString("hällo")
		if err != nil {
			t.Fatal(err)
		}
		var got string
		if err := a.Do(context.Background(), func() error { got = h.String(); return nil }); err != nil {
			t.Fatal(err)
		}
		h.Delete()
		if got != "hällo" {
			t.Errorf("HString round trip = %q", got)
		}
		if err := a.Do(context.Background(), func() error { panic("x") }); err == nil {
			t.Error("panic not converted to error")
		}
		ctx, cancel := context.WithCancel(context.Background())
		block := make(chan struct{})
		go func() { _ = a.Do(context.Background(), func() error { <-block; return nil }) }()
		time.Sleep(10 * time.Millisecond)
		cancel()
		if err := a.Do(ctx, func() error { t.Error("canceled work ran"); return nil }); !errors.Is(err, context.Canceled) {
			t.Errorf("Do(canceled) = %v", err)
		}
		close(block)
		a.Close()
		if err := a.Do(context.Background(), func() error { return nil }); !errors.Is(err, ErrApartmentClosed) {
			t.Errorf("Do after Close = %v", err)
		}
	}
}

func TestStreams(t *testing.T) {
	s, err := NewMemStream([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Release()
	b, err := io.ReadAll(s)
	if err != nil || string(b) != "hello" {
		t.Fatalf("ReadAll = %q, %v", b, err)
	}
	h, err := NewHGlobalStream()
	if err != nil {
		t.Fatal(err)
	}
	defer h.Release()
	if _, err := h.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if err := h.Rewind(); err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(h)
	if string(b) != "abc" {
		t.Fatalf("HGlobal stream = %q", b)
	}
	ra, err := s.RandomAccess()
	if err != nil {
		t.Fatal(err)
	}
	ra.Release()
	_ = unsafe.Sizeof(0)
}
