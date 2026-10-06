package goprint

import (
	"context"
	"fmt"
)

// backend is implemented once per platform (backend_*.go).
type backend interface {
	printers(ctx context.Context) ([]Printer, error)
	capabilities(ctx context.Context, printer string) (Capabilities, error)
	print(ctx context.Context, doc Document, s Settings) (*Job, error)
	dialog(ctx context.Context, doc Document, opts DialogOptions) (*Job, Settings, error)
}

// unsupported is the backend for platforms without (or not yet with) an
// implementation. Every call fails with ErrUnsupported.
type unsupported struct{ goos string }

func (u unsupported) err() error { return fmt.Errorf("%w (%s)", ErrUnsupported, u.goos) }

func (u unsupported) printers(context.Context) ([]Printer, error) { return nil, u.err() }

func (u unsupported) capabilities(context.Context, string) (Capabilities, error) {
	return Capabilities{}, u.err()
}

func (u unsupported) print(context.Context, Document, Settings) (*Job, error) {
	return nil, u.err()
}

func (u unsupported) dialog(context.Context, Document, DialogOptions) (*Job, Settings, error) {
	return nil, Settings{}, u.err()
}
