package core

import (
	"bytes"
	"compress/zlib"
	"image"
	"image/color"
	"io"
	"regexp"
	"strconv"
	"testing"
)

func TestImagesToPDF(t *testing.T) {
	rgba := image.NewNRGBA(image.Rect(0, 0, 300, 150))
	rgba.Set(0, 0, color.NRGBA{255, 0, 0, 255})
	rgba.Set(1, 0, color.NRGBA{0, 0, 0, 0}) // transparent → white
	gray := image.NewGray(image.Rect(10, 10, 20, 30))
	gray.SetGray(10, 10, color.Gray{0x80})

	pdf, err := ImagesToPDF([]image.Image{rgba, gray}, 150)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) || !bytes.HasSuffix(pdf, []byte("%%EOF\n")) {
		t.Fatal("not a PDF")
	}
	if got := regexp.MustCompile(`/MediaBox \[0 0 ([\d.]+) ([\d.]+)\]`).FindAllSubmatch(pdf, -1); len(got) != 2 ||
		string(got[0][1]) != "144.0000" || string(got[0][2]) != "72.0000" ||
		string(got[1][1]) != "4.8000" || string(got[1][2]) != "9.6000" {
		t.Errorf("media boxes = %q", got)
	}
	if !bytes.Contains(pdf, []byte("/ColorSpace /DeviceGray")) || !bytes.Contains(pdf, []byte("/ColorSpace /DeviceRGB")) {
		t.Error("color spaces missing")
	}
	checkXref(t, pdf)

	// First image stream: red, then white.
	m := regexp.MustCompile(`(?s)/Length (\d+) >>\nstream\n`).FindSubmatchIndex(pdf)
	n, _ := strconv.Atoi(string(pdf[m[2]:m[3]]))
	zr, err := zlib.NewReader(bytes.NewReader(pdf[m[1] : m[1]+n]))
	if err != nil {
		t.Fatal(err)
	}
	px, _ := io.ReadAll(zr)
	if len(px) != 300*150*3 || !bytes.Equal(px[:6], []byte{255, 0, 0, 255, 255, 255}) {
		t.Errorf("pixels = % x (len %d)", px[:6], len(px))
	}
}

// checkXref verifies that every xref offset points at its object header.
func checkXref(t *testing.T, pdf []byte) {
	t.Helper()
	m := regexp.MustCompile(`startxref\n(\d+)`).FindSubmatch(pdf)
	start, _ := strconv.Atoi(string(m[1]))
	entries := regexp.MustCompile(`(\d{10}) 00000 n `).FindAllSubmatch(pdf[start:], -1)
	for i, e := range entries {
		off, _ := strconv.Atoi(string(e[1]))
		want := strconv.Itoa(i+1) + " 0 obj"
		if !bytes.HasPrefix(pdf[off:], []byte(want)) {
			t.Errorf("xref entry %d points at %q", i+1, pdf[off:off+10])
		}
	}
}

func TestImagesToPDFErrors(t *testing.T) {
	if _, err := ImagesToPDF([]image.Image{image.NewGray(image.Rect(0, 0, 1, 1))}, 0); err == nil {
		t.Error("dpi 0 accepted")
	}
	if _, err := ImagesToPDF([]image.Image{nil}, 72); err == nil {
		t.Error("nil image accepted")
	}
	if _, err := ImagesToPDF([]image.Image{image.NewGray(image.Rect(0, 0, 0, 0))}, 72); err == nil {
		t.Error("empty image accepted")
	}
}
