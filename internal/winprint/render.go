//go:build windows

// Package winprint implements printing on Windows: PDF pages are rendered by
// Windows.Data.Pdf onto Direct2D command lists, which ID2D1PrintControl turns
// into an XPS package for the spooler. The same renderer serves the print
// preview of the modern dialog.
package winprint

import (
	"context"
	"fmt"
	"io"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/timzifer/goprint/internal/com"
)

var (
	modd3d11              = windows.NewLazySystemDLL("d3d11.dll")
	modd2d1               = windows.NewLazySystemDLL("d2d1.dll")
	modpdf                = windows.NewLazySystemDLL("Windows.Data.Pdf.dll")
	procD3D11CreateDevice = modd3d11.NewProc("D3D11CreateDevice")
	procD2D1CreateFactory = modd2d1.NewProc("D2D1CreateFactory")
	procPdfCreateRenderer = modpdf.NewProc("PdfCreateRenderer")
)

var (
	iidIDXGIDevice         = com.MustGUID("54ec77fa-1377-44e6-8c32-88fd5f44c84c")
	iidID2D1Factory1       = com.MustGUID("bb12d362-daee-4b9a-aa1d-14ba401cfa1f")
	clsidWICImagingFactory = com.MustGUID("cacaf262-9370-4615-a13b-9f5539da4c0a")
	iidIWICImagingFactory  = com.MustGUID("ec5ec8a9-c395-4314-9c77-54d7a935ff70")
	iidIPdfDocumentStatics = com.MustGUID("433a0b5f-c007-4788-90f2-08143d922599")
)

// vtable slots, verified against the Windows SDK 10.0.26100 headers.
const (
	d2dFactory1CreateDevice = 17

	d2dDeviceCreateDeviceContext = 4
	d2dDeviceCreatePrintControl  = 5

	d2dCtxBeginDraw         = 48
	d2dCtxEndDraw           = 49
	d2dCtxCreateCommandList = 67
	d2dCtxSetTarget         = 74

	d2dCommandListClose = 5

	pdfRendererRenderPageToDeviceContext = 4

	pdfStaticsLoadFromStreamAsync = 8
	pdfDocGetPage                 = 6
	pdfDocGetPageCount            = 7
	pdfPageGetSize                = 10
)

const (
	d3dDriverTypeHardware = 1
	d3dDriverTypeWARP     = 5
	d3d11CreateBGRA       = 0x20
	d3d11SDKVersion       = 7
)

// renderer bundles the Direct3D/Direct2D device chain and the PDF renderer.
// It must be used on a single apartment thread.
type renderer struct {
	d3d     *com.Unknown
	dxgi    *com.Unknown
	factory *com.Unknown
	device  *com.Unknown // ID2D1Device
	ctx     *com.Unknown // ID2D1DeviceContext
	pdf     *com.Unknown // IPdfRendererNative
	wic     *com.Unknown // IWICImagingFactory
}

func newRenderer() (r *renderer, err error) {
	r = &renderer{}
	defer func() {
		if err != nil {
			r.Close()
		}
	}()
	for _, driver := range []uintptr{d3dDriverTypeHardware, d3dDriverTypeWARP} {
		err = com.Call(procD3D11CreateDevice, 0, driver, 0, d3d11CreateBGRA, 0, 0, d3d11SDKVersion, uintptr(unsafe.Pointer(&r.d3d)), 0, 0)
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("D3D11CreateDevice: %w", err)
	}
	if r.dxgi, err = r.d3d.QueryInterface(&iidIDXGIDevice); err != nil {
		return nil, err
	}
	if err = com.Call(procD2D1CreateFactory, 0, uintptr(unsafe.Pointer(&iidID2D1Factory1)), 0, uintptr(unsafe.Pointer(&r.factory))); err != nil {
		return nil, err
	}
	if err = r.factory.CallHR("ID2D1Factory1.CreateDevice", d2dFactory1CreateDevice, r.dxgi.Ptr(), uintptr(unsafe.Pointer(&r.device))); err != nil {
		return nil, err
	}
	if err = r.device.CallHR("ID2D1Device.CreateDeviceContext", d2dDeviceCreateDeviceContext, 0, uintptr(unsafe.Pointer(&r.ctx))); err != nil {
		return nil, err
	}
	if err = com.Call(procPdfCreateRenderer, r.dxgi.Ptr(), uintptr(unsafe.Pointer(&r.pdf))); err != nil {
		return nil, err
	}
	if r.wic, err = com.CreateInstance(&clsidWICImagingFactory, &iidIWICImagingFactory, com.CLSCTX_INPROC_SERVER); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *renderer) Close() {
	for _, u := range []*com.Unknown{r.wic, r.pdf, r.ctx, r.device, r.factory, r.dxgi, r.d3d} {
		u.Release()
	}
	*r = renderer{}
}

// renderPage records one PDF page into a new, closed command list.
func (r *renderer) renderPage(page *com.Unknown) (*com.Unknown, error) {
	var list *com.Unknown
	if err := r.ctx.CallHR("ID2D1DeviceContext.CreateCommandList", d2dCtxCreateCommandList, uintptr(unsafe.Pointer(&list))); err != nil {
		return nil, err
	}
	r.ctx.Call(d2dCtxSetTarget, list.Ptr())
	r.ctx.Call(d2dCtxBeginDraw)
	err := r.pdf.CallHR("IPdfRendererNative.RenderPageToDeviceContext", pdfRendererRenderPageToDeviceContext, page.Ptr(), r.ctx.Ptr(), 0)
	if endErr := r.ctx.CallHR("ID2D1DeviceContext.EndDraw", d2dCtxEndDraw, 0, 0); err == nil {
		err = endErr
	}
	r.ctx.Call(d2dCtxSetTarget, 0)
	if err == nil {
		err = list.CallHR("ID2D1CommandList.Close", d2dCommandListClose)
	}
	if err != nil {
		list.Release()
		return nil, err
	}
	return list, nil
}

// pdfDoc is a loaded Windows.Data.Pdf.PdfDocument.
type pdfDoc struct {
	doc   *com.Unknown
	pages int
}

// loadPDF reads src completely and loads it with Windows.Data.Pdf.
func loadPDF(ctx context.Context, src io.Reader) (*pdfDoc, error) {
	data, err := io.ReadAll(src)
	if err != nil {
		return nil, fmt.Errorf("read PDF: %w", err)
	}
	stream, err := com.NewMemStream(data)
	if err != nil {
		return nil, err
	}
	defer stream.Release()
	ras, err := stream.RandomAccess()
	if err != nil {
		return nil, err
	}
	defer ras.Release()
	statics, err := com.ActivationFactory("Windows.Data.Pdf.PdfDocument", &iidIPdfDocumentStatics)
	if err != nil {
		return nil, err
	}
	defer statics.Release()
	var op *com.Unknown
	if err := statics.CallHR("PdfDocument.LoadFromStreamAsync", pdfStaticsLoadFromStreamAsync, ras.Ptr(), uintptr(unsafe.Pointer(&op))); err != nil {
		return nil, err
	}
	defer op.Release()
	doc, err := com.Await(ctx, op, true)
	if err != nil {
		return nil, fmt.Errorf("load PDF: %w", err)
	}
	var n uint32
	if err := doc.CallHR("PdfDocument.get_PageCount", pdfDocGetPageCount, uintptr(unsafe.Pointer(&n))); err != nil {
		doc.Release()
		return nil, err
	}
	return &pdfDoc{doc: doc, pages: int(n)}, nil
}

func (d *pdfDoc) Close() { d.doc.Release(); d.doc = nil }

// size is a D2D_SIZE_F / Windows.Foundation.Size in DIPs (1/96 inch).
type size struct{ W, H float32 }

// page returns page i (0-based) and its size in DIPs.
func (d *pdfDoc) page(i int) (*com.Unknown, size, error) {
	var p *com.Unknown
	if err := d.doc.CallHR("PdfDocument.GetPage", pdfDocGetPage, uintptr(i), uintptr(unsafe.Pointer(&p))); err != nil {
		return nil, size{}, err
	}
	var s size
	if err := p.CallHR("PdfPage.get_Size", pdfPageGetSize, uintptr(unsafe.Pointer(&s))); err != nil {
		p.Release()
		return nil, size{}, err
	}
	return p, s, nil
}
