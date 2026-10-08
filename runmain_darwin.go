//go:build darwin

package goprint

import "github.com/timzifer/goprint/internal/macprint"

func runMain(f func()) { macprint.RunMain(f) }

func setMainThreadRunner(run func(func())) { macprint.SetRunner(run) }
