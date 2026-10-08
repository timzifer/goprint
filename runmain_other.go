//go:build !darwin

package goprint

func runMain(f func()) { f() }

func setMainThreadRunner(func(func())) {}
