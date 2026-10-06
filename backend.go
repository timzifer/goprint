package goprint

import (
	"context"
	"io"
)

// backend is implemented once per platform (backend_*.go).
type backend interface {
	printers(ctx context.Context) ([]Printer, error)
	capabilities(ctx context.Context, printer string) (Capabilities, error)
	print(ctx context.Context, src io.Reader, doc Document, s Settings) (*Job, error)
	dialog(ctx context.Context, doc Document, opts DialogOptions) (*Job, Settings, error)
}
