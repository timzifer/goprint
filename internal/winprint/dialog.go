//go:build windows

package winprint

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/timzifer/goprint/internal/com"
	"github.com/timzifer/goprint/internal/errdefs"
)

// The modern print dialog (Windows 10+) is driven through
// Windows.Graphics.Printing.PrintManager via IPrintManagerInterop. The
// application provides an IPrintDocumentSource that also implements
// IPrintDocumentPageSource: the dialog asks it for preview pages
// (IPrintPreviewPageCollection, drawn onto DXGI surfaces) and, on "Print",
// for the document (MakeDocument, the same D2D → XPS path as headless).

var (
	iidIPrintManagerInterop             = com.MustGUID("c5435a42-8d43-4e7b-a68a-ef311e392087")
	iidIPrintManager                    = com.MustGUID("ff2a9694-8c99-44fd-ae4a-19d9aa9a0f0a")
	iidIAsyncOperationBool              = com.MustGUID("cdb5efb3-5788-509d-9be1-71ccb8a3362a")
	iidPrintTaskRequestedHandler        = com.MustGUID("8a8cb877-70c5-54ce-8b42-d790e2914859") // TypedEventHandler<PrintManager, PrintTaskRequestedEventArgs>
	iidPrintTaskCompletedHandler        = com.MustGUID("b0b02549-b9ad-5226-898a-7b563b46640c") // TypedEventHandler<PrintTask, PrintTaskCompletedEventArgs>
	iidIPrintTaskSourceRequestedHandler = com.MustGUID("6c109fa8-5cb6-4b3a-8663-f39cb02dc9b4")
	iidIPrintTaskOptionsCore            = com.MustGUID("1bdbb474-4ed1-41eb-be3c-72d18ed67337")
	iidIPrintTaskOptionsCoreProperties  = com.MustGUID("c1b71832-9e93-4e55-814b-3326a59efce1")
	iidIPrintDocumentSource             = com.MustGUID("dedc0c30-f1eb-47df-aae6-ed5427511f01")
	iidIPrintDocumentPageSource         = com.MustGUID("a96bb1db-172e-4667-82b5-ad97a252318f")
	iidIPrintPreviewPageCollection      = com.MustGUID("0b31cc62-d7ec-4747-9d6e-f2537d870f2b")
	iidIPrintPreviewDxgiPackageTarget   = com.MustGUID("1a6dd0ad-1e2a-4e99-a5ba-91f17818290e") // also ID_PREVIEWPACKAGETARGET_DXGI
)

// vtable slots (Windows SDK 10.0.26100).
const (
	interopGetForWindow              = 6
	interopShowPrintUIForWindowAsync = 7

	printManagerAddPrintTaskRequested    = 6
	printManagerRemovePrintTaskRequested = 7

	taskRequestedArgsGetRequest = 6
	taskRequestCreatePrintTask  = 7

	printTaskGetOptions      = 8
	printTaskAddCompleted    = 15
	printTaskRemoveCompleted = 16

	sourceRequestedArgsSetSource = 7

	taskCompletedArgsGetCompletion = 6

	optionsCoreGetPageDescription = 6

	optPutMediaSize      = 6
	optGetMediaSize      = 7
	optPutOrientation    = 10
	optGetOrientation    = 11
	optPutPrintQuality   = 12
	optGetPrintQuality   = 13
	optPutColorMode      = 14
	optGetColorMode      = 15
	optPutDuplex         = 16
	optGetDuplex         = 17
	optPutCollation      = 18
	optGetCollation      = 19
	optPutNumberOfCopies = 28
	optGetNumberOfCopies = 29

	targetGetPackageTarget = 4

	previewSetJobPageCount = 3
	previewDrawPage        = 4

	pageCountFinal            = 0
	jobPageApplicationDefined = 0xFFFFFFFF
)

// PrintTaskCompletion values.
const (
	completionAbandoned    = 0
	completionTaskCanceled = 1
	completionTaskFailed   = 2
	completionSubmitted    = 3
)

// TaskOptions are the dialog's settings in WinRT enum values
// (Windows.Graphics.Printing.PrintOrientation etc.); 0 means default.
type TaskOptions struct {
	Copies      uint32
	MediaSize   int32
	Orientation int32
	Quality     int32
	Color       int32
	Duplex      int32
	Collation   int32
}

// DialogOptions configures Dialog.
type DialogOptions struct {
	// Owner is the parent window; 0 creates a helper window.
	Owner windows.HWND
	Title string
	// Presets are applied to the dialog before it shows.
	Presets TaskOptions
	// PrintNow prints on confirmation; otherwise only the chosen settings
	// are returned. The dialog still creates a spooler job before asking us
	// for the document; it is canceled right away, but print-to-file
	// printers (e.g. "Microsoft Print to PDF") ask for a file name anyway.
	PrintNow bool
}

// DialogResult is the outcome of a confirmed dialog.
type DialogResult struct {
	// Job is the spooled job, nil unless PrintNow.
	Job *Job
	// Chosen are the settings the user confirmed.
	Chosen TaskOptions
}

// pageDescription mirrors PrintPageDescription.
type pageDescription struct {
	PageSize      size
	ImageableRect [4]float32
	DpiX, DpiY    uint32
}

// dialog is the state of one modern print dialog. Its methods are called
// from COM callbacks on arbitrary threads.
type dialog struct {
	opts DialogOptions
	doc  *pdfDoc

	mu         sync.Mutex
	task       *com.Unknown // IPrintTask
	taskToken  int64
	completion int32
	completed  chan struct{}
	chosen     TaskOptions
	job        *Job
	jobErr     error
	errs       []error

	preview previewState

	// COM objects implemented by us.
	source           *com.Unknown // IPrintDocumentSource
	sourceHandler    *com.Unknown
	completedHandler *com.Unknown
}

var (
	taskRequestedVT = com.NewVTable(com.Method(func(this, sender, args uintptr) (hr uintptr) {
		defer com.Guard(&hr)
		if d, ok := com.Lookup(this).(*dialog); ok {
			d.onTaskRequested((*com.Unknown)(com.Ptr(args)))
		}
		return com.S_OK
	}))
	sourceRequestedVT = com.NewVTable(com.Method(func(this, args uintptr) (hr uintptr) {
		defer com.Guard(&hr)
		if d, ok := com.Lookup(this).(*dialog); ok {
			a := (*com.Unknown)(com.Ptr(args))
			if err := a.CallHR("PrintTaskSourceRequestedArgs.SetSource", sourceRequestedArgsSetSource, d.source.Ptr()); err != nil {
				d.fail(err)
			}
		}
		return com.S_OK
	}))
	taskCompletedVT = com.NewVTable(com.Method(func(this, sender, args uintptr) (hr uintptr) {
		defer com.Guard(&hr)
		if d, ok := com.Lookup(this).(*dialog); ok {
			d.onCompleted((*com.Unknown)(com.Ptr(sender)), (*com.Unknown)(com.Ptr(args)))
		}
		return com.S_OK
	}))
	documentSourceVT = com.NewInspectableVTable() // IPrintDocumentSource has no own methods
	pageSourceVT     = com.NewVTable(
		com.Method(func(this, target, out uintptr) (hr uintptr) { // GetPreviewPageCollection
			defer com.Guard(&hr)
			d, ok := com.Lookup(this).(*dialog)
			if !ok || out == 0 {
				return com.E_POINTER
			}
			coll, err := d.previewCollection((*com.Unknown)(com.Ptr(target)))
			if err != nil {
				d.fail(err)
				return com.E_FAIL
			}
			*(*uintptr)(com.Ptr(out)) = coll.Ptr()
			return com.S_OK
		}),
		com.Method(func(this, options, target uintptr) (hr uintptr) { // MakeDocument
			defer com.Guard(&hr)
			d, ok := com.Lookup(this).(*dialog)
			if !ok {
				return com.E_FAIL
			}
			if err := d.makeDocument((*com.Unknown)(com.Ptr(target))); err != nil {
				d.fail(err)
				return com.E_FAIL
			}
			return com.S_OK
		}),
	)
)

func (d *dialog) fail(err error) {
	tracef("dialog error: %v", err)
	d.mu.Lock()
	d.errs = append(d.errs, err)
	d.mu.Unlock()
}

// Dialog shows the modern print dialog with a live preview of the PDF from
// src. It returns errdefs.ErrCanceled if the user cancels.
func Dialog(ctx context.Context, src io.Reader, opts DialogOptions) (*DialogResult, error) {
	if !addPageSupported {
		return nil, fmt.Errorf("%w: print dialog on this architecture", errdefs.ErrUnsupported)
	}
	data, err := io.ReadAll(src)
	if err != nil {
		return nil, fmt.Errorf("read PDF: %w", err)
	}
	a, err := com.NewApartment(com.STA)
	if err != nil {
		return nil, err
	}
	defer a.Close()

	var res *DialogResult
	err = a.Do(ctx, func() error {
		var err error
		res, err = runDialog(ctx, data, opts)
		return err
	})
	return res, err
}

func runDialog(ctx context.Context, data []byte, opts DialogOptions) (*DialogResult, error) {
	doc, err := loadPDFBytes(ctx, data)
	if err != nil {
		return nil, err
	}
	if doc.pages == 0 {
		doc.Close()
		return nil, fmt.Errorf("%w: PDF has no pages", errdefs.ErrInvalid)
	}
	d := &dialog{opts: opts, doc: doc, completion: -1, completed: make(chan struct{})}
	defer d.close()

	if d.sourceHandler, err = com.NewObject(sourceRequestedVT, d, iidIPrintTaskSourceRequestedHandler); err != nil {
		return nil, err
	}
	if d.completedHandler, err = com.NewObject(taskCompletedVT, d, iidPrintTaskCompletedHandler); err != nil {
		return nil, err
	}
	srcs, err := com.NewMultiObject(d,
		com.Interface{VT: documentSourceVT, IIDs: []com.GUID{iidIPrintDocumentSource, com.IIDIInspectable}},
		com.Interface{VT: pageSourceVT, IIDs: []com.GUID{iidIPrintDocumentPageSource}},
	)
	if err != nil {
		return nil, err
	}
	d.source = srcs[0]

	hwnd := opts.Owner
	if hwnd == 0 {
		if hwnd, err = com.HelperWindow(opts.Title); err != nil {
			return nil, err
		}
		defer com.DestroyWindow(hwnd)
	}

	interop, err := com.ActivationFactory("Windows.Graphics.Printing.PrintManager", &iidIPrintManagerInterop)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errdefs.ErrNoDialog, err)
	}
	defer interop.Release()
	var pm *com.Unknown
	if err := interop.CallHR("IPrintManagerInterop.GetForWindow", interopGetForWindow, uintptr(hwnd), uintptr(unsafe.Pointer(&iidIPrintManager)), uintptr(unsafe.Pointer(&pm))); err != nil {
		return nil, fmt.Errorf("%w: %v", errdefs.ErrNoDialog, err)
	}
	defer pm.Release()

	requested, err := com.NewObject(taskRequestedVT, d, iidPrintTaskRequestedHandler)
	if err != nil {
		return nil, err
	}
	defer requested.Release()
	var token int64
	if err := pm.CallHR("PrintManager.add_PrintTaskRequested", printManagerAddPrintTaskRequested, requested.Ptr(), uintptr(unsafe.Pointer(&token))); err != nil {
		return nil, err
	}
	defer pm.Call(printManagerRemovePrintTaskRequested, tokenArgs(token)...)

	var op *com.Unknown
	if err := interop.CallHR("IPrintManagerInterop.ShowPrintUIForWindowAsync", interopShowPrintUIForWindowAsync, uintptr(hwnd), uintptr(unsafe.Pointer(&iidIAsyncOperationBool)), uintptr(unsafe.Pointer(&op))); err != nil {
		return nil, fmt.Errorf("%w: %v", errdefs.ErrNoDialog, err)
	}
	defer op.Release()
	shown, err := com.AwaitBool(ctx, op)
	tracef("ShowPrintUIForWindowAsync returned %v, %v", shown, err)
	if err != nil {
		return nil, err
	}

	// The print task reports its end through Completed; wait for it while
	// pumping messages. Without a task (dialog closed before one existed)
	// the user canceled.
	d.mu.Lock()
	hasTask := d.task != nil
	d.mu.Unlock()
	if !hasTask {
		return nil, d.errOr(errdefs.ErrCanceled)
	}
	for {
		select {
		case <-d.completed:
			return d.result()
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			com.Pump(10)
		}
	}
}

// errOr returns the first callback error, or def.
func (d *dialog) errOr(def error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.errs) > 0 {
		return errors.Join(d.errs...)
	}
	return def
}

func (d *dialog) result() (*DialogResult, error) {
	d.mu.Lock()
	completion, chosen, job, jobErr := d.completion, d.chosen, d.job, d.jobErr
	d.mu.Unlock()
	tracef("print task completed: %d", completion)
	switch completion {
	case completionSubmitted:
		if jobErr != nil {
			return nil, jobErr
		}
		return &DialogResult{Job: job, Chosen: chosen}, nil
	case completionTaskCanceled, completionAbandoned:
		if !d.opts.PrintNow && job == nil && jobErr == errSettingsOnly {
			// We canceled the package ourselves after the user confirmed.
			return &DialogResult{Chosen: chosen}, nil
		}
		return nil, d.errOr(errdefs.ErrCanceled)
	default:
		if !d.opts.PrintNow && jobErr == errSettingsOnly {
			return &DialogResult{Chosen: chosen}, nil
		}
		return nil, d.errOr(fmt.Errorf("winprint: print task failed"))
	}
}

// errSettingsOnly marks a dialog confirmed with PrintNow == false.
var errSettingsOnly = errors.New("winprint: settings only")

func (d *dialog) onTaskRequested(args *com.Unknown) {
	var req *com.Unknown
	if err := args.CallHR("PrintTaskRequestedEventArgs.get_Request", taskRequestedArgsGetRequest, uintptr(unsafe.Pointer(&req))); err != nil {
		d.fail(err)
		return
	}
	defer req.Release()
	title, err := com.NewHString(d.opts.Title)
	if err != nil {
		d.fail(err)
		return
	}
	defer title.Delete()
	var task *com.Unknown
	if err := req.CallHR("PrintTaskRequest.CreatePrintTask", taskRequestCreatePrintTask, uintptr(title), d.sourceHandler.Ptr(), uintptr(unsafe.Pointer(&task))); err != nil {
		d.fail(err)
		return
	}
	if err := applyPresets(task, d.opts.Presets); err != nil {
		d.fail(err)
	}
	var token int64
	if err := task.CallHR("PrintTask.add_Completed", printTaskAddCompleted, d.completedHandler.Ptr(), uintptr(unsafe.Pointer(&token))); err != nil {
		d.fail(err)
		task.Release()
		return
	}
	d.mu.Lock()
	d.task, d.taskToken = task, token
	d.mu.Unlock()
	tracef("print task created")
}

func (d *dialog) onCompleted(task, args *com.Unknown) {
	var c int32
	if err := args.CallHR("PrintTaskCompletedEventArgs.get_Completion", taskCompletedArgsGetCompletion, uintptr(unsafe.Pointer(&c))); err != nil {
		d.fail(err)
	}
	chosen, err := readOptions(task)
	if err != nil {
		d.fail(err)
	}
	d.mu.Lock()
	d.completion, d.chosen = c, chosen
	d.mu.Unlock()
	select {
	case <-d.completed:
	default:
		close(d.completed)
	}
}

func taskOptions(task *com.Unknown) (*com.Unknown, error) {
	var opts *com.Unknown
	if err := task.CallHR("PrintTask.get_Options", printTaskGetOptions, uintptr(unsafe.Pointer(&opts))); err != nil {
		return nil, err
	}
	defer opts.Release()
	return opts.QueryInterface(&iidIPrintTaskOptionsCoreProperties)
}

func applyPresets(task *com.Unknown, p TaskOptions) error {
	props, err := taskOptions(task)
	if err != nil {
		return err
	}
	defer props.Release()
	var errs []error
	put := func(name string, slot int, v int32) {
		if v != 0 {
			if err := props.CallHR(name, slot, uintptr(uint32(v))); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if p.Copies > 0 {
		put("put_NumberOfCopies", optPutNumberOfCopies, int32(p.Copies))
	}
	put("put_MediaSize", optPutMediaSize, p.MediaSize)
	put("put_Orientation", optPutOrientation, p.Orientation)
	put("put_PrintQuality", optPutPrintQuality, p.Quality)
	put("put_ColorMode", optPutColorMode, p.Color)
	put("put_Duplex", optPutDuplex, p.Duplex)
	put("put_Collation", optPutCollation, p.Collation)
	return errors.Join(errs...)
}

func readOptions(task *com.Unknown) (TaskOptions, error) {
	props, err := taskOptions(task)
	if err != nil {
		return TaskOptions{}, err
	}
	defer props.Release()
	var o TaskOptions
	var errs []error
	get := func(name string, slot int, v *int32) {
		if err := props.CallHR(name, slot, uintptr(unsafe.Pointer(v))); err != nil {
			errs = append(errs, err)
		}
	}
	var copies int32
	get("get_NumberOfCopies", optGetNumberOfCopies, &copies)
	o.Copies = uint32(copies)
	get("get_MediaSize", optGetMediaSize, &o.MediaSize)
	get("get_Orientation", optGetOrientation, &o.Orientation)
	get("get_PrintQuality", optGetPrintQuality, &o.Quality)
	get("get_ColorMode", optGetColorMode, &o.Color)
	get("get_Duplex", optGetDuplex, &o.Duplex)
	get("get_Collation", optGetCollation, &o.Collation)
	return o, errors.Join(errs...)
}

// makeDocument spools the document into the target the dialog provides.
func (d *dialog) makeDocument(target *com.Unknown) error {
	tracef("MakeDocument")
	if !d.opts.PrintNow {
		target.Call(targetCancel)
		d.mu.Lock()
		d.jobErr = errSettingsOnly
		d.mu.Unlock()
		return nil
	}
	if PrinterGuard != nil {
		// The dialog does not tell which printer the user picked before the
		// job exists; refuse rather than risk a real device.
		target.Call(targetCancel)
		return fmt.Errorf("winprint: printing from the dialog is disabled by PrinterGuard")
	}
	r, err := newRenderer()
	if err != nil {
		return err
	}
	defer r.Close()
	job := newJob("", d.opts.Title)
	target.AddRef()
	job.res.keep(target.Release)
	pages := make([]int, d.doc.pages)
	for i := range pages {
		pages[i] = i
	}
	err = job.writeTarget(context.Background(), r, d.doc, target, pages, 0, paperLayout{})
	if err == nil {
		// The user picked the printer; look it up while the job is still
		// queued (it may leave the queue quickly).
		for i := 0; i < 20 && job.printerName() == ""; i++ {
			time.Sleep(50 * time.Millisecond)
		}
	}
	d.mu.Lock()
	d.job, d.jobErr = job, err
	d.mu.Unlock()
	if err != nil {
		job.res.release()
	}
	return err
}

func (d *dialog) close() {
	d.mu.Lock()
	task, token := d.task, d.taskToken
	d.task = nil
	d.mu.Unlock()
	if task != nil {
		task.Call(printTaskRemoveCompleted, tokenArgs(token)...)
		task.Release()
	}
	d.preview.close()
	for _, u := range []*com.Unknown{d.source, d.sourceHandler, d.completedHandler} {
		u.Release()
	}
	d.doc.Close()
}
