//go:build windows

package winprint

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
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
	// Settings are applied through a DEVMODE converted into the job's
	// PrintTicket (ignored if Ticket is set).
	Settings JobSettings
	// DevMode, if set, is used as is (e.g. from a dialog) instead of
	// Settings; pages are laid out on its paper.
	DevMode []byte
	// Strict fails before spooling if a setting cannot be applied.
	Strict bool
	// PageRanges selects pages; empty means all.
	PageRanges []core.PageRange
	// RasterDPI is the resolution for content Direct2D must rasterize.
	// 0 means 150.
	RasterDPI float32
}

// Tracef, if set, receives internal progress messages (tests, debugging).
var Tracef func(format string, args ...any)

func tracef(format string, args ...any) {
	if f := Tracef; f != nil {
		f(format, args...)
	}
}

// jobIDTimeout bounds how long State waits for package events (job id,
// completion) after the package was closed before trusting the queue alone.
const jobIDTimeout = 30 * time.Second

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
	tracef("package status %+v", st)
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
	printer string // empty until known for dialog jobs
	title   string
	output  string // print-to-file target, verified when the job is done
	sink    *statusSink

	clipped  bool // page ranges exceeded the document
	warnings []Warning
	closedAt time.Time // when the XPS package was completely handed to the spooler

	mu       sync.Mutex
	canceled bool
	res      *resources // released when the job reaches a final state
}

// resources holds what a job keeps until it is final: the output stream the
// spooler writes through and the status event subscription. release is
// idempotent, so the explicit path and the cleanup safety net can race.
type resources struct {
	mu    sync.Mutex
	frees []func()
}

func (r *resources) keep(free func()) {
	r.mu.Lock()
	r.frees = append(r.frees, free)
	r.mu.Unlock()
}

func (r *resources) release() {
	r.mu.Lock()
	frees := r.frees
	r.frees = nil
	r.mu.Unlock()
	if len(frees) == 0 {
		return
	}
	a, err := apartment()
	if err != nil {
		return
	}
	_ = a.Do(context.Background(), func() error {
		for i := len(frees) - 1; i >= 0; i-- {
			frees[i]()
		}
		return nil
	})
}

// Warnings lists settings the printer could not apply.
func (j *Job) Warnings() []Warning { return append([]Warning(nil), j.warnings...) }

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

func newJob(printer, title string) *Job {
	j := &Job{printer: printer, title: title, sink: &statusSink{update: make(chan struct{}, 1)}, res: &resources{}}
	// Safety net for jobs nobody waits on: release held objects (closing an
	// output file) when the Job becomes unreachable.
	runtime.AddCleanup(j, (*resources).release, j.res)
	return j
}

// PrinterGuard, if set, is consulted before anything is sent to a printer
// (headless and from the dialog). Tests set it so that they can never reach
// a real device by accident.
var PrinterGuard func(printer string) error

func guard(printer string) error {
	if g := PrinterGuard; g != nil {
		return g(printer)
	}
	return nil
}

// Print renders the PDF from src and spools it.
func Print(ctx context.Context, src io.Reader, opts Options) (*Job, error) {
	if opts.Printer == "" {
		def, err := DefaultPrinter()
		if err != nil {
			return nil, err
		}
		opts.Printer = def
	}
	if err := guard(opts.Printer); err != nil {
		return nil, err
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
	job := newJob(opts.Printer, opts.Title)
	job.output = opts.OutputFile
	err = a.Do(ctx, func() error { return job.spool(ctx, src, opts) })
	if err != nil {
		job.res.release()
		return nil, err
	}
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

	var lay paperLayout
	if len(opts.Ticket) == 0 && len(opts.DevMode) > 0 {
		var err error
		if opts.Ticket, err = PrintTicket(opts.Printer, opts.DevMode); err != nil {
			return fmt.Errorf("print ticket: %w", err)
		}
		lay = paperLayout{Paper: paperDIPs(opts.Printer, opts.DevMode)}
	} else if len(opts.Ticket) == 0 && !opts.Settings.isZero() {
		dm, warns, err := BuildDevMode(opts.Printer, opts.Settings)
		if err != nil {
			return err
		}
		if opts.Settings.wantsLayout() {
			lay = paperLayout{Paper: paperDIPs(opts.Printer, dm), Scaling: opts.Settings.Scaling}
		}
		j.warnings = append(j.warnings, warns...)
		if opts.Strict && len(warns) > 0 {
			return fmt.Errorf("%w: %s: %s", errdefs.ErrUnsupported, warns[0].Setting, warns[0].Message)
		}
		if opts.Ticket, err = PrintTicket(opts.Printer, dm); err != nil {
			return fmt.Errorf("print ticket: %w", err)
		}
		tracef("print ticket: %d bytes, warnings %v", len(opts.Ticket), warns)
	}

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
		j.res.keep(output.Release)
	}

	factory, err := com.CreateInstance(&clsidPrintDocumentPackageTargetFactory, &iidIPrintDocumentPackageTargetFactory, com.CLSCTX_INPROC_SERVER)
	if err != nil {
		return err
	}
	j.res.keep(factory.Release)

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
	// The spooler may keep using the target (and the output stream behind
	// it) after Close returns; release it only once the job is final.
	j.res.keep(target.Release)

	pages, clipped := core.SelectPages(opts.PageRanges, doc.pages)
	j.clipped = clipped
	if len(pages) == 0 {
		return fmt.Errorf("%w: page ranges select no page of %d", errdefs.ErrInvalid, doc.pages)
	}
	return j.writeTarget(ctx, r, doc, target, pages, opts.RasterDPI, lay)
}

// writeTarget subscribes to the target's status events and writes the
// selected pages as XPS package into it.
func (j *Job) writeTarget(ctx context.Context, r *renderer, doc *pdfDoc, target *com.Unknown, pages []int, dpi float32, lay paperLayout) error {
	// The subscription outlives the call: the event carrying the spooler job
	// id may arrive late.
	unadvise, err := j.advise(target)
	if err != nil {
		return err
	}
	j.res.keep(unadvise)

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

	for _, i := range pages {
		if err := ctx.Err(); err != nil {
			target.Call(targetCancel)
			return err
		}
		if err := addPage(r, control, doc, i, lay); err != nil {
			target.Call(targetCancel)
			return fmt.Errorf("page %d: %w", i+1, err)
		}
	}
	if err := control.CallHR("ID2D1PrintControl.Close", printControlClose); err != nil {
		return err
	}
	j.mu.Lock()
	j.closedAt = time.Now()
	tracef("package closed")
	j.mu.Unlock()
	return nil
}

// paperLayout places PDF pages on paper. A zero Paper keeps each page's
// own size.
type paperLayout struct {
	Paper   size // DIPs
	Scaling int  // core.Scale*
}

func addPage(r *renderer, control *com.Unknown, doc *pdfDoc, i int, lay paperLayout) error {
	page, sz, err := doc.page(i)
	if err != nil {
		return err
	}
	defer page.Release()
	m, out := identity, sz
	if lay.Paper.W > 0 && lay.Paper.H > 0 {
		s, dx, dy := core.Layout(float64(sz.W), float64(sz.H), float64(lay.Paper.W), float64(lay.Paper.H), lay.Scaling)
		m, out = matrix{float32(s), 0, 0, float32(s), float32(dx), float32(dy)}, lay.Paper
	}
	list, err := r.renderPage(page, m)
	if err != nil {
		return err
	}
	defer list.Release()
	args := append([]com.Arg{com.I(list.Ptr())}, sizeArg(out)...)
	args = append(args, com.I(0), com.I(0), com.I(0)) // page ticket, tag1, tag2
	return control.CallArgsHR("ID2D1PrintControl.AddPage", printControlAddPage, args...)
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

// printerName returns the job's printer, looking it up by job id for
// dialog jobs where the user picked it.
func (j *Job) printerName() string {
	j.mu.Lock()
	name := j.printer
	j.mu.Unlock()
	if name != "" {
		return name
	}
	id := j.spoolerID()
	if id == 0 {
		return ""
	}
	if found, err := findJobPrinter(id, j.title); err == nil {
		j.mu.Lock()
		j.printer = found
		j.mu.Unlock()
		return found
	}
	return ""
}

// Printer returns the printer the job was sent to, if known.
func (j *Job) Printer() string { return j.printerName() }

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
	// A print-to-file job may still receive data after it left the queue;
	// Wait releases those resources once the output is verified.
	if err == nil && st >= StateCompleted && j.output == "" {
		j.res.release()
	}
	return st, err
}

func (j *Job) state(ctx context.Context) (State, error) {
	j.mu.Lock()
	canceled, closedAt := j.canceled, j.closedAt
	closed := !closedAt.IsZero()
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
			// "Not in the queue" is ambiguous (not yet vs. already done), so
			// the queue is only consulted once the package is complete. If the
			// final event never comes, fall back to the queue after a while.
			if !closed || time.Since(closedAt) < jobIDTimeout {
				return StateProcessing, nil
			}
		}
	}
	if !ok || st.JobID == 0 {
		// No spooler job id yet. Wait for the event; if none ever comes
		// (some drivers), the package being closed for a while is all we
		// can know.
		if closed && time.Since(closedAt) > jobIDTimeout {
			return StateCompleted, nil
		}
		return StateProcessing, nil
	}
	name := j.printerName()
	if name == "" {
		// Dialog job whose printer is not known (yet): only the package
		// events tell us something.
		if closed && time.Since(closedAt) > jobIDTimeout {
			return StateCompleted, nil
		}
		return StateProcessing, nil
	}
	h, err := openPrinter(name)
	if err != nil {
		return StatePending, err
	}
	defer h.Close()
	flags, err := h.job(st.JobID)
	tracef("queue job %d: flags 0x%X err %v", st.JobID, flags, err)
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
			err := j.verifyOutput(ctx)
			j.res.release()
			return err
		case StateCanceled:
			j.res.release()
			return errdefs.ErrCanceled
		case StateAborted:
			j.res.release()
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

// outputGrace is how long Wait waits for a print-to-file output to appear
// after the spooler reported the job as done.
const outputGrace = 10 * time.Second

// ErrOutputMissing is returned by Wait when a print-to-file job completed
// but its output file stayed empty.
var ErrOutputMissing = errors.New("winprint: job completed but output file is empty")

// verifyOutput makes sure a print-to-file job produced output, so that a
// lost output never looks like success.
func (j *Job) verifyOutput(ctx context.Context) error {
	if j.output == "" {
		return nil
	}
	deadline := time.Now().Add(outputGrace)
	for {
		if fi, err := os.Stat(j.output); err == nil && fi.Size() > 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%w: %s", ErrOutputMissing, j.output)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// Cancel deletes the job from the spooler queue.
func (j *Job) Cancel(ctx context.Context) error {
	id := j.spoolerID()
	if id == 0 {
		return fmt.Errorf("winprint: job id unknown, cannot cancel")
	}
	h, err := openPrinter(j.printerName())
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
