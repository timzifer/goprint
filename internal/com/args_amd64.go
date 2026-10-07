//go:build windows

package com

func (u *Unknown) callArgs(op string, i int, args []Arg) error {
	flat := make([]uintptr, len(args))
	for k, a := range args {
		flat[k] = a.v
	}
	return u.CallFloatHR(op, i, flat...)
}
