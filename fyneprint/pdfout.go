package fyneprint

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"

	"github.com/timzifer/goprint"
	"github.com/timzifer/goprint/internal/core"
)

// documentPDF returns the document as PDF bytes; image documents are
// wrapped into a PDF the same way goprint prints them.
func documentPDF(doc goprint.Document) ([]byte, error) {
	if doc.PDF == nil {
		b, err := core.ImagesToPDF(doc.Images, doc.DPI)
		if err != nil {
			return nil, fmt.Errorf("%w: document: %v", goprint.ErrInvalid, err)
		}
		return b, nil
	}
	r, err := doc.PDF()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// pageSize is a page's visible size in points, /Rotate applied.
type pageSize struct{ W, H float64 }

// savedPDF builds the PDF that "Save as PDF" writes: the pages selected by
// s.PageRanges, in that order, turned to s.Orientation. Paper size and
// scaling do not apply: the saved file keeps its own page sizes.
//
// sizes are the sizes of the source pages (for the orientation).
func savedPDF(ctx context.Context, data []byte, sizes []pageSize, s goprint.Settings) ([]byte, error) {
	pages, _ := core.SelectPages(toCoreRanges(s.PageRanges), len(sizes))
	if len(pages) == 0 {
		return nil, fmt.Errorf("%w: no pages selected", goprint.ErrInvalid)
	}
	conf := model.NewStatelessConfiguration()
	conf.ValidationMode = model.ValidationRelaxed

	out := data
	if !allPagesInOrder(pages, len(sizes)) {
		sel := make([]string, len(pages))
		for i, p := range pages {
			sel[i] = strconv.Itoa(p + 1)
		}
		var b bytes.Buffer
		if err := api.Collect(ctx, bytes.NewReader(out), &b, sel, conf); err != nil {
			return nil, fmt.Errorf("select pages: %w", err)
		}
		out = b.Bytes()
	}

	// Pages to turn, by angle; page numbers refer to the output.
	turn := map[int][]string{}
	for i, p := range pages {
		if a := turnAngle(sizes[p], s.Orientation); a != 0 {
			turn[a] = append(turn[a], strconv.Itoa(i+1))
		}
	}
	for _, a := range []int{90, 180, 270} {
		if len(turn[a]) == 0 {
			continue
		}
		var b bytes.Buffer
		if err := api.Rotate(ctx, bytes.NewReader(out), &b, a, turn[a], conf); err != nil {
			return nil, fmt.Errorf("rotate pages: %w", err)
		}
		out = b.Bytes()
	}
	return out, nil
}

// turnAngle returns the clockwise rotation that gives a page of size sz the
// orientation o; 0 for OrientationDefault.
func turnAngle(sz pageSize, o goprint.Orientation) int {
	landscape := sz.W > sz.H
	switch o {
	case goprint.Portrait:
		if landscape {
			return 270
		}
	case goprint.Landscape:
		if !landscape {
			return 90
		}
	case goprint.ReversePortrait:
		if landscape {
			return 90
		}
		return 180
	case goprint.ReverseLandscape:
		if !landscape {
			return 270
		}
		return 180
	}
	return 0
}

func allPagesInOrder(pages []int, n int) bool {
	if len(pages) != n {
		return false
	}
	for i, p := range pages {
		if p != i {
			return false
		}
	}
	return true
}

func toCoreRanges(rs []goprint.PageRange) []core.PageRange {
	out := make([]core.PageRange, len(rs))
	for i, r := range rs {
		out[i] = core.PageRange{From: r.From, To: r.To}
	}
	return out
}
