//go:build darwin

package goprint

import (
	"fmt"
	"io"
)

// readPDF returns the whole document as PDF, for native dialogs that take
// it in memory (PDFKit, UIKit).
func readPDF(doc Document) ([]byte, error) {
	src, err := doc.open()
	if err != nil {
		return nil, err
	}
	defer src.Close()
	b, err := io.ReadAll(src)
	if err != nil {
		return nil, fmt.Errorf("goprint: reading document: %w", err)
	}
	return b, nil
}

func docTitle(doc Document) string {
	if doc.Title == "" {
		return "Document"
	}
	return doc.Title
}
