//go:build !darwin || ios

package goprint

func runMain(f func()) { f() }

func setMainThreadRunner(func(func())) {}
