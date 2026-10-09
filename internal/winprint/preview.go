//go:build windows

package winprint

import (
	"fmt"
	"math"
	"sync"
	"unsafe"

	"github.com/timzifer/goprint/internal/com"
	"github.com/timzifer/goprint/internal/core"
)

var (
	iidIDXGISurface = com.MustGUID("cafcb56c-6ac3-4889-bf47-9e23bbd260ec")
)

const (
	d3dDeviceCreateTexture2D       = 5
	pdfRendererRenderPageToSurface = 3
	previewDPI                     = 150 // preview resolution; printing is vector
	dxgiFormatB8G8R8A8             = 87
	d3d11BindShaderResource        = 0x8
	d3d11BindRenderTarget          = 0x20
)

// previewState renders preview pages. The dialog calls Paginate and MakePage
// from its own threads; everything here is serialized by mu.
type previewState struct {
	mu     sync.Mutex
	target *com.Unknown // IPrintPreviewDxgiPackageTarget
	r      *renderer
	paper  size  // paper size in DIPs from the current options
	pages  []int // document pages (0-based) the user selected
	white  []byte
}

var previewCollectionVT = com.NewVTable(
	com.Method(func(this, currentJobPage, options uintptr) (hr uintptr) { // Paginate
		defer com.Guard(&hr)
		d, ok := com.Lookup(this).(*dialog)
		if !ok {
			return com.E_FAIL
		}
		if err := d.paginate((*com.Unknown)(com.Ptr(options))); err != nil {
			d.fail(err)
			return com.E_FAIL
		}
		return com.S_OK
	}),
	// MakePage(UINT32 desiredJobPage, FLOAT width, FLOAT height): the floats
	// arrive in XMM registers that callbacks cannot read, so the page size
	// comes from the options seen in Paginate instead.
	com.Method(func(this, page, w, h uintptr) (hr uintptr) {
		defer com.Guard(&hr)
		d, ok := com.Lookup(this).(*dialog)
		if !ok {
			return com.E_FAIL
		}
		if err := d.makePage(uint32(page)); err != nil {
			d.fail(err)
			return com.E_FAIL
		}
		return com.S_OK
	}),
)

func (d *dialog) previewCollection(target *com.Unknown) (*com.Unknown, error) {
	var pt *com.Unknown
	if err := target.CallHR("IPrintDocumentPackageTarget.GetPackageTarget", targetGetPackageTarget,
		uintptr(unsafe.Pointer(&iidIPrintPreviewDxgiPackageTarget)), uintptr(unsafe.Pointer(&iidIPrintPreviewDxgiPackageTarget)), uintptr(unsafe.Pointer(&pt))); err != nil {
		return nil, err
	}
	coll, err := com.NewObject(previewCollectionVT, d, iidIPrintPreviewPageCollection)
	if err != nil {
		pt.Release()
		return nil, err
	}
	p := &d.preview
	p.mu.Lock()
	old := p.target
	p.target = pt
	p.mu.Unlock()
	old.Release()
	tracef("preview collection created")
	return coll, nil // the caller owns the reference
}

// paperSize returns the paper the print task options describe, in DIPs,
// turned to the chosen orientation.
func paperSize(options *com.Unknown) (size, error) {
	oc, err := options.QueryInterface(&iidIPrintTaskOptionsCore)
	if err != nil {
		return size{}, err
	}
	defer oc.Release()
	var desc pageDescription
	if err := oc.CallHR("IPrintTaskOptionsCore.GetPageDescription", optionsCoreGetPageDescription, 1, uintptr(unsafe.Pointer(&desc))); err != nil {
		return size{}, err
	}
	return desc.PageSize, nil
}

// previewPages returns the pages to preview: the selected ones, or all if
// the selection is empty or lies outside the document (printing then
// reports that).
func previewPages(ranges []core.PageRange, n int) []int {
	pages, _ := core.SelectPages(ranges, n)
	if len(pages) == 0 {
		pages, _ = core.SelectPages(nil, n)
	}
	return pages
}

// paginate runs whenever the user changes an option: the paper and the
// selected pages are taken again.
func (d *dialog) paginate(options *com.Unknown) error {
	paper, err := paperSize(options)
	if err != nil {
		return err
	}
	ranges, err := customPageRanges(options)
	if err != nil {
		tracef("custom page ranges unavailable: %v", err)
	}
	p := &d.preview
	p.mu.Lock()
	defer p.mu.Unlock()
	p.paper = paper
	p.pages = previewPages(ranges, d.doc.pages)
	tracef("paginate: paper %.1fx%.1f DIP, ranges %v → %d pages", paper.W, paper.H, ranges, len(p.pages))
	if p.target == nil {
		return fmt.Errorf("winprint: paginate without preview target")
	}
	return p.target.CallHR("IPrintPreviewDxgiPackageTarget.SetJobPageCount", previewSetJobPageCount, pageCountFinal, uintptr(len(p.pages)))
}

func (d *dialog) makePage(jobPage uint32) error {
	if jobPage == jobPageApplicationDefined {
		jobPage = 1
	}
	p := &d.preview
	p.mu.Lock()
	defer p.mu.Unlock()
	pages := p.pages
	if pages == nil {
		pages = previewPages(nil, d.doc.pages)
	}
	if jobPage < 1 || int(jobPage) > len(pages) {
		return fmt.Errorf("winprint: preview page %d out of range", jobPage)
	}
	if p.target == nil {
		return fmt.Errorf("winprint: preview without target")
	}
	if p.r == nil {
		r, err := newRenderer()
		if err != nil {
			return err
		}
		p.r = r
	}
	paper := p.paper
	if paper.W <= 0 || paper.H <= 0 {
		paper = size{816, 1056} // Letter in DIPs until paginated
	}
	page, sz, err := d.doc.page(pages[jobPage-1])
	if err != nil {
		return err
	}
	defer page.Release()

	// Surface: the paper at previewDPI, white.
	pw := uint32(math.Ceil(float64(paper.W) * previewDPI / 96))
	ph := uint32(math.Ceil(float64(paper.H) * previewDPI / 96))
	surface, err := p.whiteSurface(pw, ph)
	if err != nil {
		return err
	}
	defer surface.Release()

	// The PDF page placed as printing places it (addPage).
	scale, dx, dy := core.Layout(float64(sz.W), float64(sz.H), float64(paper.W), float64(paper.H), d.opts.Scaling)
	px := float64(previewDPI) / 96
	dw := uint32(math.Round(float64(sz.W) * scale * px))
	dh := uint32(math.Round(float64(sz.H) * scale * px))
	params := pdfRenderParams{DestinationWidth: dw, DestinationHeight: dh, Background: [4]float32{1, 1, 1, 1}}
	args := append([]uintptr{page.Ptr(), surface.Ptr()}, pointArgs(int32(math.Round(dx*px)), int32(math.Round(dy*px)))...)
	args = append(args, uintptr(unsafe.Pointer(&params)))
	if err := p.r.pdf.CallHR("IPdfRendererNative.RenderPageToSurface", pdfRendererRenderPageToSurface, args...); err != nil {
		return err
	}
	return p.target.CallArgsHR("IPrintPreviewDxgiPackageTarget.DrawPage", previewDrawPage,
		com.I(uintptr(jobPage)), com.I(surface.Ptr()), com.F(previewDPI), com.F(previewDPI))
}

// pdfRenderParams mirrors PDF_RENDER_PARAMS.
type pdfRenderParams struct {
	SourceRect         [4]float32
	DestinationWidth   uint32
	DestinationHeight  uint32
	Background         [4]float32
	IgnoreHighContrast uint8
}

// texture2DDesc mirrors D3D11_TEXTURE2D_DESC.
type texture2DDesc struct {
	Width, Height, MipLevels, ArraySize, Format uint32
	SampleCount, SampleQuality                  uint32
	Usage, BindFlags, CPUAccessFlags, MiscFlags uint32
}

// subresourceData mirrors D3D11_SUBRESOURCE_DATA.
type subresourceData struct {
	SysMem           uintptr
	SysMemPitch      uint32
	SysMemSlicePitch uint32
}

// whiteSurface creates a white BGRA texture of w×h pixels as IDXGISurface.
// Called with p.mu held.
func (p *previewState) whiteSurface(w, h uint32) (*com.Unknown, error) {
	n := int(w) * int(h) * 4
	if len(p.white) < n {
		p.white = make([]byte, n)
		for i := range p.white {
			p.white[i] = 0xFF
		}
	}
	desc := texture2DDesc{Width: w, Height: h, MipLevels: 1, ArraySize: 1, Format: dxgiFormatB8G8R8A8,
		SampleCount: 1, BindFlags: d3d11BindRenderTarget | d3d11BindShaderResource}
	data := subresourceData{SysMem: uintptr(unsafe.Pointer(&p.white[0])), SysMemPitch: w * 4}
	var tex *com.Unknown
	if err := p.r.d3d.CallHR("ID3D11Device.CreateTexture2D", d3dDeviceCreateTexture2D,
		uintptr(unsafe.Pointer(&desc)), uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(&tex))); err != nil {
		return nil, err
	}
	defer tex.Release()
	return tex.QueryInterface(&iidIDXGISurface)
}

func (p *previewState) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.target.Release()
	p.target = nil
	if p.r != nil {
		p.r.Close()
		p.r = nil
	}
}
