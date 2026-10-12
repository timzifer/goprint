//go:build darwin && !ios

package goprint_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/timzifer/goprint"
	"github.com/timzifer/goprint/internal/testpdf"
	"github.com/timzifer/goprint/ipp"
	"github.com/timzifer/goprint/virtualprinter"
)

// TestMacCUPSLandscape prints a portrait A4 page in landscape to a CUPS
// queue that forwards to a virtual IPP printer, and checks what arrives:
// one sheet, turned. Set GOPRINT_CUPS_VIRTUAL=1 and GOPRINT_LPADMIN.
func TestMacCUPSLandscape(t *testing.T) {
	if os.Getenv("GOPRINT_CUPS_VIRTUAL") == "" {
		t.Skip("set GOPRINT_CUPS_VIRTUAL=1 to test against the local CUPS")
	}
	lpadmin := strings.Fields(os.Getenv("GOPRINT_LPADMIN"))
	if len(lpadmin) == 0 {
		lpadmin = []string{"lpadmin"}
	}

	vp := virtualprinter.New("v", virtualprinter.Office("Office"))
	h := vp.Handler()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		if m, err := ipp.Decode(bytes.NewReader(data)); err == nil {
			if g := m.Group(ipp.TagJobGroup); g != nil {
				t.Logf("%s job attributes: %v", m.Operation(), g.Attrs)
			}
		}
		r.Body = io.NopCloser(bytes.NewReader(data))
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	uri := "ipp" + strings.TrimPrefix(ts.URL, "http") + "/printers/Office"
	const queue = "goprint-virtual-mac"
	out, err := exec.Command(lpadmin[0], append(lpadmin[1:], "-p", queue, "-E", "-v", uri, "-m", "everywhere")...).CombinedOutput()
	if err != nil {
		t.Fatalf("lpadmin: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command(lpadmin[0], append(lpadmin[1:], "-x", queue)...).Run() })

	src := testpdf.Generate(1, testpdf.A4Width, testpdf.A4Height)
	for _, tt := range []struct {
		name string
		s    goprint.Settings
	}{
		{"pdfkit landscape", goprint.Settings{Printer: queue, Media: goprint.MediaA4, Orientation: goprint.Landscape}},
		{"pdfkit landscape fit", goprint.Settings{Printer: queue, Media: goprint.MediaA4, Orientation: goprint.Landscape, Scaling: goprint.ScalingFit}},
		{"pdfkit landscape no media", goprint.Settings{Printer: queue, Orientation: goprint.Landscape}},
		{"ipp landscape", goprint.Settings{Printer: queue, Media: goprint.MediaA4, Orientation: goprint.Landscape, Strict: true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			vp.Reset()
			job, err := goprint.Print(context.Background(), goprint.PDFBytes("landscape "+tt.name, src), tt.s)
			if err != nil {
				t.Fatalf("Print: %v", err)
			}
			t.Logf("job %+v", job)
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			for len(vp.Jobs()) == 0 {
				select {
				case <-ctx.Done():
					t.Fatal("CUPS sent no job")
				case <-time.After(200 * time.Millisecond):
				}
			}
			j := vp.Jobs()[0]
			pages := pageSizes(j.PDF)
			t.Logf("received %q: %d bytes, settings %+v, pages %v", j.Title, len(j.PDF), j.Settings, pages)
			if len(pages) != 1 {
				t.Errorf("received %d pages, want 1", len(pages))
			}
			if dir := os.Getenv("GOPRINT_CUPS_OUT"); dir != "" {
				if err := os.WriteFile(filepath.Join(dir, strings.ReplaceAll(tt.name, " ", "-")+".pdf"), j.PDF, 0o644); err != nil {
					t.Log(err)
				}
			}
		})
	}
}

var (
	pageTypeRE = regexp.MustCompile(`/Type\s*/Page[^s]`)
	boxRE      = regexp.MustCompile(`/MediaBox\s*\[\s*([-\d.]+)\s+([-\d.]+)\s+([-\d.]+)\s+([-\d.]+)\s*\]`)
)

// pageSizes lists the MediaBox of every uncompressed page object.
func pageSizes(b []byte) []string {
	var out []string
	for _, loc := range pageTypeRE.FindAllIndex(b, -1) {
		start := bytes.LastIndex(b[:loc[0]], []byte("obj"))
		end := bytes.Index(b[loc[0]:], []byte("endobj"))
		if start < 0 || end < 0 {
			out = append(out, "?")
			continue
		}
		m := boxRE.FindSubmatch(b[start : loc[0]+end])
		if m == nil {
			out = append(out, "inherited")
			continue
		}
		f := func(i int) float64 { v, _ := strconv.ParseFloat(string(m[i]), 64); return v }
		out = append(out, strconv.FormatFloat(f(3)-f(1), 'f', 1, 64)+"x"+strconv.FormatFloat(f(4)-f(2), 'f', 1, 64))
	}
	return out
}
