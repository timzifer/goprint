//go:build windows

package com

// Int64Args passes a 64-bit integer by value (LARGE_INTEGER, INT64): one
// register on 64-bit platforms.
func Int64Args(v int64) []uintptr { return []uintptr{uintptr(v)} }
