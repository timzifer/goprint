//go:build windows

package com

// Int64Args passes a 64-bit integer by value (LARGE_INTEGER, INT64): two
// stack words on 386, low word first.
func Int64Args(v int64) []uintptr {
	return []uintptr{uintptr(uint32(v)), uintptr(uint32(uint64(v) >> 32))}
}
