//go:build windows

package com

import "math"

// F32 passes a float32 by value through a uintptr argument (see CallFloatHR).
func F32(f float32) uintptr { return uintptr(math.Float32bits(f)) }
