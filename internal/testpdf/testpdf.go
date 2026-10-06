// Package testpdf generates small, valid PDF documents for tests.
package testpdf

import (
	"bytes"
	"fmt"
)

// Generate returns a PDF with n pages of the given size in PDF points
// (1/72 inch). Each page shows a frame and the text "Page i of n".
func Generate(n int, width, height float64) []byte {
	var b bytes.Buffer
	var offsets []int
	obj := func(body string) {
		offsets = append(offsets, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", len(offsets), body)
	}

	b.WriteString("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n")
	// 1: catalog, 2: pages, 3: font, then per page: page + content stream.
	obj("<< /Type /Catalog /Pages 2 0 R >>")
	kids := ""
	for i := 0; i < n; i++ {
		kids += fmt.Sprintf("%d 0 R ", 4+2*i)
	}
	obj(fmt.Sprintf("<< /Type /Pages /Count %d /Kids [%s] >>", n, kids))
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	for i := 0; i < n; i++ {
		obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %g %g] "+
			"/Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", width, height, 5+2*i))
		content := fmt.Sprintf("2 w 36 36 %g %g re S BT /F1 24 Tf 72 %g Td (Page %d of %d) Tj ET",
			width-72, height-72, height-108, i+1, n)
		obj(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, o := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)
	return b.Bytes()
}

// A4 is the A4 page size in points.
const A4Width, A4Height = 595.276, 841.89
