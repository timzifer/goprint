package core

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"image"
	"image/color"
)

// ImagesToPDF wraps raster pages into a PDF, one image per page. The page
// size follows from the image size and dpi; transparent pixels are
// composited onto white. Grayscale images stay grayscale.
func ImagesToPDF(imgs []image.Image, dpi int) ([]byte, error) {
	if dpi <= 0 {
		return nil, fmt.Errorf("imagepdf: invalid dpi %d", dpi)
	}
	var b bytes.Buffer
	var offsets []int
	begin := func() int {
		offsets = append(offsets, b.Len())
		n := len(offsets)
		fmt.Fprintf(&b, "%d 0 obj\n", n)
		return n
	}
	end := func() { b.WriteString("\nendobj\n") }

	b.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	begin()
	b.WriteString("<< /Type /Catalog /Pages 2 0 R >>")
	end()
	// Object 2 (page tree) is written last; reserve its slot.
	offsets = append(offsets, 0)

	var kids []int
	for i, img := range imgs {
		if img == nil {
			return nil, fmt.Errorf("imagepdf: image %d is nil", i)
		}
		r := img.Bounds()
		if r.Empty() {
			return nil, fmt.Errorf("imagepdf: image %d is empty", i)
		}
		data, cs, err := encodePixels(img)
		if err != nil {
			return nil, err
		}
		w := float64(r.Dx()) * 72 / float64(dpi)
		h := float64(r.Dy()) * 72 / float64(dpi)

		imgObj := begin()
		fmt.Fprintf(&b, "<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /%s "+
			"/BitsPerComponent 8 /Filter /FlateDecode /Length %d >>\nstream\n", r.Dx(), r.Dy(), cs, len(data))
		b.Write(data)
		b.WriteString("\nendstream")
		end()

		content := fmt.Sprintf("q %.4f 0 0 %.4f 0 0 cm /Im0 Do Q", w, h)
		contentObj := begin()
		fmt.Fprintf(&b, "<< /Length %d >>\nstream\n%s\nendstream", len(content), content)
		end()

		kids = append(kids, begin())
		fmt.Fprintf(&b, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.4f %.4f] "+
			"/Resources << /XObject << /Im0 %d 0 R >> >> /Contents %d 0 R >>", w, h, imgObj, contentObj)
		end()
	}

	offsets[1] = b.Len()
	b.WriteString("2 0 obj\n<< /Type /Pages /Kids [")
	for _, k := range kids {
		fmt.Fprintf(&b, "%d 0 R ", k)
	}
	fmt.Fprintf(&b, "] /Count %d >>", len(kids))
	end()

	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, o := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)
	return b.Bytes(), nil
}

func encodePixels(img image.Image) ([]byte, string, error) {
	r := img.Bounds()
	gray := false
	switch img.(type) {
	case *image.Gray, *image.Gray16:
		gray = true
	}
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	row := make([]byte, 0, r.Dx()*3)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row = row[:0]
		for x := r.Min.X; x < r.Max.X; x++ {
			c := color.NRGBA64Model.Convert(img.At(x, y)).(color.NRGBA64)
			// Composite onto white.
			a := uint32(c.A)
			blend := func(v uint16) byte {
				return byte((uint32(v)*a + 0xFFFF*(0xFFFF-a)) / 0xFFFF >> 8)
			}
			if gray {
				row = append(row, blend(c.R))
			} else {
				row = append(row, blend(c.R), blend(c.G), blend(c.B))
			}
		}
		if _, err := zw.Write(row); err != nil {
			return nil, "", err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, "", err
	}
	if gray {
		return z.Bytes(), "DeviceGray", nil
	}
	return z.Bytes(), "DeviceRGB", nil
}
