package goprint

import (
	"bytes"
	"io"

	"github.com/timzifer/goprint/internal/core"
)

// open returns the document as PDF stream. Raster sources are wrapped into
// an image PDF so that every backend has a single path.
func (d Document) open() (io.ReadCloser, error) {
	if d.PDF != nil {
		return d.PDF()
	}
	b, err := core.ImagesToPDF(d.Images, d.DPI)
	if err != nil {
		return nil, invalidf("document: %v", err)
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (s Settings) corePageRanges() []core.PageRange {
	var r []core.PageRange
	for _, p := range s.PageRanges {
		r = append(r, core.PageRange{From: p.From, To: p.To})
	}
	return r
}
