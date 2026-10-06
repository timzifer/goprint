//go:build windows

package winprint

import (
	"context"
	"fmt"
	"io"
	"runtime"
	"strconv"
	"sync"
	"time"
	"unsafe"

	"github.com/timzifer/goprint/internal/com"
	"github.com/timzifer/goprint/internal/core"
	"github.com/timzifer/goprint/internal/errdefs"
)

var (
	clsidPrintDocumentPackageTargetFactory = com.MustGUID("348ef17d-6c81-4982-92b4-ee188a43867a")
	iidIPrintDocumentPackageTargetFactory  = com.MustGUID("d2959bf7-b31b-4a3d-9600-712eb1335ba4")
	iidIPrintDocumentPackageStatusEvent    = com.MustGUID("ed90c8ad-5c34-4d05-a1ec-0e8a9b3ad7af")
	iidIConnectionPointContainer           = com.MustGUID("b196b284-bab4-101a-b69c-00aa00341d07")
)

const (
	targetFactoryCreateForPrintJob = 3
	targetCancel                   = 5

	printControlAddPage = 3
	printControlClose   = 4

	cpcFindConnectionPoint = 4
	cpAdvise               = 5
	cpUnadvise             = 6
)

// Options configures a print job.
type Options struct {
	// Printer is the printer name; empty means the default printer.
	Printer string
	// Title is the job name.
	Title string
	// Ticket is an optional PrintTicket (XML) for the job.
	Ticket []byte
	// OutputFile, if set, receives the printer output instead of the device
	// (print to file).
	OutputFile string
	// PageRanges selects pages; empty means all.
	PageRanges []core.PageRange
	// RasterDPI is the resolution for content Direct2D must rasterize.
	// 0 means 150.
	RasterDPI float32
}

// Package completion values (PrintDocumentPackageCompletion).
const (
	completionInProgress = 0
	completionCompleted  = 1
	completionCanceled   = 2
	completionFailed     = 3
)

// packageStatus mirrors PrintDocumentPackageStatus.
type packageStatus struct {
	JobID            uint32
	CurrentDocument  int32
	CurrentPage      int32
	CurrentPageTotal int32
	Completion       int32
	PackageStatus    int32
}

// statusSink receives IPrintDocumentPackageStatusEvent callbacks, which may
// arrive on any thread.
type statusSink struct {
	mu     sync.Mutex
	last   packageStatus
	seen   bool
	update chan struct{}
}

func (s *statusSink) set(st packageStatus) {
	s.mu.Lock()
	s.last, s.seen = st, true
	s.mu.Unlock()
	select {
	case s.update <- struct{}{}:
	default:
	}
}

func (s *statusSink) get() (packageStatus, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last, s.seen
}

var statusSinkVTable = com.NewVTable(
	// IDispatch
	com.Method(func(this, pctinfo uintptr) (hr uintptr) { // GetTypeInfoCount
		defer com.Guard(&hr)
		if pctinfo != 0 {
			*(*uint32)(com.Ptr(pctinfo)) = 0
		}
		return com.S_OK
	}),
	com.Method(func(this, i, lcid, pp uintptr) uintptr { return com.E_NOTIMPL }),                                  // GetTypeInfo
	com.Method(func(this, riid, names, n, lcid, ids uintptr) uintptr { return com.E_NOTIMPL }),                    // GetIDsOfNames
	com.Method(func(this, id, riid, lcid, flags, params, res, exc, arg uintptr) uintptr { return com.E_NOTIMPL }), // Invoke
	// IPrintDocumentPackageStatusEvent
	com.Method(func(this, pstatus uintptr) (hr uintptr) { // PackageStatusUpdated
		defer com.Guard(&hr)
		if s, ok := com.Lookup(this).(*statusSink); ok && pstatus != 0 {
			s.set(*(*packageStatus)(com.Ptr(pstatus)))
		}
		return com.S_OK
	}),
)

// Job is a spooled print job.
type Job struct {
	printer string
	sink    *statusSink

	clipped bool // page ranges exceeded the document
	closed  bool // the XPS package was completely handed to the spooler

	mu       sync.Mutex
	canceled bool
	res      *resources // released when the job reaches a final state
}

// resources holds COM references a job keeps until it is final. release
// is idempotent, so the explicit path and the cleanup safety net can race.
type resources struct {
	mu   sync.Mutex
	held []*com.Unknown
}

func (r *resources) keep(u *com.Unknown) {
	r.mu.Lock()
	r.held = append(r.held, u)
	r.mu.Unlock()
}

func (r *resources) release() {
	r.mu.Lock()
	held := r.held
	r.held = nil
	r.mu.Unlock()
	if len(held) > 0 {
		releaseOnApartment(held)
	}
}

func releaseOnApartment(held []*com.Unknown) {
	a, err := apartment()
	if err != nil {
		return
	}
	_ = a.Do(context.Background(), func() error {
		for _, u := range held {
			u.Release()
		}
		return nil
	})
}

// PagesClipped reports whether page ranges reached beyond the document.
func (j *Job) PagesClipped() bool { return j.clipped }

// State is a coarse job state.
type State int

const (
	StatePending State = iota
	StateProcessing
	StateCompleted
	StateCanceled
	StateAborted
)

var mta struct {
	once sync.Once
	a    *com.Apartment
	err  error
}

func apartment() (*com.Apartment, error) {
	mta.once.Do(func() { mta.a, mta.err = com.NewApartment(com.MTA) })
	return mta.a, mta.err
}

// Print renders the PDF from src and spools it.
func Print(ctx context.Context, src io.Reader, opts Options) (*Job, error) {
	if !addPageSupported {
		return nil, fmt.Errorf("%w: printing on this architecture", errdefs.ErrUnsupported)
	}
	if opts.Printer == "" {
		def, err := DefaultPrinter()
		if err != nil {
			return nil, err
		}
		opts.Printer = def
	}
	// Validate the printer name early for a clear error.
	h, err := openPrinter(opts.Printer)
	if err != nil {
		return nil, err
	}
	h.Close()

	a, err := apartment()
	if err != nil {
		return nil, err
	}
	job := &Job{printer: opts.Printer, sink: &statusSink{update: make(chan struct{}, 1)}, res: &resources{}}
	err = a.Do(ctx, func() error { return job.spool(ctx, src, opts) })
	if err != nil {
		job.res.release()
		return nil, err
	}
	// Safety net for jobs nobody waits on: release held streams (closing an
	// output file) when the Job becomes unreachable.
	runtime.AddCleanup(job, (*resources).release, job.res)
	return job, nil
}

func (j *Job) spool(ctx context.Context, src io.Reader, opts Options) error {
	doc, err := loadPDF(ctx, src)
	if err != nil {
		return err
	}
	defer doc.Close()
	if doc.pages == 0 {
		return fmt.Errorf("%w: PDF has no pages", errdefs.ErrInvalid)
	}

	r, err := newRenderer()
	if err != nil {
		return err
	}
	defer r.Close()

	var ticket, output *com.Stream
	if len(opts.Ticket) > 0 {
		if ticket, err = com.NewMemStream(opts.Ticket); err != nil {
			return err
		}
		defer ticket.Release()
	}
	if opts.OutputFile != "" {
		if output, err = com.NewFileStream(opts.OutputFile, com.STGM_WRITE|com.STGM_CREATE|com.STGM_SHARE_DENY_WRITE, true); err != nil {
			return err
		}
		// The spooler writes the printer output through this stream while the
		// job is processed, after spool returns; the job owns it from here.
		j.res.keep(&output.Unknown)
	}

	factory, err := com.CreateInstance(&clsidPrintDocumentPackageTargetFactory, &iidIPrintDocumentPackageTargetFactory, com.CLSCTX_INPROC_SERVER)
	if err != nil {
		return err
	}
	defer factory.Release()

	printer, err := utf16(opts.Printer)
	if err != nil {
		return err
	}
	title, err := utf16(opts.Title)
	if err != nil {
		return err
	}
	var target *com.Unknown
	if err := factory.CallHR("CreateDocumentPackageTargetForPrintJob", targetFactoryCreateForPrintJob,
		uintptr(unsafe.Pointer(printer)), uintptr(unsafe.Pointer(title)), streamPtr(output), streamPtr(ticket), uintptr(unsafe.Pointer(&target))); err != nil {
		return err
	}
	defer target.Release()

	unadvise, err := j.advise(target)
	if err != nil {
		return err
	}
	defer unadvise()

	dpi := opts.RasterDPI
	if dpi == 0 {
		dpi = 150
	}
	props := struct {
		fontSubset uint32
		rasterDPI  float32
		colorSpace uint32
	}{0, dpi, 0}
	var control *com.Unknown
	if err := r.device.CallHR("ID2D1Device.CreatePrintControl", d2dDeviceCreatePrintControl,
		r.wic.Ptr(), target.Ptr(), uintptr(unsafe.Pointer(&props)), uintptr(unsafe.Pointer(&control))); err != nil {
		return err
	}
	defer control.Release()

	pages, clipped := core.SelectPages(opts.PageRanges, doc.pages)
	j.clipped = clipped
	if len(pages) == 0 {
		return fmt.Errorf("%w: page ranges select no page of %d", errdefs.ErrInvalid, doc.pages)
	}
	for _, i := range pages {
		if err := ctx.Err(); err != nil {
			target.Call(targetCancel)
			return err
		}
		if err := addPage(r, control, doc, i); err != nil {
			target.Call(targetCancel)
			return fmt.Errorf("page %d: %w", i+1, err)
		}
	}
	if err := control.CallHR("ID2D1PrintControl.Close", printControlClose); err != nil {
		return err
	}
	j.mu.Lock()
	j.closed = true
	j.mu.Unlock()
	return nil
}

func addPage(r *renderer, control *com.Unknown, doc *pdfDoc, i int) error {
	page, sz, err := doc.page(i)
	if err != nil {
		return err
	}
	defer page.Release()
	list, err := r.renderPage(page)
	if err != nil {
		return err
	}
	defer list.Release()
	args := append([]uintptr{list.Ptr()}, sizeArgs(sz)...)
	args = append(args, 0, 0, 0) // page ticket, tag1, tag2
	return control.CallHR("ID2D1PrintControl.AddPage", printControlAddPage, args...)
}

// advise registers the status sink on the package target.
func (j *Job) advise(target *com.Unknown) (func(), error) {
	cpc, err := target.QueryInterface(&iidIConnectionPointContainer)
	if err != nil {
		return nil, err
	}
	defer cpc.Release()
	var cp *com.Unknown
	if err := cpc.CallHR("FindConnectionPoint", cpcFindConnectionPoint, uintptr(unsafe.Pointer(&iidIPrintDocumentPackageStatusEvent)), uintptr(unsafe.Pointer(&cp))); err != nil {
		return nil, err
	}
	sink, err := com.NewObject(statusSinkVTable, j.sink, com.IIDIDispatch, iidIPrintDocumentPackageStatusEvent)
	if err != nil {
		cp.Release()
		return nil, err
	}
	var cookie uint32
	err = cp.CallHR("IConnectionPoint.Advise", cpAdvise, sink.Ptr(), uintptr(unsafe.Pointer(&cookie)))
	sink.Release() // the connection point holds its own reference
	if err != nil {
		cp.Release()
		return nil, err
	}
	return func() {
		cp.Call(cpUnadvise, uintptr(cookie))
		cp.Release()
	}, nil
}

func streamPtr(s *com.Stream) uintptr {
	if s == nil {
		return 0
	}
	return s.Ptr()
}

func utf16(s string) (*uint16, error) {
	p, err := syscallUTF16(s)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errdefs.ErrInvalid, err)
	}
	return p, nil
}

// ID returns the spooler job id, or "" if not yet known.
func (j *Job) ID() string {
	if id := j.spoolerID(); id != 0 {
		return strconv.FormatUint(uint64(id), 10)
	}
	return ""
}

func (j *Job) spoolerID() uint32 {
	if st, ok := j.sink.get(); ok && st.JobID != 0 {
		return st.JobID
	}
	return 0
}

// State reports the job state, combining the package status and the
// spooler queue.
func (j *Job) State(ctx context.Context) (State, error) {
	st, err := j.state(ctx)
	if err == nil && st >= StateCompleted {
		j.res.release()
	}
	return st, err
}

func (j *Job) state(ctx context.Context) (State, error) {
	j.mu.Lock()
	canceled, closed := j.canceled, j.closed
	j.mu.Unlock()
	if canceled {
		return StateCanceled, nil
	}
	st, ok := j.sink.get()
	if ok {
		switch st.Completion {
		case completionCanceled:
			return StateCanceled, nil
		case completionFailed:
			return StateAborted, nil
		case completionInProgress:
			// The final package event may arrive after the sink was
			// unadvised; once the package is closed the queue is authoritative.
			if !closed {
				return StateProcessing, nil
			}
		}
	}
	if !ok || st.JobID == 0 {
		// Package completed (Close succeeded) but no spooler job is known:
		// treat as handed off.
		return StateCompleted, nil
	}
	h, err := openPrinter(j.printer)
	if err != nil {
		return StatePending, err
	}
	defer h.Close()
	flags, err := h.job(st.JobID)
	if err == errJobGone {
		return StateCompleted, nil
	}
	if err != nil {
		return StatePending, err
	}
	switch {
	case flags&(jobStatusDeleting|jobStatusDeleted) != 0:
		return StateCanceled, nil
	case flags&jobStatusError != 0:
		return StateAborted, nil
	case flags&(jobStatusPrinted|jobStatusComplete|jobStatusRetained) != 0:
		return StateCompleted, nil
	case flags&(jobStatusPrinting|jobStatusSpooling) != 0:
		return StateProcessing, nil
	}
	return StatePending, nil
}

// Wait polls until the job reaches a final state.
func (j *Job) Wait(ctx context.Context) error {
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		s, err := j.State(ctx)
		if err != nil {
			return err
		}
		switch s {
		case StateCompleted:
			return nil
		case StateCanceled:
			return errdefs.ErrCanceled
		case StateAborted:
			return fmt.Errorf("winprint: job %s aborted", j.ID())
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		case <-j.sink.update:
		}
	}
}

// Cancel deletes the job from the spooler queue.
func (j *Job) Cancel(ctx context.Context) error {
	id := j.spoolerID()
	if id == 0 {
		return fmt.Errorf("winprint: job id unknown, cannot cancel")
	}
	h, err := openPrinter(j.printer)
	if err != nil {
		return err
	}
	defer h.Close()
	if err := h.deleteJob(id); err != nil {
		return err
	}
	j.mu.Lock()
	j.canceled = true
	j.mu.Unlock()
	return nil
}
