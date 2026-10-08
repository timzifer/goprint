package goprint

import (
	"bytes"
	"context"
	"errors"
	"image"
	"io"
	"testing"
)

func pdfSource() (io.ReadSeekCloser, error) {
	return nopCloser{bytes.NewReader([]byte("%PDF-1.7\n"))}, nil
}

type nopCloser struct{ io.ReadSeeker }

func (nopCloser) Close() error { return nil }

func TestDocumentValidate(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 1, 1))
	tests := []struct {
		name string
		doc  Document
		ok   bool
	}{
		{"pdf", Document{PDF: pdfSource}, true},
		{"images", Document{Images: []image.Image{img}, DPI: 300}, true},
		{"empty", Document{}, false},
		{"both", Document{PDF: pdfSource, Images: []image.Image{img}, DPI: 300}, false},
		{"no dpi", Document{Images: []image.Image{img}}, false},
		{"nil image", Document{Images: []image.Image{nil}, DPI: 300}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.doc.validate()
			if (err == nil) != tt.ok {
				t.Fatalf("validate() = %v, want ok=%v", err, tt.ok)
			}
			if err != nil && !errors.Is(err, ErrInvalid) {
				t.Fatalf("validate() = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestSettingsValidate(t *testing.T) {
	tests := []struct {
		name string
		s    Settings
		ok   bool
	}{
		{"zero", Settings{}, true},
		{"full", Settings{Copies: 2, PageRanges: []PageRange{{1, 3}, {5, 0}}, Media: MediaA4}, true},
		{"negative copies", Settings{Copies: -1}, false},
		{"page zero", Settings{PageRanges: []PageRange{{0, 2}}}, false},
		{"reversed range", Settings{PageRanges: []PageRange{{3, 2}}}, false},
		{"bad media", Settings{Media: Media{Name: "a4"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.s.validate(); (err == nil) != tt.ok {
				t.Fatalf("validate() = %v, want ok=%v", err, tt.ok)
			}
		})
	}
}

func TestPrintRejectsInvalidInput(t *testing.T) {
	if _, err := Print(context.Background(), Document{}, Settings{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Print(empty doc) = %v, want ErrInvalid", err)
	}
	_, _, err := Dialog(context.Background(), Document{PDF: pdfSource}, DialogOptions{Settings: Settings{Copies: -1}})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Dialog(bad settings) = %v, want ErrInvalid", err)
	}
}

func TestJobStateDone(t *testing.T) {
	for s, done := range map[JobState]bool{
		JobPending: false, JobProcessing: false,
		JobCompleted: true, JobCanceled: true, JobAborted: true,
	} {
		if s.Done() != done {
			t.Errorf("%v.Done() = %v, want %v", s, s.Done(), done)
		}
	}
}

func TestFileOutputName(t *testing.T) {
	for name, want := range map[string]bool{
		"Microsoft Print to PDF":        true,
		"Microsoft XPS Document Writer": true,
		"OneNote (Desktop)":             true,
		"PDF":                           true,
		"Cups-PDF":                      true,
		"Print to File":                 true,
		"SHARP BP-50M26 PCL6":           false,
		"Office PDF-Ready Laser":        false,
	} {
		if got := fileOutputName(name); got != want {
			t.Errorf("fileOutputName(%q) = %v", name, got)
		}
	}
}
