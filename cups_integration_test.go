//go:build linux || darwin || freebsd

package goprint

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/timzifer/goprint/internal/testpdf"
)

// TestCUPSDiscovery talks to the real local CUPS. Set GOPRINT_CUPS=1.
func TestCUPSDiscovery(t *testing.T) {
	if os.Getenv("GOPRINT_CUPS") == "" {
		t.Skip("set GOPRINT_CUPS=1 to test against the local CUPS")
	}
	ps, err := Printers(context.Background())
	if err != nil {
		t.Fatalf("Printers: %v", err)
	}
	for _, p := range ps {
		t.Logf("%+v", p)
	}
}

// TestCUPSPrintToPDF prints to a cups-pdf queue and checks the PDF it
// writes. Set GOPRINT_CUPS_PDF=<queue> and GOPRINT_CUPS_PDF_OUT=<dirs>
// (colon-separated output directories to search).
func TestCUPSPrintToPDF(t *testing.T) {
	queue := os.Getenv("GOPRINT_CUPS_PDF")
	if queue == "" {
		t.Skip("set GOPRINT_CUPS_PDF to a cups-pdf queue")
	}
	dirs := filepath.SplitList(os.Getenv("GOPRINT_CUPS_PDF_OUT"))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	caps, err := GetCapabilities(ctx, queue)
	if err != nil {
		t.Fatalf("GetCapabilities: %v", err)
	}
	t.Logf("caps: %+v", caps)

	for _, tc := range []struct {
		name  string
		s     Settings
		pages int
	}{
		{"all pages", Settings{}, 3},
		{"page range", Settings{PageRanges: []PageRange{{2, 3}}}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			title := "goprint-" + strings.ReplaceAll(tc.name, " ", "-") + "-" + time.Now().Format("150405.000")
			src := testpdf.Generate(3, testpdf.A4Width, testpdf.A4Height)
			tc.s.Printer = queue
			job, err := Print(ctx, Document{Title: title, PDF: func() (io.ReadSeekCloser, error) { return nopCloser{bytes.NewReader(src)}, nil }}, tc.s)
			if err != nil {
				t.Fatalf("Print: %v", err)
			}
			t.Logf("job %s, warnings %v", job.ID(), job.Warnings())
			if err := job.Wait(ctx); err != nil {
				t.Fatalf("Wait: %v", err)
			}
			out := findOutput(t, ctx, dirs, title)
			if n := len(regexp.MustCompile(`/Type\s*/Page[^s]`).FindAll(out, -1)); n != tc.pages {
				t.Errorf("output has %d pages, want %d", n, tc.pages)
			}
		})
	}
}

// findOutput waits for the cups-pdf output file whose name contains title.
func findOutput(t *testing.T, ctx context.Context, dirs []string, title string) []byte {
	t.Helper()
	for {
		for _, d := range dirs {
			matches, _ := filepath.Glob(filepath.Join(d, "*"+title+"*.pdf"))
			for _, m := range matches {
				if b, err := os.ReadFile(m); err == nil && bytes.Contains(b, []byte("%%EOF")) {
					t.Logf("output %s (%d bytes)", m, len(b))
					return b
				}
			}
		}
		select {
		case <-ctx.Done():
			for _, d := range dirs {
				entries, _ := os.ReadDir(d)
				for _, e := range entries {
					t.Logf("%s/%s", d, e.Name())
				}
			}
			t.Fatalf("no output for %q in %v", title, dirs)
		case <-time.After(200 * time.Millisecond):
		}
	}
}
