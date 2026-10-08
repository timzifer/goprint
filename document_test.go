package goprint

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func readDoc(t *testing.T, d Document) []byte {
	t.Helper()
	if err := d.validate(); err != nil {
		t.Fatal(err)
	}
	// Every consumer gets a fresh reader.
	var first []byte
	for i := 0; i < 2; i++ {
		r, err := d.open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = b
		} else if !bytes.Equal(b, first) {
			t.Fatal("second open returned different content")
		}
	}
	return first
}

func TestPDFFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.4 test"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := PDFFile(path)
	if d.Title != "report.pdf" {
		t.Errorf("Title = %q", d.Title)
	}
	if got := readDoc(t, d); string(got) != "%PDF-1.4 test" {
		t.Errorf("content = %q", got)
	}
	if _, err := PDFFile(filepath.Join(t.TempDir(), "missing.pdf")).open(); !os.IsNotExist(err) {
		t.Errorf("missing file: err = %v", err)
	}
}

func TestPDFBytes(t *testing.T) {
	d := PDFBytes("Invoice", []byte("%PDF-1.7"))
	if d.Title != "Invoice" {
		t.Errorf("Title = %q", d.Title)
	}
	if got := readDoc(t, d); string(got) != "%PDF-1.7" {
		t.Errorf("content = %q", got)
	}
}
