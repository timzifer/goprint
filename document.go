package goprint

import (
	"bytes"
	"io"
	"os"
	"path/filepath"

	"github.com/timzifer/goprint/internal/core"
)

// PDFFile returns a Document that reads the PDF file at path. The title is
// the file name; set Title on the result to change it. The file is opened
// when the document is printed, not here.
func PDFFile(path string) Document {
	return Document{
		Title: filepath.Base(path),
		PDF:   func() (io.ReadSeekCloser, error) { return os.Open(path) },
	}
}

// PDFBytes returns a Document for a PDF held in memory. data must not be
// modified while the document is in use.
func PDFBytes(title string, data []byte) Document {
	return Document{
		Title: title,
		PDF:   func() (io.ReadSeekCloser, error) { return nopSeekCloser{bytes.NewReader(data)}, nil },
	}
}

type nopSeekCloser struct{ *bytes.Reader }

func (nopSeekCloser) Close() error { return nil }

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
