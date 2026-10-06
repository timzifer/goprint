//go:build windows

package com

import (
	"fmt"
	"io"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modshlwapi = windows.NewLazySystemDLL("shlwapi.dll")
	modshcore  = windows.NewLazySystemDLL("shcore.dll")

	procSHCreateMemStream        = modshlwapi.NewProc("SHCreateMemStream")
	procSHCreateStreamOnFileEx   = modshlwapi.NewProc("SHCreateStreamOnFileEx")
	procCreateStreamOnHGlobal    = modole32.NewProc("CreateStreamOnHGlobal")
	procCreateRandomAccessStream = modshcore.NewProc("CreateRandomAccessStreamOverStream")
)

// IIDIRandomAccessStream is Windows.Storage.Streams.IRandomAccessStream.
var IIDIRandomAccessStream = MustGUID("905A0FE1-BC53-11DF-8C49-001E4FC686DA")

// IStream vtable slots.
const (
	streamRead  = 3
	streamWrite = 4
	streamSeek  = 5
)

// Stream is an IStream.
type Stream struct{ Unknown }

// NewMemStream copies b into a new memory IStream.
func NewMemStream(b []byte) (*Stream, error) {
	if err := procSHCreateMemStream.Find(); err != nil {
		return nil, err
	}
	var p uintptr
	if len(b) > 0 {
		p = uintptr(unsafe.Pointer(&b[0]))
	}
	r, _, _ := syscallN(procSHCreateMemStream.Addr(), p, uintptr(len(b)))
	if r == 0 {
		return nil, &Error{HR: E_FAIL, Op: "SHCreateMemStream"}
	}
	return (*Stream)(ptr(r)), nil
}

// NewHGlobalStream creates an empty, growable memory IStream.
func NewHGlobalStream() (*Stream, error) {
	var s *Unknown
	if err := Call(procCreateStreamOnHGlobal, 0, 1, uintptr(unsafe.Pointer(&s))); err != nil {
		return nil, err
	}
	return (*Stream)(unsafe.Pointer(s)), nil
}

// STGM flags for file streams.
const (
	STGM_READ             = 0x0
	STGM_WRITE            = 0x1
	STGM_READWRITE        = 0x2
	STGM_CREATE           = 0x1000
	STGM_SHARE_DENY_WRITE = 0x20
)

// NewFileStream opens or creates a file as IStream.
func NewFileStream(path string, mode uint32, create bool) (*Stream, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	var c uintptr
	if create {
		c = 1
	}
	var s *Unknown
	if err := Call(procSHCreateStreamOnFileEx, uintptr(unsafe.Pointer(p)), uintptr(mode), windows.FILE_ATTRIBUTE_NORMAL, c, 0, uintptr(unsafe.Pointer(&s))); err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return (*Stream)(unsafe.Pointer(s)), nil
}

// Rewind seeks to the start.
func (s *Stream) Rewind() error {
	return s.CallHR("IStream.Seek", streamSeek, 0, 0 /* STREAM_SEEK_SET */, 0)
}

// Read implements io.Reader.
func (s *Stream) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	var n uint32
	if err := s.CallHR("IStream.Read", streamRead, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), uintptr(unsafe.Pointer(&n))); err != nil {
		return int(n), err
	}
	if n == 0 {
		return 0, io.EOF
	}
	return int(n), nil
}

// Write implements io.Writer.
func (s *Stream) Write(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	var n uint32
	err := s.CallHR("IStream.Write", streamWrite, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), uintptr(unsafe.Pointer(&n)))
	return int(n), err
}

// RandomAccess wraps the stream as a WinRT IRandomAccessStream.
func (s *Stream) RandomAccess() (*Unknown, error) {
	var out *Unknown
	err := Call(procCreateRandomAccessStream, s.Ptr(), 0 /* BSOS_DEFAULT */, uintptr(unsafe.Pointer(&IIDIRandomAccessStream)), uintptr(unsafe.Pointer(&out)))
	return out, err
}
