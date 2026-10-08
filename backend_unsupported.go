//go:build !(linux || freebsd || openbsd || netbsd || dragonfly || darwin || windows)

package goprint

import (
	"context"
	"fmt"
	"io"
)

// unsupported is the backend for platforms without (or not yet with) an
// implementation. Every call fails with ErrUnsupported.
type unsupported struct{ goos string }

func (u unsupported) err() error { return fmt.Errorf("%w (%s)", ErrUnsupported, u.goos) }

func (u unsupported) printers(context.Context) ([]Printer, error) { return nil, u.err() }

func (u unsupported) capabilities(context.Context, string) (Capabilities, error) {
	return Capabilities{}, u.err()
}

func (u unsupported) print(context.Context, io.Reader, Document, Settings) (*Job, error) {
	return nil, u.err()
}

func (u unsupported) dialog(context.Context, Document, DialogOptions) (*Job, Settings, error) {
	return nil, Settings{}, u.err()
}

func (u unsupported) properties(context.Context, Settings, uintptr) (Settings, error) {
	return Settings{}, u.err()
}
